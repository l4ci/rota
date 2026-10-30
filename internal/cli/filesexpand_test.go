package cli

import (
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
)

func expandCtx() *Ctx {
	return &Ctx{Deps: testDeps(), Stdin: strings.NewReader(""), Stdout: io.Discard, Stderr: io.Discard}
}

func TestExpandFilesQuotesAndSkipsDeleted(t *testing.T) {
	root := tierProject(t, `{}`)
	write(t, filepath.Join(root, "gone.txt"), "g")
	gittest.Run(t, root, "add", "gone.txt")
	gittest.Run(t, root, "commit", "-q", "-m", "seed")
	gittest.Run(t, root, "checkout", "-q", "-b", "feat")
	write(t, filepath.Join(root, "it's here.txt"), "x")
	gittest.Run(t, root, "add", "it's here.txt")
	gittest.Run(t, root, "rm", "-q", "gone.txt")
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
	gittest.Run(t, root, "add", ".gitignore")
	gittest.Run(t, root, "commit", "-q", "-m", "ignore")
	gittest.Run(t, root, "checkout", "-q", "-b", "feat")
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
	gittest.Run(t, root, "checkout", "-q", "-b", "feat")
	write(t, filepath.Join(root, "upstream.txt"), "u")
	gittest.Run(t, root, "add", "upstream.txt")
	gittest.Run(t, root, "commit", "-q", "-m", "upstream")
	gittest.Run(t, root, "update-ref", "refs/remotes/origin/main", "HEAD")
	write(t, filepath.Join(root, "branch.txt"), "b")
	gittest.Run(t, root, "add", "branch.txt")
	gittest.Run(t, root, "commit", "-q", "-m", "branch")
	got, err := expandFiles(expandCtx(), root, "", []string{"t {files}"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || strings.Contains(got[0], "upstream.txt") || !strings.Contains(got[0], "branch.txt") {
		t.Errorf("default base: %q, want only the branch changes", got)
	}
	got, err = expandFiles(expandCtx(), root, "main", []string{"t {files}"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !strings.Contains(got[0], "upstream.txt") {
		t.Errorf("explicit base main: %q, want it used as given", got)
	}
}

// A bad base is an error when {files} needs the diff.
func TestExpandFilesBadBaseErrors(t *testing.T) {
	root := tierProject(t, `{}`)
	gittest.Run(t, root, "checkout", "-q", "-b", "feat")
	if got, err := expandFiles(expandCtx(), root, "no-such-ref", []string{"t {files}"}); err == nil {
		t.Errorf("bad base with {files}: got %q, want an error", got)
	}
}

// Several arguments are quoted one by one; a bare {files} argument stays a list.
func TestExpandFilesMultiArgAndNoPlaceholder(t *testing.T) {
	root := tierProject(t, `{}`)
	gittest.Run(t, root, "checkout", "-q", "-b", "feat")
	write(t, filepath.Join(root, "a.txt"), "a")
	got, err := expandFiles(expandCtx(), root, "main", []string{"go", "vet", "{files}"}, []string{"echo hi there"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{`'go' 'vet' '.rota/config.json' 'a.txt'`, "echo hi there"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
	// Without {files}, git is never consulted, so a bad base is no error.
	if got, err := expandFiles(expandCtx(), root, "no-such-ref", []string{"echo hi"}); err != nil || got[0] != "echo hi" {
		t.Errorf("without {files}: %q, %v", got, err)
	}
}
