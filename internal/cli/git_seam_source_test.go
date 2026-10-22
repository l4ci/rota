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

// The working directory and home come from Deps.Getwd and Deps.UserHomeDir;
// only deps.go names the process reads.
func TestCliCwdAndHomeReadsStayOnDeps(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "deps.go" {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			for _, bad := range []string{"os.Getwd(", "os.UserHomeDir("} {
				if strings.Contains(line, bad) {
					t.Errorf("%s uses %s; go through Deps", f, bad)
				}
			}
		}
	}
}
