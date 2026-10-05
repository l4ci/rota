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
// exec.CommandContext with a "git" program, or a LookPath("git") probe.
var gitExec = regexp.MustCompile(`exec\.(Command|CommandContext)\(\s*(\w+,\s*)?"git"|LookPath\("git"\)`)

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
			if d.IsDir() && p == filepath.Join(root, "internal", "git") {
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
