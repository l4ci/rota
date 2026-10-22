package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/l4ci/rota/internal/tracker"
)

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
	return []tracker.PR{{Number: 7, Branch: f.w.forgeWord("head"), URL: ghURL}}, nil
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

func (f *fakeForge) Get(_ context.Context, n tracker.IssueKey, _ bool) (tracker.Issue, error) {
	f.logf("Get %s", n)
	if msg := f.w.forgeWord("issueErr"); msg != "" {
		return tracker.Issue{}, errors.New(msg)
	}
	state := f.w.forgeWord("issueState")
	if state == "" {
		state = "open"
	}
	is := tracker.Issue{Key: n, State: state, Body: f.w.forgeWord("issueBody")}
	if l := f.w.forgeWord("issueLabels"); l != "" {
		is.Labels = strings.Split(l, ",")
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
	g := func(args ...string) string {
		cmd := exec.Command("git", append([]string{"-c", "user.name=f", "-c", "user.email=f@f"}, args...)...)
		cmd.Dir = t
		out, err := cmd.CombinedOutput()
		if err != nil {
			w.t.Fatalf("fake forge: git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	target, head := w.forgeWord("base"), w.forgeWord("head")
	if f := w.forgeWord("pushBeforeMerge"); f != "" { // the base moves between the gate's check and the merge
		w.originCommit(f)
	}
	g("clone", "-q", w.forgeWord("origin"), ".")
	if w.mode == "elsewhere" {
		target = "stack"
		g("checkout", "-q", "-b", "stack", "origin/"+w.forgeWord("base"))
	}
	if w.mode == "ff" {
		g("merge", "--ff-only", "-q", "origin/"+head)
	} else {
		g("merge", "--no-ff", "-q", "-m", "merge pr", "origin/"+head)
		w.forge("merge", g("rev-parse", "HEAD"))
	}
	g("push", "-q", "origin", target)
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

var _ IssueTrust = (*fakeForge)(nil)
var _ IssueTrust = (*tracker.GitHub)(nil)
