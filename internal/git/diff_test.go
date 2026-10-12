package git

import (
	"context"
	"reflect"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
)

func branchWithChanges(t *testing.T) string {
	t.Helper()
	dir := newRepo(t)
	mustGit(t, dir, "checkout", "-q", "-b", "feat")
	writeFile(t, dir, "b.txt", "1\n2\n3\n")
	mustGit(t, dir, "add", "b.txt")
	mustGit(t, dir, "commit", "-q", "-m", "add b")
	return dir
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	gittest.Write(t, dir, name, content)
}

func TestChangedPathsAndLines(t *testing.T) {
	dir := branchWithChanges(t)
	ctx := context.Background()
	repo := Repo{dir}
	paths, err := repo.ChangedPaths(ctx, "main...feat")
	if err != nil || !reflect.DeepEqual(paths, []string{"b.txt"}) {
		t.Fatalf("paths = %v, %v", paths, err)
	}
	n, err := repo.ChangedLines(ctx, "main...feat")
	if err != nil || n != 3 {
		t.Fatalf("lines = %d, %v; want 3", n, err)
	}
	if _, err := repo.ChangedPaths(ctx, "nope...feat"); err == nil {
		t.Fatal("bad span: want error")
	}
	if _, err := repo.ChangedLines(ctx, "nope...feat"); err == nil {
		t.Fatal("bad span: want error")
	}
}

func TestCountLinesSkipsBinary(t *testing.T) {
	if got := CountLines("3\t1\ta.go\n-\t-\timg.png\n\n"); got != 4 {
		t.Fatalf("got %d, want 4", got)
	}
}

func TestRemoteBase(t *testing.T) {
	dir := newRepo(t)
	ctx := context.Background()
	if got := RemoteBase(ctx, Exec, dir, "main"); got != "main" {
		t.Fatalf("no remote: got %q", got)
	}
	if _, ok := RemoteRef(ctx, Exec, dir, ""); ok {
		t.Fatal("empty base resolved")
	}
	mustGit(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
	if got := (Repo{dir}).RemoteBase(ctx, "main"); got != "origin/main" {
		t.Fatalf("with remote: got %q", got)
	}
	if ref, ok := RemoteRef(ctx, Exec, dir, "main"); !ok || ref != "origin/main" {
		t.Fatalf("RemoteRef = %q, %v", ref, ok)
	}
}
