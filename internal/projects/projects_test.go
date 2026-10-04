package projects

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func isolate(t *testing.T) string {
	t.Helper()
	x := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", x)
	return x
}

func TestDirHonoursXDGAndFallsBackToHome(t *testing.T) {
	x := isolate(t)
	if d, _ := Dir(); d != filepath.Join(x, "rota") {
		t.Errorf("xdg: %s", d)
	}
	t.Setenv("XDG_CONFIG_HOME", "relative/ignored")
	h := t.TempDir()
	t.Setenv("HOME", h)
	if d, _ := Dir(); d != filepath.Join(h, ".config", "rota") {
		t.Errorf("home: %s", d)
	}
}

func TestRegisterIsIdempotentAndRefreshesLastSeen(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	now = func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }
	t.Cleanup(func() { now = func() time.Time { return time.Now().UTC() } })
	if added, err := Register(proj); err != nil || !added {
		t.Fatalf("first: %v %v", added, err)
	}
	now = func() time.Time { return time.Date(2026, 2, 2, 0, 0, 0, 0, time.UTC) }
	if added, err := Register(proj); err != nil || added {
		t.Fatalf("second: %v %v", added, err)
	}
	ps, _ := List()
	if len(ps) != 1 || ps[0].LastSeen != "2026-02-02T00:00:00Z" || ps[0].Name != filepath.Base(proj) || ps[0].Missing {
		t.Fatalf("%+v", ps)
	}
}

func TestRegisterResolvesSymlinks(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(proj, link); err != nil {
		t.Skip(err)
	}
	Register(proj)
	Register(link)
	if ps, _ := List(); len(ps) != 1 {
		t.Fatalf("%+v", ps)
	}
}

func TestListFlagsMissingPathsAndKeepsThem(t *testing.T) {
	isolate(t)
	gone := filepath.Join(t.TempDir(), "gone")
	os.Mkdir(gone, 0o755)
	live := t.TempDir()
	Register(gone)
	Register(live)
	os.Remove(gone)
	ps, _ := List()
	if len(ps) != 2 {
		t.Fatalf("%+v", ps)
	}
	for _, p := range ps {
		if p.Missing != (p.Name == "gone") {
			t.Errorf("%+v", p)
		}
	}
}

func TestListEmptyAndCorruptRegistry(t *testing.T) {
	x := isolate(t)
	if ps, err := List(); err != nil || len(ps) != 0 {
		t.Fatalf("%v %v", ps, err)
	}
	os.MkdirAll(filepath.Join(x, "rota"), 0o755)
	os.WriteFile(filepath.Join(x, "rota", "projects.json"), []byte("{nope"), 0o644)
	if ps, err := List(); err != nil || len(ps) != 0 {
		t.Fatalf("%v %v", ps, err)
	}
	if added, err := Register(t.TempDir()); err != nil || !added {
		t.Fatalf("register over corrupt: %v %v", added, err)
	}
}
