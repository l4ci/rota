package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cliSources lists the package's non-test sources and fails on an empty or
// errored glob, so a wrong working directory cannot pass a guard vacuously.
func cliSources(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no .go files in the working directory")
	}
	return files
}

// Every cli git read goes through Deps; only deps.go names the real runner or
// builds a bare Repo.
func TestCliGitReadsStayOnDeps(t *testing.T) {
	files := cliSources(t)
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
			for _, bad := range []string{"git.Repo{", "git.Exec", "rotastate.CommonDir("} {
				if strings.Contains(line, bad) {
					t.Errorf("%s uses %s; go through Deps", f, bad)
				}
			}
		}
	}
}

// The working directory and home come from Deps.Getwd and Deps.UserHomeDir;
// only deps.go names the process reads.
func TestCliCwdAndHomeReadsStayOnDeps(t *testing.T) {
	files := cliSources(t)
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
			for _, bad := range []string{"os.Getwd", "os.UserHomeDir"} {
				if strings.Contains(line, bad) {
					t.Errorf("%s uses %s; go through Deps", f, bad)
				}
			}
		}
	}
}
