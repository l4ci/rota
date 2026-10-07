package stalebin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/git"
)

// fakeGit answers by the first git args, so a test names only what it needs.
func fakeGit(top string, answers map[string]git.Result) git.Runner {
	return func(_ context.Context, _ string, args ...string) (git.Result, error) {
		if args[0] == "rev-parse" && len(args) > 1 && args[1] == "--show-toplevel" {
			return git.Result{Stdout: top + "\n"}, nil
		}
		if r, ok := answers[strings.Join(args, " ")]; ok {
			return r, nil
		}
		return git.Result{ExitCode: 128}, nil
	}
}

func checkout(t *testing.T, module string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module "+module+"\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

const head = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func stale(top string) git.Runner {
	return fakeGit(top, map[string]git.Result{
		"rev-parse HEAD":                                {Stdout: head + "\n"},
		"rev-parse --verify -q aaaaaaa^{commit}":        {Stdout: "aaaaaaa0\n"},
		"rev-list --count aaaaaaa..HEAD":                {Stdout: "3\n"},
		"diff --name-only aaaaaaa HEAD -- cmd internal": {Stdout: "internal/cli/gate.go\ninternal/cli/gate_test.go\n"},
	})
}

func TestStaleBinaryWarns(t *testing.T) {
	top := checkout(t, "github.com/l4ci/rota")
	f, ok := Check(context.Background(), stale(top), top, "aaaaaaa")
	if !ok {
		t.Fatal("want a finding for a binary behind HEAD with changed Go files")
	}
	if f.Behind != 3 || f.Commit != "aaaaaaa" || f.Head != head[:7] {
		t.Errorf("finding = %+v", f)
	}
	for _, want := range []string{"3 commits", "aaaaaaa", head[:7]} {
		if !strings.Contains(f.Detail(), want) {
			t.Errorf("detail %q lacks %q", f.Detail(), want)
		}
	}
	// AC-4: the rebuild keeps Version equal to VERSION.
	if !strings.Contains(f.Rebuild, "internal/version.Version=$(cat VERSION)") || !strings.Contains(f.Rebuild, "./cmd/rota") {
		t.Errorf("rebuild = %q", f.Rebuild)
	}
}

func TestOutsideRotaNothing(t *testing.T) {
	top := checkout(t, "example.com/other")
	if _, ok := Check(context.Background(), stale(top), top, "aaaaaaa"); ok {
		t.Error("another module's checkout must not warn")
	}
	// Not a git checkout at all.
	none := func(context.Context, string, ...string) (git.Result, error) { return git.Result{ExitCode: 128}, nil }
	if _, ok := Check(context.Background(), none, t.TempDir(), "aaaaaaa"); ok {
		t.Error("a directory outside git must not warn")
	}
}

func TestNoWarningWhenCurrentOrNoGoChange(t *testing.T) {
	top := checkout(t, "github.com/l4ci/rota")
	if _, ok := Check(context.Background(), stale(top), top, head[:7]); ok {
		t.Error("binary at HEAD must not warn")
	}
	if _, ok := Check(context.Background(), stale(top), top, ""); ok {
		t.Error("a binary with no commit stamp cannot be compared")
	}
	docsOnly := fakeGit(top, map[string]git.Result{
		"rev-parse HEAD":                                {Stdout: head + "\n"},
		"rev-parse --verify -q aaaaaaa^{commit}":        {Stdout: "aaaaaaa0\n"},
		"rev-list --count aaaaaaa..HEAD":                {Stdout: "2\n"},
		"diff --name-only aaaaaaa HEAD -- cmd internal": {Stdout: "internal/cli/README.md\ninternal/cli/gate_test.go\n"},
	})
	if _, ok := Check(context.Background(), docsOnly, top, "aaaaaaa"); ok {
		t.Error("only docs and test files changed: the binary is not behind")
	}
	unknown := fakeGit(top, map[string]git.Result{"rev-parse HEAD": {Stdout: head + "\n"}})
	if _, ok := Check(context.Background(), unknown, top, "aaaaaaa"); ok {
		t.Error("a commit this checkout does not know cannot be compared")
	}
}
