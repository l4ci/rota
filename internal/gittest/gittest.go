// Package gittest is the shared git fixture for tests: run git in a temp repo
// with a fixed identity, fail the test on error, and build small repos. It
// replaces the per-package copies that had drifted apart (identity, default
// branch, symlink resolution).
package gittest

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Identity is the default author and committer of a fixture commit, so a test
// never depends on the developer's (or CI's missing) global git config. A
// GIT_AUTHOR_* or GIT_COMMITTER_* variable already in the test process wins,
// for a suite whose golden files pin commit hashes under another identity.
var Identity = []string{"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t"}

// Runner runs git with a fixed environment. The zero value is Identity under
// the test process's environment; a non-nil Env replaces both, for a
// harness that pins dates, HOME and PATH itself.
type Runner struct {
	Env []string
}

// Run runs git in dir and returns its trimmed combined output, failing t on a
// non-zero exit.
func (r Runner) Run(t testing.TB, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "init.defaultBranch=main"}, args...)...)
	cmd.Dir = dir
	if r.Env != nil {
		cmd.Env = r.Env
	} else {
		cmd.Env = append(slices.Clone(Identity), os.Environ()...)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// Run runs git in dir with the default environment (see Runner).
func Run(t testing.TB, dir string, args ...string) string {
	t.Helper()
	return Runner{}.Run(t, dir, args...)
}

// TempDir is t.TempDir with symlinks resolved, so a path compares equal to what
// git or the code under test reports (macOS /var vs /private/var).
func TempDir(t testing.TB) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// Init creates dir if needed and runs `git init` there on branch, with
// background housekeeping off (see Quiet).
func Init(t testing.TB, dir, branch string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	Run(t, dir, "init", "-q", "-b", branch)
	Quiet(t, dir)
}

// Quiet turns off the housekeeping git detaches after a commit or push
// (gc --auto, maintenance --auto, receive's auto gc). That child outlives the
// git call and can still be writing into .git when t.TempDir cleanup runs
// ("unlinkat .git: directory not empty"). It is repo config, so it also covers
// the git calls the code under test makes, and every worktree of the repo. Call
// it on a bare repo made outside Init too.
func Quiet(t testing.TB, dir string) {
	t.Helper()
	Run(t, dir, "config", "gc.auto", "0")
	Run(t, dir, "config", "gc.autoDetach", "false")
	Run(t, dir, "config", "maintenance.auto", "false")
	Run(t, dir, "config", "receive.autogc", "false")
}

// Write writes content to rel under root, creating parent directories.
func Write(t testing.TB, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Commit writes rel with content, stages just that path, commits it with
// subject and returns the short hash.
func (r Runner) Commit(t testing.TB, dir, subject, rel, content string) string {
	t.Helper()
	Write(t, dir, rel, content)
	r.Run(t, dir, "add", rel)
	r.Run(t, dir, "commit", "-q", "-m", subject)
	return r.Run(t, dir, "rev-parse", "--short", "HEAD")
}

// Commit is Runner.Commit with the default environment.
func Commit(t testing.TB, dir, subject, rel, content string) string {
	t.Helper()
	return Runner{}.Commit(t, dir, subject, rel, content)
}

// NewRepo is a symlink-resolved temp repo on branch with one commit.
func NewRepo(t testing.TB, branch string) string {
	t.Helper()
	dir := TempDir(t)
	Init(t, dir, branch)
	Commit(t, dir, "first", "seed", "seed\n")
	return dir
}

// SetIdentity puts Identity (or ident, when given as KEY=value pairs) in the
// test process's environment, for a TestMain whose code under test commits or
// merges itself (the gate, ship).
func SetIdentity(ident ...string) {
	if len(ident) == 0 {
		ident = Identity
	}
	for _, kv := range ident {
		k, v, _ := strings.Cut(kv, "=")
		os.Setenv(k, v)
	}
}
