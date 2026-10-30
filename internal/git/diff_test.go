package git

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"strings"
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
	repo := Repo{Dir: dir}
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
	if got := (Repo{Dir: dir}).RemoteBase(ctx, "main"); got != "origin/main" {
		t.Fatalf("with remote: got %q", got)
	}
	if ref, ok := RemoteRef(ctx, Exec, dir, "main"); !ok || ref != "origin/main" {
		t.Fatalf("RemoteRef = %q, %v", ref, ok)
	}
}

func TestExitErrorCarriesStderr(t *testing.T) {
	dir := newRepo(t)
	_, err := Repo{Dir: dir}.ChangedPaths(context.Background(), "nope...main")
	var ee *ExitError
	if !errors.As(err, &ee) || ee.ExitCode == 0 || !strings.Contains(ee.Stderr, "nope") {
		t.Fatalf("err = %#v, want ExitError with git's stderr", err)
	}
	_, err = Repo{Dir: dir}.ChangedLines(context.Background(), "nope...main")
	if !errors.As(err, &ee) || ee.Stderr == "" {
		t.Fatalf("lines err = %#v", err)
	}
}

func TestChangedPathsRenameBinaryEmpty(t *testing.T) {
	dir := newRepo(t)
	ctx := context.Background()
	repo := Repo{Dir: dir}
	if p, err := repo.ChangedPaths(ctx, "main...main"); err != nil || len(p) != 0 {
		t.Fatalf("empty diff: %v, %v", p, err)
	}
	if n, err := repo.ChangedLines(ctx, "main...main"); err != nil || n != 0 {
		t.Fatalf("empty lines: %d, %v", n, err)
	}
	mustGit(t, dir, "checkout", "-q", "-b", "feat")
	mustGit(t, dir, "mv", "a.txt", "renamed.txt")
	gittest.Write(t, dir, "img.bin", "a\x00b\x00\x01")
	mustGit(t, dir, "add", "-A")
	mustGit(t, dir, "commit", "-q", "-m", "rename and binary")
	paths, err := repo.ChangedPaths(ctx, "main...feat")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(paths)
	if want := []string{"img.bin", "renamed.txt"}; !reflect.DeepEqual(paths, want) {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
	// the rename is a pure move (0 lines); the binary file counts for nothing
	if n, err := repo.ChangedLines(ctx, "main...feat"); err != nil || n != 0 {
		t.Fatalf("lines = %d, %v; want 0", n, err)
	}
}

func TestRemoteBaseSpellings(t *testing.T) {
	dir := newRepo(t)
	ctx := context.Background()
	mustGit(t, dir, "update-ref", "refs/remotes/origin/release/1.x", "HEAD")
	mustGit(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
	if got := RemoteBase(ctx, Exec, dir, "release/1.x"); got != "origin/release/1.x" {
		t.Fatalf("slash base: got %q", got)
	}
	// a base already spelled origin/x asks for origin/origin/x, which is absent,
	// so it comes back unchanged
	if got := RemoteBase(ctx, Exec, dir, "origin/main"); got != "origin/main" {
		t.Fatalf("origin-spelled base: got %q", got)
	}
}

// A span that starts with a dash is a revision to git, never an option
// (`rota change --base` is user-supplied).
func TestDiffSpanIsNeverAnOption(t *testing.T) {
	var seen [][]string
	run := func(_ context.Context, _ string, args ...string) (Result, error) {
		seen = append(seen, args)
		return Result{}, nil
	}
	span := "--output=/tmp/pwned"
	ChangedPaths(context.Background(), run, ".", span)
	DiffFiles(context.Background(), run, ".", span)
	ChangedLines(context.Background(), run, ".", span)
	ChangedLinesCounted(context.Background(), run, ".", span)
	if len(seen) != 4 {
		t.Fatalf("ran %d git calls", len(seen))
	}
	for _, args := range seen {
		for i, a := range args {
			if a == span && (i == 0 || args[i-1] != "--end-of-options") {
				t.Errorf("git %v: span not guarded by --end-of-options", args)
			}
		}
	}
}
