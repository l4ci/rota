package reap

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/rotatree"
	"github.com/l4ci/rota/internal/round"
)

func TestTakeCensusCountsEachClassLikeReapLists(t *testing.T) {
	now := time.Now()
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
	mk(sys, "rota-gate-tmp.old", 3*time.Hour)
	mk(sys, ReviewWorkspacePrefix+"1001", 3*time.Hour)
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

	rin := Input{Root: root, Base: "main", Git: run, Report: &round.Report{}}
	got := TakeCensus(context.Background(), CensusInput{Root: root, SystemTemp: sys, Reap: &rin, ProcessUses: func() ([]ProcUse, error) { return nil, nil }, Git: run})
	if got.LeakedTemp.N != 3 || got.LeakedTemp.Size != 3*1024 {
		t.Errorf("LeakedTemp = %+v, want 3 dirs, 3072 bytes", got.LeakedTemp)
	}
	// No project: the process table alone proves a dir free, so a census with
	// no reader holds every dir, as reap does.
	if bare := TakeCensus(context.Background(), CensusInput{SystemTemp: sys, Git: run}); bare.LeakedTemp.N != 0 {
		t.Errorf("no process table: LeakedTemp = %+v, want every dir held", bare.LeakedTemp)
	}
	if got.Prunable.N != 1 {
		t.Errorf("Prunable = %+v, want 1", got.Prunable)
	}
	// reap lists the same checkouts. The round always supplies a report; a nil one now fails closed.
	res, err := Find(context.Background(), Input{Root: root, Base: "main", Git: run, Report: &round.Report{}}, []string{KindWorktree})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Candidates) != got.Prunable.N {
		t.Errorf("reap listed %d worktrees; want %d", len(res.Candidates), got.Prunable.N)
	}
}

// A held entry (a live process uses it) is listed by reap but not reclaimable,
// so the census must not count it; a skipped root surfaces reap's warning.
func TestTakeCensusTmpAppliesReapHeldRules(t *testing.T) {
	now := time.Now()
	tempRoot, root := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(tempRoot, rotatree.IgnoreFile), []byte("*\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"free", "busy"} {
		p := filepath.Join(tempRoot, n)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(p, "f"), make([]byte, 1024), 0o644)
		ts := now.Add(-3 * time.Hour)
		os.Chtimes(filepath.Join(p, "f"), ts, ts)
		os.Chtimes(p, ts, ts)
	}
	run := func(context.Context, string, ...string) (git.Result, error) {
		return git.Result{Stdout: "worktree " + root + "\n"}, nil
	}
	in := Input{Root: root, Base: "main", Git: run, Report: &round.Report{}, TempRoot: tempRoot,
		ProcessUses: func() ([]ProcUse, error) {
			return []ProcUse{{PID: 7, Paths: []string{filepath.Join(tempRoot, "busy", "f")}}}, nil
		}}
	got := TakeCensus(context.Background(), CensusInput{Root: root, Reap: &in, Git: run})
	if got.StaleTmp.N != 1 || got.StaleTmp.Size != 1024 {
		t.Errorf("StaleTmp = %+v, want only the unheld entry (1, 1024)", got.StaleTmp)
	}
	res, err := Find(context.Background(), in, []string{KindTmp})
	if err != nil {
		t.Fatal(err)
	}
	free := 0
	for _, c := range res.Candidates {
		if c.Held == "" {
			free++
		}
	}
	if free != got.StaleTmp.N {
		t.Errorf("census counts %d, reap lists %d unheld", got.StaleTmp.N, free)
	}

	in.TempRoot = t.TempDir() // no rota .gitignore: reap skips it with a warning
	got = TakeCensus(context.Background(), CensusInput{Root: root, Reap: &in, Git: run})
	if got.StaleTmp.N != 0 || len(got.Warnings) != 1 || !strings.Contains(got.Warnings[0], "not a temp root") {
		t.Errorf("unowned root: StaleTmp=%+v warnings=%q", got.StaleTmp, got.Warnings)
	}
}

// The census's ProcessUses is authoritative for the systmp class even when Tmp
// carries none; with neither, every dir is held.
func TestTakeCensusSysTmpProcessUsesIsAuthoritative(t *testing.T) {
	sys := sysTemp(t, map[string]time.Duration{"rota-smoke.old": 3 * time.Hour})
	root := t.TempDir()
	run := func(context.Context, string, ...string) (git.Result, error) {
		return git.Result{Stdout: "worktree " + root + "\n\n"}, nil
	}
	tmp := Input{Root: root, Base: "main", Git: run, Report: &round.Report{}}
	free := func() ([]ProcUse, error) { return nil, nil }
	if got := TakeCensus(context.Background(), CensusInput{Root: root, SystemTemp: sys, Reap: &tmp, ProcessUses: free, Git: run}); got.LeakedTemp.N != 1 {
		t.Errorf("census reader ignored when Tmp has none: %+v", got.LeakedTemp)
	}
	if got := TakeCensus(context.Background(), CensusInput{Root: root, SystemTemp: sys, Reap: &tmp, Git: run}); got.LeakedTemp.N != 0 {
		t.Errorf("no reader anywhere must hold every dir: %+v", got.LeakedTemp)
	}
}

