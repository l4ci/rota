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

// A process of the current user whose table entries fail to read for a reason
// other than permission leaves the scan incomplete: an error, never a process
// quietly treated as gone.
func TestReadProcUsesOtherReadErrorFails(t *testing.T) {
	root := fakeProc(t, map[string]map[string]string{"100": {"cwd": "/w/a"}}, nil)
	fd := filepath.Join(root, "100", "fd")
	if err := os.Remove(fd); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fd, nil, 0o600); err != nil { // ReadDir on a file: ENOTDIR
		t.Fatal(err)
	}
	if uses, err := ReadProcUses(root, os.Getuid()); err == nil {
		t.Fatalf("fd table that is no directory read as %v, want an error", uses)
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

// A file a process mapped and then closed shows only in its maps: it is still
// in use, so its path counts.
func TestReadProcUsesCountsFileMappings(t *testing.T) {
	root := fakeProc(t, map[string]map[string]string{"100": {"cwd": "/w/a"}}, nil)
	maps := "7f00-7f01 r--p 00000000 08:01 1234       /w/scratch/lib name.so\n" +
		"7f01-7f02 rw-p 00000000 00:00 0          [heap]\n" +
		"7f02-7f03 r--p 00000000 08:01 99         /w/gone.dat (deleted)\n" +
		"7f03-7f04 rw-p 00000000 00:00 0\n"
	if err := os.WriteFile(filepath.Join(root, "100", "maps"), []byte(maps), 0o600); err != nil {
		t.Fatal(err)
	}
	uses, err := ReadProcUses(root, os.Getuid())
	if err != nil || len(uses) != 1 {
		t.Fatalf("ReadProcUses = %v, %v", uses, err)
	}
	for _, want := range []string{"/w/scratch/lib name.so", "/w/gone.dat (deleted)"} {
		if !slices.Contains(uses[0].Paths, want) {
			t.Errorf("paths %v lack the mapping %s", uses[0].Paths, want)
		}
	}
	if slices.Contains(uses[0].Paths, "[heap]") {
		t.Errorf("paths %v hold an anonymous mapping", uses[0].Paths)
	}
}

// A maps entry that fails to read for a reason other than permission leaves
// the scan incomplete.
func TestReadProcUsesOtherMapsErrorFails(t *testing.T) {
	root := fakeProc(t, map[string]map[string]string{"100": {"cwd": "/w/a"}}, nil)
	if err := os.Mkdir(filepath.Join(root, "100", "maps"), 0o755); err != nil { // ReadFile on a dir: EISDIR
		t.Fatal(err)
	}
	if uses, err := ReadProcUses(root, os.Getuid()); err == nil {
		t.Fatalf("maps that is no file read as %v, want an error", uses)
	}
}

// A process of the user that holds capabilities (the systemd user manager)
// keeps its entries as the user's yet denies the read of cwd, fd, maps and
// environ. Per the maintainer decision on #3306 it is skipped like a
// non-dumpable one: it does not hold every entry, and a process that reads fine
// is still counted.
func TestReadProcUsesSkipsPermissionDeniedOwnProcess(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads every mode")
	}
	root := fakeProc(t, map[string]map[string]string{"100": {"cwd": "/w/a"}, "300": {"cwd": "/w/systemd"}}, nil)
	dir := filepath.Join(root, "300")
	if err := os.Chmod(dir, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	uid := os.Getuid()
	owner := func(string) (int, error) { return uid, nil } // lstat of cwd still shows uid
	uses, err := readProcUses(root, uid, owner)
	if err != nil {
		t.Fatalf("a permission-denied own process failed the scan: %v", err)
	}
	if len(uses) != 1 || uses[0].PID != 100 || !slices.Contains(uses[0].Paths, "/w/a") {
		t.Fatalf("uses = %v, want only pid 100 holding /w/a", uses)
	}
}

// Each unreadable entry of an own process is a permission skip, not only cwd.
func TestReadProcUsesSkipsPermissionDeniedEntries(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads every mode")
	}
	for _, name := range []string{"fd", "maps", "environ"} {
		root := fakeProc(t, map[string]map[string]string{"100": {"cwd": "/w/a"}}, map[string]string{"100": "HOME=/w\x00"})
		if err := os.WriteFile(filepath.Join(root, "100", "maps"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(root, "100", name)
		if err := os.Chmod(p, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(p, 0o755) })
		uses, err := ReadProcUses(root, os.Getuid())
		if err != nil || len(uses) != 0 {
			t.Errorf("%s denied: ReadProcUses = %v, %v; want skipped without error", name, uses, err)
		}
	}
}

// A non-dumpable process of the user (gpg-agent, an sshd session) keeps
// /proc/<pid> as the user's, but its cwd, fd and maps entries show as root's
// and cannot be read. Per the maintainer decision on #2499 it is skipped, not
// a reason to fail the scan; the fake owner stands in for those root-owned
// entries, which a test cannot create.
func TestReadProcUsesSkipsNonDumpableProcesses(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads every mode")
	}
	root := fakeProc(t, map[string]map[string]string{"100": {"cwd": "/w/a"}, "300": {"cwd": "/w/agent"}}, nil)
	fd := filepath.Join(root, "300", "fd")
	if err := os.Chmod(fd, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(fd, 0o755) })
	uid := os.Getuid()
	owner := func(path string) (int, error) {
		if path == filepath.Join(root, "300", "cwd") {
			return 0, nil
		}
		return uid, nil
	}
	uses, err := readProcUses(root, uid, owner)
	if err != nil {
		t.Fatalf("a non-dumpable process failed the scan: %v", err)
	}
	if len(uses) != 1 || uses[0].PID != 100 {
		t.Fatalf("uses = %v, want only pid 100", uses)
	}
}
