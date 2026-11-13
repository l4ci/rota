package reap

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// fakeProc builds a /proc-shaped tree: pid -> link name -> target, plus an
// environ file per pid.
func fakeProc(t *testing.T, pids map[string]map[string]string, environ map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for pid, links := range pids {
		dir := filepath.Join(root, pid)
		if err := os.MkdirAll(filepath.Join(dir, "fd"), 0o755); err != nil {
			t.Fatal(err)
		}
		for name, target := range links {
			if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
				t.Fatal(err)
			}
		}
		if env, ok := environ[pid]; ok {
			if err := os.WriteFile(filepath.Join(dir, "environ"), []byte(env), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "self"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestReadProcUsesCollectsCwdExeFilesAndEnvDirs(t *testing.T) {
	root := fakeProc(t, map[string]map[string]string{
		"100": {"cwd": "/w/a", "exe": "/w/bin/rota", "fd/0": "/w/in.txt", "fd/1": "pipe:[12]", "fd/2": "/w/out (deleted)"},
		"200": {}, // a zombie: no links, no environ
	}, map[string]string{"100": "PATH=/usr/bin\x00HOME=/w/home\x00TMPDIR=/w/tmp\x00"})
	uses, err := ReadProcUses(root, os.Getuid())
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, u := range uses {
		if u.PID == 100 {
			got = u.Paths
		}
	}
	for _, want := range []string{"/w/a", "/w/bin/rota", "/w/in.txt", "/w/out (deleted)", "/w/home", "/w/tmp"} {
		if !slices.Contains(got, want) {
			t.Errorf("pid 100 paths %v lack %s", got, want)
		}
	}
	for _, bad := range []string{"pipe:[12]", "/usr/bin"} {
		if slices.Contains(got, bad) {
			t.Errorf("pid 100 paths %v hold %s, which is no path in use", got, bad)
		}
	}
}

// A process of the current user whose table entries cannot be read leaves the
// scan incomplete: an error, never a process quietly treated as gone.
func TestReadProcUsesUnreadableOwnProcessFails(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads every mode")
	}
	root := fakeProc(t, map[string]map[string]string{"100": {"cwd": "/w/a"}}, nil)
	fd := filepath.Join(root, "100", "fd")
	if err := os.Chmod(fd, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(fd, 0o755) })
	if uses, err := ReadProcUses(root, os.Getuid()); err == nil {
		t.Fatalf("unreadable fd table read as %v, want an error", uses)
	}
}

// Processes of other users cannot enter the 0700 temp root, so they are not
// consulted (their entries are unreadable to us by design).
func TestReadProcUsesSkipsOtherUsers(t *testing.T) {
	root := fakeProc(t, map[string]map[string]string{"100": {"cwd": "/w/a"}}, nil)
	uses, err := ReadProcUses(root, os.Getuid()+1)
	if err != nil || len(uses) != 0 {
		t.Fatalf("ReadProcUses for another uid = %v, %v; want none", uses, err)
	}
}

func TestReadProcUsesNoTableFails(t *testing.T) {
	if _, err := ReadProcUses(filepath.Join(t.TempDir(), "absent"), os.Getuid()); err == nil {
		t.Fatal("a missing process table must be an error")
	}
}
