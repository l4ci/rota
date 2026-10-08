package gittest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTempDirResolvesSymlinks(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	// t.TempDir lives under TMPDIR; point it at a symlink so the unresolved
	// path differs from the real one on every platform.
	t.Setenv("TMPDIR", link)
	if raw := t.TempDir(); !strings.HasPrefix(raw, link) {
		t.Skipf("t.TempDir ignores TMPDIR here: %s", raw)
	}
	dir := TempDir(t)
	want, err := filepath.EvalSymlinks(dir)
	if err != nil || dir != want {
		t.Errorf("TempDir = %q, want resolved %q (%v)", dir, want, err)
	}
	if strings.HasPrefix(dir, link) {
		t.Errorf("TempDir %q still goes through the symlink %q", dir, link)
	}
}

func TestWrite(t *testing.T) {
	root := t.TempDir()
	Write(t, root, "a/b/c.txt", "hi")
	got, err := os.ReadFile(filepath.Join(root, "a", "b", "c.txt"))
	if err != nil || string(got) != "hi" {
		t.Errorf("read = (%q, %v)", got, err)
	}
	Write(t, root, "a/b/c.txt", "over")
	got, _ = os.ReadFile(filepath.Join(root, "a", "b", "c.txt"))
	if string(got) != "over" {
		t.Errorf("overwrite = %q", got)
	}
}

func TestInitQuietsRepo(t *testing.T) {
	dir := filepath.Join(TempDir(t), "sub", "repo")
	Init(t, dir, "trunk")
	if got := Run(t, dir, "symbolic-ref", "--short", "HEAD"); got != "trunk" {
		t.Errorf("branch = %q, want trunk", got)
	}
	for key, want := range map[string]string{
		"gc.auto": "0", "gc.autoDetach": "false", "maintenance.auto": "false", "receive.autogc": "false",
	} {
		if got := Run(t, dir, "config", "--get", key); got != want {
			t.Errorf("config %s = %q, want %q", key, got, want)
		}
	}
}

func TestNewRepoAndCommit(t *testing.T) {
	dir := NewRepo(t, "main")
	if got := Run(t, dir, "log", "--format=%s"); got != "first" {
		t.Errorf("log = %q, want first", got)
	}
	short := Commit(t, dir, "second", "x/y.txt", "data\n")
	if full := Run(t, dir, "rev-parse", "HEAD"); !strings.HasPrefix(full, short) {
		t.Errorf("short %q is not a prefix of %q", short, full)
	}
	if got := Run(t, dir, "log", "-1", "--format=%s %an <%ae>"); got != "second t <t@t>" {
		t.Errorf("last commit = %q, want identity t <t@t>", got)
	}
	if got := Run(t, dir, "status", "--porcelain"); got != "" {
		t.Errorf("status after Commit = %q, want clean", got)
	}
}

func TestRunnerEnvReplacesDefault(t *testing.T) {
	dir := NewRepo(t, "main")
	r := Runner{Env: append([]string{
		"GIT_AUTHOR_NAME=other", "GIT_AUTHOR_EMAIL=o@o", "GIT_COMMITTER_NAME=other", "GIT_COMMITTER_EMAIL=o@o",
	}, "PATH="+os.Getenv("PATH"), "HOME="+t.TempDir())}
	r.Commit(t, dir, "by other", "f", "1")
	if got := Run(t, dir, "log", "-1", "--format=%an"); got != "other" {
		t.Errorf("author = %q, want other", got)
	}
}

func TestSetIdentity(t *testing.T) {
	keys := []string{"GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL"}
	for _, k := range keys {
		t.Setenv(k, "") // registers restore of the original value
	}
	tests := []struct {
		name  string
		args  []string
		check map[string]string
	}{
		{"default identity", nil, map[string]string{"GIT_AUTHOR_NAME": "t", "GIT_COMMITTER_EMAIL": "t@t"}},
		{"custom pairs", []string{"GIT_AUTHOR_NAME=zed", "GIT_AUTHOR_EMAIL=z@z"}, map[string]string{"GIT_AUTHOR_NAME": "zed", "GIT_AUTHOR_EMAIL": "z@z"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			SetIdentity(tc.args...)
			for k, want := range tc.check {
				if got := os.Getenv(k); got != want {
					t.Errorf("%s = %q, want %q", k, got, want)
				}
			}
		})
	}
}