// A project dir outside any git repo has no checkouts to hold a dir, so the
// systmp count still runs; any other git failure leaves it unchecked.
func TestTakeCensusSysTmpToleratesNoGitRepoOnly(t *testing.T) {
	sys := sysTemp(t, map[string]time.Duration{"rota-smoke.old": 3 * time.Hour})
	failing := func(stderr string) git.Runner {
		return func(context.Context, string, ...string) (git.Result, error) {
			return git.Result{ExitCode: 128, Stderr: stderr}, nil
		}
	}
	free := func() ([]ProcUse, error) { return nil, nil }
	in := CensusInput{Root: t.TempDir(), SystemTemp: sys, ProcessUses: free, Git: failing("fatal: not a git repository (or any of the parent directories): .git")}
	if got := TakeCensus(context.Background(), in); got.LeakedTemp.N != 1 || len(got.Warnings) != 0 {
		t.Errorf("no repo: %+v warnings %v, want 1 dir and no warning", got.LeakedTemp, got.Warnings)
	}
	in.Git = failing("fatal: index file corrupt")
	if got := TakeCensus(context.Background(), in); got.LeakedTemp.N != 0 || len(got.Warnings) != 1 {
		t.Errorf("other git error: %+v warnings %v, want unchecked with a warning", got.LeakedTemp, got.Warnings)
	}
}

// doctor's prunable count is the prunable, unheld worktree candidates reap
// lists: a slot that owns a prunable checkout holds it, and an unreadable host
// lists none, so the census counts none.
func TestTakeCensusPrunableFollowsReapsWorktreeRules(t *testing.T) {
	root := t.TempDir()
	porcelain := "worktree " + root + "\n\n" +
		"worktree " + filepath.Join(root, ".worktrees", "gone") + "\nbranch refs/heads/kit/1-a\nprunable gone\n\n" +
		"worktree " + filepath.Join(root, ".worktrees", "mine") + "\nbranch refs/heads/kit/2-b\nprunable gone\n"
	run := func(context.Context, string, ...string) (git.Result, error) {
		return git.Result{Stdout: porcelain}, nil
	}
	cases := []struct {
		name string
		rep  *round.Report
	}{
		{"free", &round.Report{}},
		{"slot owns one", &round.Report{Rows: []round.Row{{Name: "mine", Registered: true}}}},
		{"host unreadable", &round.Report{Unavailable: []string{round.SourceHost}}},
		{"no report", nil},
	}
	for _, tc := range cases {
		in := Input{Root: root, Base: "main", Git: run, Report: tc.rep}
		res, err := Find(context.Background(), in, []string{KindWorktree})
		if err != nil {
			t.Fatal(err)
		}
		want := 0
		for _, cd := range res.Candidates {
			if cd.Prunable && cd.Held == "" {
				want++
			}
		}
		got := TakeCensus(context.Background(), CensusInput{Root: root, Reap: &in, Git: run})
		if got.Prunable.N != want {
			t.Errorf("%s: census Prunable = %d, reap lists %d", tc.name, got.Prunable.N, want)
		}
	}
	free := Input{Root: root, Base: "main", Git: run, Report: &round.Report{}}
	if n := TakeCensus(context.Background(), CensusInput{Root: root, Reap: &free, Git: run}).Prunable.N; n != 2 {
		t.Errorf("free: Prunable = %d, want 2", n)
	}
	owned := Input{Root: root, Base: "main", Git: run, Report: &round.Report{Rows: []round.Row{{Name: "mine", Registered: true}}}}
	if n := TakeCensus(context.Background(), CensusInput{Root: root, Reap: &owned, Git: run}).Prunable.N; n != 1 {
		t.Errorf("slot owns one: Prunable = %d, want 1", n)
	}
	down := Input{Root: root, Base: "main", Git: run, Report: &round.Report{Unavailable: []string{round.SourceHost}}}
	if n := TakeCensus(context.Background(), CensusInput{Root: root, Reap: &down, Git: run}).Prunable.N; n != 0 {
		t.Errorf("host unreadable: Prunable = %d, want 0", n)
	}
}

// Doctor in a directory that is no git repo counted 0 worktrees without a
// warning; going through Find must keep that, while other git failures warn.
func TestTakeCensusPrunableNoGitRepoIsSilentZero(t *testing.T) {
	root := t.TempDir()
	failing := func(msg string) git.Runner {
		return func(context.Context, string, ...string) (git.Result, error) {
			return git.Result{ExitCode: 128, Stderr: msg}, nil
		}
	}
	in := Input{Root: root, Base: "main", Git: failing("fatal: not a git repository (or any of the parent directories): .git"), Report: &round.Report{}}
	if got := TakeCensus(context.Background(), CensusInput{Root: root, Reap: &in, Git: in.Git}); got.Prunable.N != 0 || len(got.Warnings) != 0 {
		t.Errorf("no repo: %+v, want 0 and no warning", got)
	}
	in.Git = failing("fatal: boom")
	if got := TakeCensus(context.Background(), CensusInput{Root: root, Reap: &in, Git: in.Git}); got.Prunable.N != 0 || len(got.Warnings) != 1 {
		t.Errorf("other git error: %+v, want 0 and one warning", got)
	}
}
