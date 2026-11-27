package worker

import (
	"context"
	"errors"
	"fmt"
	"github.com/l4ci/rota/internal/gittest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/tracker"
)

// fakeForgeModes lists the world.mode values this fake understands. It is the
// in-process twin of test/fakes/fake_forge.py, which smoke sections 68 and 89
// drive as a gh/glab CLI; TestForgeFakesAgree runs every mode through both and
// compares what a caller can observe.
var fakeForgeModes = []string{"ok", "race", "fail", "noop", "elsewhere", "ff", "squash"}

// mergeOutcome is what a caller can observe of one merge request: the error's
// kind (not its text), the PR state and head the forge ends on, whether it
// reports a merge commit, and where the work landed on the origin.
type mergeOutcome struct {
	Err          string // "", "pin" or "protected"
	State        string
	HeadSHA      string // "" when unchanged from the pinned one
	MergeSet     bool
	MainHasWork  bool
	MainIsMerge  bool // main's tip is a two-parent merge commit
	StackHasWork bool // the work landed on a branch other than the PR's base
}

func errKind(msg string) string {
	switch {
	case msg == "":
		return ""
	case strings.Contains(msg, "does not match the pin"):
		return "pin"
	case strings.Contains(msg, "branch protection"):
		return "protected"
	}
	return "other: " + msg
}

// observeMerge reads the outcome off a world after a merge attempt.
func observeMerge(w *world, msg, pinned string) mergeOutcome {
	o := mergeOutcome{Err: errKind(msg), State: w.forgeWord("state"), MergeSet: w.forgeWord("merge") != ""}
	if sha := w.forgeWord("sha"); sha != pinned {
		o.HeadSHA = sha
	}
	has := func(ref string) bool {
		out, err := exec.Command("git", "-C", w.origin, "ls-tree", "--name-only", ref).Output()
		return err == nil && strings.Contains(string(out), "work.txt")
	}
	o.MainHasWork = has("main")
	o.StackHasWork = has("stack")
	if parents, err := exec.Command("git", "-C", w.origin, "rev-list", "--parents", "-n1", "main").Output(); err == nil {
		o.MainIsMerge = len(strings.Fields(string(parents))) == 3
	}
	return o
}

// TestForgeFakesAgree runs each FORGE_MODE through the in-process fakeForge and
// through test/fakes/fake_forge.py (as gh and as glab) on identical worlds, and
// requires the same observable outcome. A mode one fake does not know, or a
// merge one accepts and the other refuses, fails here instead of in smoke.
func TestForgeFakesAgree(t *testing.T) {
	py, err := filepath.Abs("../../test/fakes/fake_forge.py")
	if err != nil {
		t.Fatal(err)
	}
	type call struct {
		name, mode string
		pin        func(sha string) string // "" omits the pin
	}
	right := func(sha string) string { return sha }
	var cases []call
	for _, m := range fakeForgeModes {
		cases = append(cases, call{"pinned " + m, m, right})
	}
	cases = append(cases,
		call{"missing pin", "ok", func(string) string { return "" }},
		call{"stale pin", "ok", func(string) string { return strings.Repeat("a", 40) }})

	for _, c := range cases {
		for _, tool := range []string{"gh", "glab"} {
			t.Run(tool+"/"+c.name, func(t *testing.T) {
				// Go side.
				gw := newWorld(t, ghURL)
				gw.mode = c.mode
				gsha := gw.forgeWord("sha")
				f := &fakeForge{w: gw, provider: tool}
				var gmsg string
				if err := f.PRRequestMerge(bg, 7, tracker.MergeOpts{HeadSHA: c.pin(gsha)}); err != nil {
					gmsg = err.Error()
				}
				want := observeMerge(gw, gmsg, gsha)

				// Python side: a fresh, identical world.
				pw := newWorld(t, ghURL)
				pw.mode = c.mode
				psha := pw.forgeWord("sha")
				args := []string{py, "pr", "merge", "7"}
				flag := "--match-head-commit"
				if tool == "glab" {
					args, flag = []string{py, "mr", "merge", "7"}, "--sha"
				}
				if pin := c.pin(psha); pin != "" {
					args = append(args, flag, pin)
				}
				cmd := exec.Command("python3", args...)
				cmd.Env = append(os.Environ(), "FORGE_TOOL="+tool, "FORGE_DB="+pw.forgeDB, "FORGE_LOG="+pw.log, "FORGE_MODE="+c.mode)
				out, runErr := cmd.CombinedOutput()
				pmsg := ""
				if runErr != nil {
					pmsg = string(out)
				}
				got := observeMerge(pw, pmsg, psha)

				// The pinned sha is the same text on both sides only by construction;
				// compare shapes, not the random shas.
				if got != want {
					t.Errorf("fakes disagree on mode %q, %s\ngo:     %+v\npython: %+v\npython out: %s", c.mode, tool, want, got, out)
				}
			})
		}
	}
}

