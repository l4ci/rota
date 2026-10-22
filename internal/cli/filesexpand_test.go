package cli

import (
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func expandCtx() *Ctx {
	return &Ctx{Deps: testDeps(), Stdin: strings.NewReader(""), Stdout: io.Discard, Stderr: io.Discard}
}

func TestExpandFilesQuotesAndSkipsDeleted(t *testing.T) {
	root := tierProject(t, `{}`)
	write(t, filepath.Join(root, "gone.txt"), "g")
	gitT(t, root, "add", "gone.txt")
	gitT(t, root, "commit", "-q", "-m", "seed")
	gitT(t, root, "checkout", "-q", "-b", "feat")
	write(t, filepath.Join(root, "it's here.txt"), "x")
	gitT(t, root, "add", "it's here.txt")
	gitT(t, root, "rm", "-q", "gone.txt")
	got, err := expandFiles(expandCtx(), root, "main", []string{"lint {files}"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{`lint 'it'\''s here.txt' '.rota/config.json'`}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestExpandFilesIncludesUntrackedNotIgnored(t *testing.T) {
	root := tierProject(t, `{}`)
	write(t, filepath.Join(root, ".gitignore"), "*.log\n")
	gitT(t, root, "add", ".gitignore")
	gitT(t, root, "commit", "-q", "-m", "ignore")
	gitT(t, root, "checkout", "-q", "-b", "feat")
	write(t, filepath.Join(root, "new one.txt"), "x")
	write(t, filepath.Join(root, "noise.log"), "x")
	got, err := expandFiles(expandCtx(), root, "main", []string{"t {files}"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{`t '.rota/config.json' 'new one.txt'`}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want untracked minus ignored %q", got, want)
	}
}

// A local main that lags origin/main must not pull the upstream commits in:
// the default base is the remote-tracking ref when it is ahead.
func TestExpandFilesPrefersAheadRemoteBase(t *testing.T) {
	root := tierProject(t, `{}`)
	gitT(t, root, "checkout", "-q", "-b", "feat")
	write(t, filepath.Join(root, "upstream.txt"), "u")
	gitT(t, root, "add", "upstream.txt")
	gitT(t, root, "commit", "-q", "-m", "upstream")
	gitT(t, root, "update-ref", "refs/remotes/origin/main", "HEAD")
	write(t, filepath.Join(root, "branch.txt"), "b")
	gitT(t, root, "add", "branch.txt")
	gitT(t, root, "commit", "-q", "-m", "branch")
	got, _ := expandFiles(expandCtx(), root, "", []string{"t {files}"})
	if len(got) != 1 || strings.Contains(got[0], "upstream.txt") || !strings.Contains(got[0], "branch.txt") {
		t.Errorf("default base: %q, want only the branch changes", got)
	}
	got, _ = expandFiles(expandCtx(), root, "main", []string{"t {files}"})
	if len(got) != 1 || !strings.Contains(got[0], "upstream.txt") {
		t.Errorf("explicit base main: %q, want it used as given", got)
	}
}

// Several arguments are quoted one by one; a bare {files} argument stays a list.
func TestExpandFilesMultiArgAndNoPlaceholder(t *testing.T) {
	root := tierProject(t, `{}`)
	gitT(t, root, "checkout", "-q", "-b", "feat")
	write(t, filepath.Join(root, "a.txt"), "a")
	got, err := expandFiles(expandCtx(), root, "main", []string{"go", "vet", "{files}"}, []string{"echo hi there"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{`'go' 'vet' '.rota/config.json' 'a.txt'`, "echo hi there"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
	// No placeholder: git is never consulted, so a bad base is no error.
	if got, err := expandFiles(expandCtx(), root, "no-such-ref", []string{"echo hi"}); err != nil || got[0] != "echo hi" {
		t.Errorf("no placeholder: %q, %v", got, err)
	}
}
