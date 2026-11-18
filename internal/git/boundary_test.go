package git

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// gitExec matches code that starts the git binary itself: exec.Command or
// exec.CommandContext with a "git" program, a LookPath("git") probe, a
// proc.Cmd naming git, or a process runner (proc.Run, a Deps.Run or an Exec
// seam) handed a "git" program literal. Each skips Run's timeout, C locale and
// ErrNoGit mapping.
var gitExec = regexp.MustCompile(`exec\.(Command|CommandContext)\(\s*(\w+,\s*)?"git"|LookPath\("git"\)|Cmd\{\s*Name:\s*"git"|\b(Run|Exec|exec)(\(\))?\([^()\n]*"git"`)

// TestGitExecPattern pins what the boundary pattern catches and what it lets
// by, so a loosened regex cannot quietly stop seeing a bypass.
func TestGitExecPattern(t *testing.T) {
	for src, want := range map[string]bool{
		`exec.Command("git", "status")`:                       true,
		`exec.LookPath("git")`:                                true,
		`proc.Run(ctx, proc.Cmd{Name: "git", Args: a})`:       true,
		`o.d.Run(ctx, "git", []string{"-C", cwd})`:            true,
		`c.exec()(ctx, c.Dir, "git", args, nil)`:              true,
		`run(ctx, dir, "git", args)`:                          false,
		`d.in.Look("git")`:                                    false,
		`fail("git", "git not found on PATH", "install git")`: false,
		`r.Run(ctx, "rev-parse", "--verify", ref)`:            false,
		`errors.New("git")`:                                   false,
	} {
		if got := gitExec.MatchString(src); got != want {
			t.Errorf("%s: match = %v, want %v", src, got, want)
		}
	}
}

// TestNoDirectGitExec holds the line that internal/git is the one place git
// is run: a timeout, a context and ErrNoGit come with it.
func TestNoDirectGitExec(t *testing.T) {
	root := filepath.Join("..", "..")
	var bad []string
	for _, top := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			// internal/gittest is the test fixture package: tests build repos
			// with the plain git binary, with no timeout or context to add.
			if d.IsDir() && (p == filepath.Join(root, "internal", "git") || p == filepath.Join(root, "internal", "gittest")) {
				return filepath.SkipDir
			}
			if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			if gitExec.Match(b) {
				bad = append(bad, filepath.ToSlash(strings.TrimPrefix(p, root+string(filepath.Separator))))
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(bad) > 0 {
		t.Fatalf("run git through internal/git, not exec directly: %s", strings.Join(bad, ", "))
	}
}

// TestNoRotatreeImport keeps internal/git below rotatree: the .rota/ root
// walk lives in rotatree, and git does not know the state layout.
func TestNoRotatreeImport(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), `"github.com/l4ci/rota/internal/rotatree"`) {
			t.Errorf("%s imports internal/rotatree", f)
		}
	}
}