func TestPythonForgeFakeRefusesAnUnknownMode(t *testing.T) {
	w := newWorld(t, ghURL)
	cmd := exec.Command("python3", "../../test/fakes/fake_forge.py", "pr", "merge", "7")
	cmd.Env = append(os.Environ(), "FORGE_TOOL=gh", "FORGE_DB="+w.forgeDB, "FORGE_LOG="+w.log, "FORGE_MODE=nonsense")
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "unknown FORGE_MODE") {
		t.Fatalf("an unknown mode must be refused; err=%v out=%s", err, out)
	}
}

// fakeForge is the gate's Forge over a world's bare origin: one PR whose state
// lives in the world's forge DB, and a merge that really lands on the origin.
// It lies the ways a real forge does, by world.mode: race (a push after the
// check), fail (branch protection), noop (merge reports success and merges
// nothing), elsewhere (merged into another branch), ff and squash (no merge
// commit, a squash commit). Like a real forge it refuses a merge whose pin is
// missing or is not the PR's head. Every call is appended to the world's log.
type fakeForge struct {
	w        *world
	provider string
}

func (f *fakeForge) logf(format string, a ...any) {
	fh, err := os.OpenFile(f.w.log, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		f.w.t.Fatal(err)
	}
	defer fh.Close()
	fmt.Fprintf(fh, "%s %s\n", f.provider, fmt.Sprintf(format, a...))
}

func (f *fakeForge) PRView(_ context.Context, pr int) (tracker.PRInfo, error) {
	f.logf("PRView %d", pr)
	w := f.w
	info := tracker.PRInfo{Head: w.forgeWord("head"), HeadSHA: w.forgeWord("sha"), Base: w.forgeWord("base"),
		State: w.forgeWord("state"), MergeSHA: w.forgeWord("merge"), Title: w.forgeWord("title"), Body: w.forgeWord("body")}
	// A second PR (a train of two) names its own head and sha: "head#8", "sha#8".
	if h := w.forgeWord(fmt.Sprintf("head#%d", pr)); h != "" {
		info.Head, info.HeadSHA = h, w.forgeWord(fmt.Sprintf("sha#%d", pr))
	}
	// "body#9" is that PR's own body, exact (the integration PR rota writes).
	if b := w.forgeWord(fmt.Sprintf("body#%d", pr)); b != "" {
		info.Body = b
	}
	// "state#9" and "base#9" are that PR's own state and target branch.
	if st := w.forgeWord(fmt.Sprintf("state#%d", pr)); st != "" {
		info.State = st
	}
	if b := w.forgeWord(fmt.Sprintf("base#%d", pr)); b != "" {
		info.Base = b
	}
	if w.mode == "ff" {
		info.MergeSHA = "" // a fast-forward merge has no merge commit
	}
	// A forge that lags a push: "staleHeadFor" reads still name the old head.
	if n, _ := strconv.Atoi(w.forgeWord("staleHeadFor")); n > 0 {
		w.forge("staleHeadFor", strconv.Itoa(n-1))
		info.HeadSHA = strings.Repeat("0", 40)
	}
	return info, nil
}

