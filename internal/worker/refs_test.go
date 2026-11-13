package worker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/verdict"
)

// refsWorld is a green integration branch whose project renews reference
// files with test.refs: cmd writes one tracked-pattern file and, when stray is
// set, one outside it.
func refsWorld(t *testing.T, cmd string) (*world, string) {
	t.Helper()
	w := integrationWorld(t)
	tip := integrationPR(t, w)
	w.setConfig(`{"test":{"full":["true"],"refs":[` + jsonString(cmd) + `],"refsPattern":"^refs/.*\\.png$"}}`)
	return w, tip
}

func (w *world) refs() (RefsResult, error) {
	return w.env(false).Refs(bg, w.dir, RefsOpts{Branch: intBranch})
}

func TestRefsCommitsOnlyReferenceFilesAfterLook(t *testing.T) {
	w, tip := refsWorld(t, "mkdir -p refs && echo new > refs/a.png")
	w.record(intBranch, tip, verdict.Look)
	res, err := w.refs()
	if err != nil || res.Verdict != RefsRenewed || res.Commit == "" {
		t.Fatalf("renew: %+v %v", res, err)
	}
	if got := strings.Fields(w.remoteLog("--format=%H", "-1", intBranch)); len(got) != 1 || got[0] != res.Commit {
		t.Fatalf("one pushed commit at the tip: %v want %s", got, res.Commit)
	}
	files := strings.Fields(gittest.Run(t, w.origin, "diff-tree", "--no-commit-id", "--name-only", "-r", res.Commit))
	if len(files) != 1 || files[0] != "refs/a.png" {
		t.Fatalf("the commit touches only reference files, got %v", files)
	}
	if parent := gittest.Run(t, w.origin, "rev-parse", res.Commit+"^"); parent != tip {
		t.Fatalf("exactly one commit on the tip: parent %s tip %s", parent, tip)
	}
}

func TestRefsRefusesWithoutLookApproval(t *testing.T) {
	w, tip := refsWorld(t, "mkdir -p refs && echo new > refs/a.png")
	// A look at an older sha, and a non-look verdict at the tip, are no approval.
	w.record(intBranch, strings.Repeat("0", 40), verdict.Look)
	w.record(intBranch, tip, verdict.ReviewSpec)
	res, err := w.refs()
	if err != nil || res.Verdict != RefsNoLook || !strings.Contains(res.Message, "look") {
		t.Fatalf("want a refusal naming the missing look approval: %+v %v", res, err)
	}
	if got := gittest.Run(t, w.origin, "rev-parse", intBranch); got != tip {
		t.Fatalf("nothing may be pushed, tip moved to %s", got)
	}
}

func TestRefsRefusesAFileOutsideThePattern(t *testing.T) {
	w, tip := refsWorld(t, "mkdir -p refs && echo new > refs/a.png && echo x > stray.txt")
	w.record(intBranch, tip, verdict.Look)
	res, err := w.refs()
	if err != nil || res.Verdict != RefsOutside || !strings.Contains(res.Message, "stray.txt") {
		t.Fatalf("want a refusal naming the stray file: %+v %v", res, err)
	}
	if got := gittest.Run(t, w.origin, "rev-parse", intBranch); got != tip {
		t.Fatalf("nothing may be committed, tip moved to %s", got)
	}
	if _, err := os.Stat(filepath.Join(w.dir, "stray.txt")); err == nil {
		t.Fatal("the command ran in the main checkout")
	}
}
