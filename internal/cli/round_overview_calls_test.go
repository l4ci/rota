package cli

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/tui"
)

// readOnlyCall is the forge vocabulary of a monitoring pass: list and view an
// issue or a PR, read a comment thread, nothing else. Any other call is a
// mutation (or an unreviewed read) and fails the test that made it.
var readOnlyCall = regexp.MustCompile(`^(issue (list|view) |pr (list|view|status|checks) |api [^ ]*(issues|pulls)/[0-9]+(/comments)?( |$)|repo view |auth status)`)

// snapshotTree reads every file under dir, so a test can prove none changed.
func snapshotTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		out[p] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// #1873 AC-1: a full overview pass, then the i and p keys, write nothing: every
// forge call is a read, the opener is the only side effect and it is a fake,
// and the registry, ledger and config on disk are byte-identical afterwards.
func TestRoundOverviewMonitoringIsReadOnly(t *testing.T) {
	r := newPerfRig(t)
	t.Setenv("TERM", "xterm")
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	os.Chdir(r.root)
	before := snapshotTree(t, filepath.Join(r.root, ".rota"))
	if len(before) == 0 {
		t.Fatal("no .rota files to compare: the snapshot proves nothing")
	}

	m, err := roundStatusView(&Ctx{Deps: r.deps}, Result{})
	if err != nil {
		t.Fatal(err)
	}
	rm := m.(*roundScreen)
	var opened []string
	rm.open = func(u string) error { opened = append(opened, u); return nil }
	var model tui.Model = rm
	model = runCmd(model, model.(*roundScreen).Init())
	for _, k := range []string{"j", "j", "i", "p", "r"} {
		var cmd tui.Cmd
		model, cmd = press(model, k)
		if cmd.Exec != nil || cmd.Async != nil {
			model = runCmd(model, cmd)
		}
	}

	if len(opened) == 0 {
		t.Error("i/p opened nothing: the fixture no longer exercises the open path")
	}
	for _, c := range r.callList() {
		if !readOnlyCall.MatchString(strings.TrimPrefix(c, "gh ")) {
			t.Errorf("a monitoring path made a call that is not a plain read: %s", c)
		}
	}
	after := snapshotTree(t, filepath.Join(r.root, ".rota"))
	for p, b := range after {
		if old, ok := before[p]; !ok || old != b {
			t.Errorf("%s changed during monitoring", p)
		}
	}
	for p := range before {
		if _, ok := after[p]; !ok {
			t.Errorf("%s removed during monitoring", p)
		}
	}
}

// #1873 AC-4: one pass with support items reads each labelled issue's claims
// once (the candidates read of the same issues is answered from the pass's
// cache), and the pass's total tracker calls are pinned so growth shows up here
// instead of as a slower screen. The claim threads of the slots' own issues
// (1-3) are read by both status and the candidates; that is outside the
// support path and counted in the total.
func TestRoundOverviewPassCallCount(t *testing.T) {
	r := newPerfRig(t)
	t.Setenv("TERM", "xterm")
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	os.Chdir(r.root)
	m, err := roundStatusView(&Ctx{Deps: r.deps}, Result{})
	if err != nil {
		t.Fatal(err)
	}
	snap, err := m.(*roundScreen).load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Support) != 3 {
		t.Fatalf("fixture has %d support items, want 3", len(snap.Support))
	}
	calls := r.callList()
	for _, id := range []string{"10", "11", "12"} { // the support items
		want := "gh api repos/{owner}/{repo}/issues/" + id + "/comments --paginate"
		n := 0
		for _, c := range calls {
			if c == want {
				n++
			}
		}
		if n != 1 {
			t.Errorf("claims of support issue %s read %d times in one pass, want 1", id, n)
		}
	}
	const pinned = 21
	if len(calls) > pinned {
		t.Errorf("%d forge calls in one pass, pinned at %d:\n%s", len(calls), pinned, strings.Join(calls, "\n"))
	}
}

// #1873 AC-5: a failed issue-URL read is a Note, not links that vanish.
func TestRoundOverviewIssueURLFailureIsNoted(t *testing.T) {
	r := newPerfRig(t)
	r.failIf = func(joined string) bool { return strings.HasPrefix(joined, "issue list") }
	t.Setenv("TERM", "xterm")
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	os.Chdir(r.root)
	m, err := roundStatusView(&Ctx{Deps: r.deps}, Result{})
	if err != nil {
		t.Fatal(err)
	}
	snap, err := m.(*roundScreen).load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.IssueURLs) != 0 {
		t.Fatalf("issue list failed yet URLs were read: %v", snap.IssueURLs)
	}
	var noted bool
	for _, n := range snap.Notes {
		noted = noted || strings.Contains(n, "issue links unavailable")
	}
	if !noted {
		t.Errorf("a failed issue-URL read left no note: %v", snap.Notes)
	}
}

// #1873 AC-10: q before the pass goroutine has registered its cancel stops the
// pass for good: it must not read the tracker for a closed screen.
func TestRoundOverviewQuitBeforePassStarts(t *testing.T) {
	loads := 0
	load := func(context.Context) (roundSnap, error) { loads++; return roundSnap{}, nil }
	m := openingScreen(newRoundScreen(overviewFixture(), load, newClock().now, roundRefresh))
	cmd := m.Init() // the Async closure exists, its goroutine has not run
	if _, q := press(m, "q"); !q.Quit {
		t.Fatal("q did not leave")
	}
	if err := cmd.Async(context.Background()); err != nil {
		t.Fatal(err)
	}
	if loads != 0 {
		t.Errorf("a pass ran %d time(s) after the screen was closed", loads)
	}
}
