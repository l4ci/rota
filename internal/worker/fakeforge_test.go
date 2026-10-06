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
		State: w.forgeWord("state"), MergeSHA: w.forgeWord("merge"), Body: w.forgeWord("body")}
	if w.mode == "ff" {
		info.MergeSHA = "" // a fast-forward merge has no merge commit
	}
	return info, nil
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
	for _, m := range regexp.MustCompile(`(?i)closes #(\d+)`).FindAllStringSubmatch(body, -1) {
		n, _ := strconv.Atoi(m[1])
		out = append(out, n)
	}
	return out
}

func (f *fakeForge) Get(_ context.Context, n int, _ bool) (tracker.Issue, error) {
	f.logf("Get %d", n)
	if msg := f.w.forgeWord("issueErr"); msg != "" {
		return tracker.Issue{}, errors.New(msg)
	}
	state := f.w.forgeWord("issueState")
	if state == "" {
		state = "open"
	}
	is := tracker.Issue{Number: n, State: state}
	if l := f.w.forgeWord("issueLabels"); l != "" {
		is.Labels = strings.Split(l, ",")
	}
	return is, nil
}

func (f *fakeForge) RemoveLabels(_ context.Context, n int, labels []string) error {
	f.logf("RemoveLabels %d %s", n, strings.Join(labels, ","))
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
	w.forge("state", "MERGED")
	return nil
}

// CommitChecks reports one check, "ci/test", on a commit pushed to the
// origin's rota/ci/* branches: failure when its tree holds the file named by
// "ciFail", else success. "ci" overrides it: none (CI never started) or
// pending (still running). A commit not pushed there has no checks.
func (f *fakeForge) CommitChecks(_ context.Context, sha string) ([]tracker.CheckRun, error) {
	f.logf("CommitChecks %s", sha[:7])
	w := f.w
	switch w.forgeWord("ci") {
	case "none":
		return nil, nil
	case "pending":
		return []tracker.CheckRun{{Name: "ci/test", State: tracker.CheckPending}}, nil
	}
	g := func(args ...string) (string, error) {
		out, err := exec.Command("git", append([]string{"-C", w.origin}, args...)...).Output()
		return strings.TrimSpace(string(out)), err
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
	return []tracker.CheckRun{{Name: "ci/test", State: state, URL: "https://ci.example/" + sha[:7]}}, nil
}