// PRSetBase retargets one PR: it records the new target as that PR's "base#N".
func (f *fakeForge) PRSetBase(_ context.Context, pr int, base string) error {
	f.logf("PRSetBase %d %s", pr, base)
	if f.w.forgeWord("setBaseErr") != "" {
		return errors.New(f.w.forgeWord("setBaseErr"))
	}
	f.w.forge(fmt.Sprintf("base#%d", pr), base)
	return nil
}

// PRSetBody rewrites one PR's body: the forge DB word "body#N".
func (f *fakeForge) PRSetBody(_ context.Context, pr int, body string) error {
	f.logf("PRSetBody %d", pr)
	f.w.forge(fmt.Sprintf("body#%d", pr), body)
	return nil
}

// PRMergeable answers from the forge DB word "mergeable" (clean, conflict,
// unknown, or error to fail), "mergeReason" naming why. Unset it is clean and
// unlogged, so the frozen forge logs of the other cases keep their shape. A
// fresh "unknownFor" count of unknown answers precedes the real one.
func (f *fakeForge) PRMergeable(context.Context, int) (tracker.Mergeability, error) {
	w := f.w
	word := w.forgeWord("mergeable")
	if word == "" {
		return tracker.Mergeability{State: tracker.MergeClean}, nil
	}
	f.logf("PRMergeable")
	if n, _ := strconv.Atoi(w.forgeWord("unknownFor")); n > 0 {
		w.forge("unknownFor", strconv.Itoa(n-1))
		return tracker.Mergeability{State: tracker.MergeUnknown, Reason: "UNKNOWN"}, nil
	}
	if word == "error" {
		return tracker.Mergeability{}, errors.New("simulated: gh pr view failed")
	}
	return tracker.Mergeability{State: word, Reason: w.forgeWord("mergeReason")}, nil
}

// OpenPRs lists the world's PR only when the world opted in with listed=1, so
// the cases that record no PR keep their frozen forge log (a listing is not
// logged). listError makes the listing fail.
func (f *fakeForge) OpenPRs(context.Context) ([]tracker.PR, error) {
	if f.w.forgeWord("listError") != "" {
		return nil, errors.New(f.w.forgeWord("listError"))
	}
	if f.w.forgeWord("listed") != "1" {
		return nil, nil
	}
	return []tracker.PR{{Number: 7, SameRepository: true, Branch: f.w.forgeWord("head"), URL: ghURL}}, nil
}

// ClosedNumbers reads `Closes #N` lines. Get and RemoveLabels serve one issue
// whose state ("issueState", default open) and labels ("issueLabels", comma
// separated) live in the forge DB; "issueErr" makes both fail.
func (f *fakeForge) ClosedNumbers(body string) []int {
	var out []int
	for _, m := range regexp.MustCompile(`(?i)(?:closes|fixes) #(\d+)`).FindAllStringSubmatch(body, -1) {
		n, _ := strconv.Atoi(m[1])
		out = append(out, n)
	}
	return out
}

// ClosingRefs answers from the forge DB word "closingRefs": unset the forge
// cannot report them, "error" fails the read, "none" none, else comma-separated issue numbers.
func (f *fakeForge) ClosingRefs(context.Context, int) ([]int, bool, error) {
	word := f.w.forgeWord("closingRefs")
	if word == "" {
		return nil, false, nil
	}
	if word == "error" {
		return nil, false, fmt.Errorf("fake gh: unsupported: api graphql")
	}
	var out []int
	for _, p := range strings.Split(word, ",") {
		if n, err := strconv.Atoi(p); err == nil {
			out = append(out, n)
		}
	}
	return out, true, nil
}

// ClosesIssue is the keyword convention of the forge's own tracker.
func (f *fakeForge) ClosesIssue(key tracker.IssueKey, c tracker.ClosingSource) bool {
	n, ok := key.Number()
	return ok && slices.Contains(f.ClosedNumbers(c.Body), n)
}

