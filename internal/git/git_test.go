package git

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/gittest"
)

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	gittest.Run(t, dir, args...)
}

func newRepo(t *testing.T) string {
	t.Helper()
	dir := gittest.TempDir(t)
	gittest.Init(t, dir, "main")
	gittest.Commit(t, dir, "first", "a.txt", "a")
	return dir
}

func TestRunMapsExitCodeToResult(t *testing.T) {
	dir := newRepo(t)
	res, err := Repo{dir}.Run(context.Background(), "rev-parse", "--verify", "-q", "nope")
	if err != nil || res.Code == 0 {
		t.Fatalf("got %+v, %v; want non-zero Result and nil error", res, err)
	}
	res, err = Repo{dir}.Run(context.Background(), "rev-parse", "--git-dir")
	if err != nil || res.Code != 0 || strings.TrimSpace(res.Stdout) != ".git" {
		t.Fatalf("got %+v, %v", res, err)
	}
}

func TestRunNoGit(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, err := (Repo{}).Run(context.Background(), "--version"); !errors.Is(err, ErrNoGit) {
		t.Fatalf("err = %v, want ErrNoGit", err)
	}
}

func TestRunContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (Repo{}).Run(ctx, "--version"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestRunTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	time.Sleep(time.Millisecond)
	if _, err := (Repo{}).Run(ctx, "--version"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
}

func TestRunForcesCLocale(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LC_ALL", "de_DE.UTF-8")
	res, err := Repo{dir}.Run(context.Background(), "status")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Stderr, "not a git repository") {
		t.Fatalf("stderr = %q, want English message", res.Stderr)
	}
}

func TestQueries(t *testing.T) {
	ctx := context.Background()
	dir := newRepo(t)
	r := Repo{dir}

	if got, ok, err := r.CommonDir(ctx); err != nil || !ok || got != filepath.Join(dir, ".git") {
		t.Fatalf("CommonDir = %q, %v, %v", got, ok, err)
	}
	if got, ok, err := r.Toplevel(ctx); err != nil || !ok || got != dir {
		t.Fatalf("Toplevel = %q, %v, %v", got, ok, err)
	}
	if got, ok, err := r.ShortHead(ctx); err != nil || !ok || len(got) < 4 {
		t.Fatalf("ShortHead = %q, %v, %v", got, ok, err)
	}
	if got, ok, err := r.LastCommitDate(ctx, "a.txt"); err != nil || !ok || len(got) != len("2006-01-02") {
		t.Fatalf("LastCommitDate = %q, %v, %v", got, ok, err)
	}
	if _, ok, _ := r.LastCommitDate(ctx, "missing.txt"); ok {
		t.Fatal("LastCommitDate of an untracked path should not be ok")
	}
	mustGit(t, dir, "branch", "spike/x")
	mustGit(t, dir, "branch", "spike/y")
	refs, ok, err := r.ForEachRef(ctx, "refs/heads/spike/")
	if err != nil || !ok || strings.Join(refs, ",") != "spike/x,spike/y" {
		t.Fatalf("ForEachRef = %v, %v, %v", refs, ok, err)
	}
}

func TestCommonDirOfLinkedWorktree(t *testing.T) {
	dir := newRepo(t)
	wt := filepath.Join(t.TempDir(), "wt")
	mustGit(t, dir, "worktree", "add", "-q", "-b", "side", wt)
	got, ok, err := Repo{wt}.CommonDir(context.Background())
	if err != nil || !ok || got != filepath.Join(dir, ".git") {
		t.Fatalf("CommonDir = %q, %v, %v; want %s", got, ok, err, filepath.Join(dir, ".git"))
	}
}

func TestQueriesOutsideRepo(t *testing.T) {
	ctx := context.Background()
	r := Repo{t.TempDir()}
	if _, ok, err := r.CommonDir(ctx); ok || err != nil {
		t.Fatalf("CommonDir ok=%v err=%v", ok, err)
	}
	if _, ok, err := r.ShortHead(ctx); ok || err != nil {
		t.Fatalf("ShortHead ok=%v err=%v", ok, err)
	}
}

func TestIsMergeConflict(t *testing.T) {
	for out, want := range map[string]bool{
		"CONFLICT (content): Merge conflict in work.txt": true,
		"Automatic merge failed; fix conflicts":          true,
		"fatal: unable to auto-detect email address":     false,
		"": false,
	} {
		if got := IsMergeConflict(out); got != want {
			t.Errorf("IsMergeConflict(%q) = %v", out, got)
		}
	}
}
