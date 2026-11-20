package reap

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/rotatree"
	"github.com/l4ci/rota/internal/round"
)

func TestTakeCensusCountsEachClassLikeReapLists(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	sys, tempRoot, root := t.TempDir(), t.TempDir(), t.TempDir()
	mk := func(parent, name string, age time.Duration) {
		p := filepath.Join(parent, name)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		f := filepath.Join(p, "f")
		if err := os.WriteFile(f, make([]byte, 1024), 0o644); err != nil {
			t.Fatal(err)
		}
		ts := now.Add(-age)
		os.Chtimes(f, ts, ts)
		os.Chtimes(p, ts, ts)
	}
	mk(sys, "rota-smoke.old", 3*time.Hour)
	mk(sys, "rota-gate-old", 3*time.Hour)
	mk(sys, ReviewWorkspacePrefix+"old", 3*time.Hour)
	mk(sys, "rota-smoke.fresh", 10*time.Minute)
	mk(sys, "unrelated", 3*time.Hour)
	if err := os.WriteFile(filepath.Join(tempRoot, rotatree.IgnoreFile), []byte("*\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mk(tempRoot, "old-a", 3*time.Hour)
	mk(tempRoot, "fresh", 10*time.Minute)
	porcelain := "worktree " + root + "\n\n" +
		"worktree " + filepath.Join(root, ".worktrees", "gone") + "\nbranch refs/heads/kit/1-a\nprunable gone\n\n" +
		"worktree " + filepath.Join(t.TempDir(), "elsewhere") + "\nbranch refs/heads/kit/2-b\nprunable gone\n"
	run := func(context.Context, string, ...string) (git.Result, error) {
		return git.Result{Stdout: porcelain}, nil
	}

	got := TakeCensus(context.Background(), CensusInput{Root: root, SystemTemp: sys, TempRoot: tempRoot, Git: run, Now: now})
	if got.LeakedTemp.N != 3 || got.LeakedTemp.Size != 3*1024 {
		t.Errorf("LeakedTemp = %+v, want 3 dirs, 3072 bytes", got.LeakedTemp)
	}
	if got.StaleTmp.N != 1 || got.StaleTmp.Size != 1024 {
		t.Errorf("StaleTmp = %+v, want 1 entry, 1024 bytes", got.StaleTmp)
	}
	if got.Prunable.N != 1 {
		t.Errorf("Prunable = %+v, want 1", got.Prunable)
	}
	// reap lists the same checkouts, and the same tmp entries before age.
	// The round always supplies a report; a nil one now fails closed.
	res, err := Find(context.Background(), Input{Root: root, Base: "main", Git: run, TempRoot: tempRoot, Report: &round.Report{}}, []string{KindTmp, KindWorktree})
	if err != nil {
		t.Fatal(err)
	}
	var tmp, wt int
	for _, c := range res.Candidates {
		switch c.Kind {
		case KindTmp:
			tmp++
		case KindWorktree:
			wt++
		}
	}
	if tmp != 2 || wt != got.Prunable.N {
		t.Errorf("reap listed tmp=%d worktree=%d; want 2 (one stale) and %d", tmp, wt, got.Prunable.N)
	}
}