func (f *fakeForge) Get(_ context.Context, n tracker.IssueKey, withComments bool) (tracker.Issue, error) {
	f.logf("Get %s", n)
	if msg := f.w.forgeWord("issueErr"); msg != "" {
		return tracker.Issue{}, errors.New(msg)
	}
	state := f.w.forgeWord("issueState")
	if state == "" {
		state = "open"
	}
	is := tracker.Issue{Key: n, State: state, Body: f.w.forgeWord("issueBody")}
	l := f.w.forgeWord("issueLabels")
	if per := f.w.forgeWord("issueLabels." + string(n)); per != "" {
		l = per // labels of one issue, beside the default every other issue gets
	}
	if l != "" {
		is.Labels = strings.Split(l, ",")
	}
	// "issueComments" is a JSON list of {author, body}, served when asked for.
	if raw := f.w.forgeWord("issueComments"); raw != "" && withComments {
		var cs []struct{ Author, Body string }
		if err := jsonUnmarshal([]byte(raw), &cs); err != nil {
			return tracker.Issue{}, err
		}
		for i, c := range cs {
			is.Comments = append(is.Comments, tracker.Comment{ID: strconv.Itoa(i + 1), Author: c.Author, Body: c.Body})
		}
	}
	return is, nil
}

func (f *fakeForge) RemoveLabels(_ context.Context, n tracker.IssueKey, labels []string) error {
	f.logf("RemoveLabels %s %s", n, strings.Join(labels, ","))
	if f.w.forgeWord("removeErr") != "" {
		return errors.New(f.w.forgeWord("removeErr"))
	}
	var keep []string
	for _, l := range strings.Split(f.w.forgeWord("issueLabels"), ",") {
		if !slices.Contains(labels, l) {
			keep = append(keep, l)
		}
	}
	f.w.forge("issueLabels", strings.Join(keep, ","))
	return nil
}

func (f *fakeForge) PRRequestMerge(_ context.Context, pr int, o tracker.MergeOpts) error {
	f.logf("PRRequestMerge %d pin=%s deleteBranch=%v", pr, o.HeadSHA, o.DeleteBranch)
	w := f.w
	if w.mode == "race" {
		w.forge("sha", strings.Repeat("f", 40)) // someone pushed after the gate's check
	}
	if sha := w.forgeWord("sha"); o.HeadSHA != sha {
		return fmt.Errorf("simulated: head commit %s does not match the pin %q", sha, o.HeadSHA)
	}
	switch w.mode {
	case "fail":
		return errors.New("simulated: merge blocked by branch protection")
	case "noop":
		return nil
	}
	t := w.t.TempDir()
	target, head := w.forgeWord("base"), w.forgeWord("head")
	if f := w.forgeWord("pushBeforeMerge"); f != "" { // the base moves between the gate's check and the merge
		w.originCommit(f)
	}
	gittest.Run(w.t, t, "clone", "-q", w.forgeWord("origin"), ".")
	if target != "main" && w.mode != "elsewhere" { // a milestone base: merge onto it, not the clone's default branch
		gittest.Run(w.t, t, "checkout", "-q", "-B", target, "origin/"+target)
	}
	if w.mode == "elsewhere" {
		target = "stack"
		gittest.Run(w.t, t, "checkout", "-q", "-b", "stack", "origin/"+w.forgeWord("base"))
	}
	if w.mode == "ff" {
		gittest.Run(w.t, t, "merge", "--ff-only", "-q", "origin/"+head)
	} else {
		gittest.Run(w.t, t, "merge", "--no-ff", "-q", "-m", "merge pr", "origin/"+head)
		w.forge("merge", gittest.Run(w.t, t, "rev-parse", "HEAD"))
	}
	gittest.Run(w.t, t, "push", "-q", "origin", target)
	if f := w.forgeWord("pushAfterMerge"); f != "" { // someone lands work right after the merge
		w.originCommit(f)
	}
	w.forge("state", "MERGED")
	return nil
}

