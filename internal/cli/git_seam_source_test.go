package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every cli git read goes through Deps; only deps.go names the real runner or
// builds a bare Repo.
func TestCliGitReadsStayOnDeps(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "deps.go" {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, bad := range []string{"git.Repo{", "git.Exec"} {
			if strings.Contains(string(b), bad) {
				t.Errorf("%s uses %s; go through Deps", f, bad)
			}
		}
	}
}