// AddMRNote logs the comment and keeps its body in the forge DB word
// "note", so a test reads what the gate posted. "noteErr" makes it fail.
func (f *fakeForge) AddMRNote(_ context.Context, pr int, body string) (string, error) {
	f.logf("AddMRNote %d", pr)
	if msg := f.w.forgeWord("noteErr"); msg != "" {
		return "", errors.New(msg)
	}
	f.w.forge("note", f.w.forgeWord("note")+body)
	return "1", nil
}

// CommitChecks reports one check, "ci/test", on a commit pushed to the
// origin's rota/ci/* branches: failure when its tree holds the file named by
// "ciFail", else success. "ci" overrides it: none (CI never started), pending
// (still running) or skipped (finished, tested nothing); late adds a failing
// "ci/late" from the second call on. A commit not pushed there has no checks.
// "ciMoveBase" names a file pushed to the origin's main on the first call, as
// if someone landed work while CI ran. "ciScript" scripts the answers
// instead: polls split by "|", each a comma list of name=state, the last
// repeated once the script runs out.
func (f *fakeForge) CommitChecks(_ context.Context, sha string) ([]tracker.CheckRun, error) {
	f.logf("CommitChecks %s", sha[:7])
	w := f.w
	if script := w.forgeWord("ciScript"); script != "" {
		polls := strings.Split(script, "|")
		n, _ := strconv.Atoi(w.forgeWord("ciScriptN"))
		w.forge("ciScriptN", strconv.Itoa(n+1))
		var checks []tracker.CheckRun
		for _, c := range strings.Split(polls[min(n, len(polls)-1)], ",") {
			name, state, _ := strings.Cut(c, "=")
			checks = append(checks, tracker.CheckRun{Name: name, State: state})
		}
		return checks, nil
	}
	switch w.forgeWord("ci") {
	case "none":
		return nil, nil
	case "pending":
		return []tracker.CheckRun{{Name: "ci/test", State: tracker.CheckPending}}, nil
	case "skipped":
		return []tracker.CheckRun{{Name: "ci/test", State: tracker.CheckSkipped}}, nil
	}
	g := func(args ...string) (string, error) {
		out, err := exec.Command("git", append([]string{"-C", w.origin}, args...)...).Output()
		return strings.TrimSpace(string(out)), err
	}
	if f := w.forgeWord("ciMoveBase"); f != "" {
		w.forge("ciMoveBase", "")
		w.originCommit(f)
	}
	refs, _ := g("for-each-ref", "--format=%(objectname)", "refs/heads/rota/ci/")
	if !slices.Contains(strings.Fields(refs), sha) {
		return nil, nil
	}
	state := tracker.CheckSuccess
	if bad := w.forgeWord("ciFail"); bad != "" {
		if _, err := g("cat-file", "-e", sha+":"+bad); err == nil {
			state = tracker.CheckFailure
		}
	}
	checks := []tracker.CheckRun{{Name: "ci/test", State: state, URL: "https://ci.example/" + sha[:7]}}
	if w.forgeWord("ci") == "late" {
		if w.forgeWord("ciCalls") != "" {
			checks = append(checks, tracker.CheckRun{Name: "ci/late", State: tracker.CheckFailure})
		}
		w.forge("ciCalls", "1")
	}
	return checks, nil
}

// IssueAuthorship answers who wrote the issue and what it says: the forge DB
// word "author" (default alice; "authorUnknown" makes it empty), "editor"
// (default none) and "issueBody". "editorsErr" makes it fail.
func (f *fakeForge) IssueAuthorship(_ context.Context, n tracker.IssueKey) (tracker.Authorship, error) {
	f.logf("IssueAuthorship %s", n)
	if msg := f.w.forgeWord("editorsErr"); msg != "" {
		return tracker.Authorship{}, errors.New(msg)
	}
	// "edited" says the body was edited; with no "editor" the editor is unknown
	a := tracker.Authorship{
		Editor: f.w.forgeWord("editor"), Body: f.w.forgeWord("issueBody"),
		Edited: f.w.forgeWord("edited") != "" || f.w.forgeWord("editor") != "",
	}
	if f.w.forgeWord("authorUnknown") == "" {
		a.Author = f.w.forgeWord("author")
		if a.Author == "" {
			a.Author = "alice"
		}
	}
	return a, nil
}

// CanPush is true for every login but those in the comma list "readers";
// "canPushErr" makes it fail.
func (f *fakeForge) CanPush(_ context.Context, login string) (bool, error) {
	f.logf("CanPush %s", login)
	if msg := f.w.forgeWord("canPushErr"); msg != "" {
		return false, errors.New(msg)
	}
	return !slices.Contains(strings.Split(f.w.forgeWord("readers"), ","), login), nil
}

var _ tracker.IssueTrust = (*fakeForge)(nil)

// TestTrackerFakeMergeAgreesWithForgeFake runs the pin scenarios through
// test/fakes/fake_tracker.py (the shim sections 56 and 89 reach through `gh`
// and `glab`) and through fake_forge.py: same error kind either way.
// The named exception: an absent pin merges on the tracker fake and is
// refused by the forge fake, which models a forge that requires the pin.
func TestTrackerFakeMergeAgreesWithForgeFake(t *testing.T) {
	forgePy, _ := filepath.Abs("../../test/fakes/fake_forge.py")
	trackerPy, _ := filepath.Abs("../../test/fakes/fake_tracker.py")
	stale := strings.Repeat("a", 40)
	for _, tool := range []string{"gh", "glab"} {
		for _, pin := range []string{"right", "stale"} {
			t.Run(tool+"/"+pin, func(t *testing.T) {
				db := filepath.Join(t.TempDir(), "t.json")
				run := func(args ...string) (string, error) {
					cmd := exec.Command("python3", append([]string{"-c", "import sys; sys.path.insert(0, sys.argv[1]); sys.argv = sys.argv[1:]; import fake_tracker; sys.exit(fake_tracker.main())", filepath.Dir(trackerPy), tool}, args...)...)
					cmd.Env = append(os.Environ(), "FAKE_TRACKER_DB="+db)
					out, err := cmd.CombinedOutput()
					return string(out), err
				}
				create := []string{"pr", "create", "--title", "t", "--head", "w1"}
				view := []string{"pr", "view", "1", "--json", "headRefOid"}
				merge, flag := []string{"pr", "merge", "1"}, "--match-head-commit"
				if tool == "glab" {
					create = []string{"mr", "create", "--title", "t", "--source-branch", "w1"}
					view = []string{"mr", "view", "1", "--output", "json"}
					merge, flag = []string{"mr", "merge", "1"}, "--sha"
				}
				if out, err := run(create...); err != nil {
					t.Fatalf("create: %v\n%s", err, out)
				}
				out, err := run(view...)
				if err != nil {
					t.Fatalf("view: %v\n%s", err, out)
				}
				pinned := shaRe.FindString(out)
				if pin == "stale" {
					pinned = stale
				}
				tkind := ""
				if tout, terr := run(append(merge, flag, pinned)...); terr != nil {
					tkind = errKind(tout)
				}

				w := newWorld(t, ghURL)
				fpin := w.forgeWord("sha")
				if pin == "stale" {
					fpin = stale
				}
				args := []string{forgePy, "pr", "merge", "7", flag, fpin}
				if tool == "glab" {
					args[1] = "mr"
				}
				cmd := exec.Command("python3", args...)
				cmd.Env = append(os.Environ(), "FORGE_TOOL="+tool, "FORGE_DB="+w.forgeDB, "FORGE_LOG="+w.log, "FORGE_MODE=ok")
				fout, ferr := cmd.CombinedOutput()
				fkind := ""
				if ferr != nil {
					fkind = errKind(string(fout))
				}
				if tkind != fkind {
					t.Errorf("pin %s on %s: tracker fake err %q, forge fake err %q\nforge out: %s", pin, tool, tkind, fkind, fout)
				}
			})
		}
	}
}
