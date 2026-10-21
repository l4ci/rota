package projects

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/repos"
)

func isolate(t *testing.T) string {
	t.Helper()
	x := t.TempDir()
	t.Setenv("ROTA_CONFIG_DIR", "")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(x, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(x, "state"))
	return x
}

func TestDirHonoursXDGAndFallsBackToHome(t *testing.T) {
	x := isolate(t)
	if d, _ := Dir(); d != filepath.Join(x, "config", "rota") {
		t.Errorf("xdg: %s", d)
	}
	t.Setenv("XDG_CONFIG_HOME", "relative/ignored")
	h := t.TempDir()
	t.Setenv("HOME", h)
	if d, _ := Dir(); d != filepath.Join(h, ".config", "rota") {
		t.Errorf("home: %s", d)
	}
}

func TestResolveOverrideBeatsXDGAndIgnoresRelative(t *testing.T) {
	x := isolate(t)
	o := t.TempDir()
	t.Setenv("ROTA_CONFIG_DIR", o)
	if d, src, w, _ := Resolve(); d != o || src != SourceOverride || w != "" {
		t.Errorf("override: %s %s %q", d, src, w)
	}
	t.Setenv("ROTA_CONFIG_DIR", "rel/dir")
	d, src, w, _ := Resolve()
	if d != filepath.Join(x, "config", "rota") || src != SourceXDG || w == "" {
		t.Errorf("relative: %s %s %q", d, src, w)
	}
	t.Setenv("ROTA_CONFIG_DIR", "")
	if _, src, w, _ := Resolve(); src != SourceXDG || w != "" {
		t.Errorf("unset: %s %q", src, w)
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", t.TempDir())
	if _, src, _, _ := Resolve(); src != SourceDefault {
		t.Errorf("default: %s", src)
	}
}

func TestRegisterIsIdempotentAndRefreshesLastSeen(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	if added, err := Register(proj, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)); err != nil || !added {
		t.Fatalf("first: %v %v", added, err)
	}
	if added, err := Register(proj, time.Date(2026, 2, 2, 0, 0, 0, 0, time.UTC)); err != nil || added {
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
	Register(proj, time.Now())
	Register(link, time.Now())
	if ps, _ := List(); len(ps) != 1 {
		t.Fatalf("%+v", ps)
	}
}

func TestListFlagsMissingPathsAndKeepsThem(t *testing.T) {
	isolate(t)
	gone := filepath.Join(t.TempDir(), "gone")
	os.Mkdir(gone, 0o755)
	live := t.TempDir()
	Register(gone, time.Now())
	Register(live, time.Now())
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
	os.MkdirAll(filepath.Join(x, "state", "rota"), 0o755)
	os.WriteFile(filepath.Join(x, "state", "rota", "projects.json"), []byte("{nope"), 0o644)
	if ps, err := List(); err != nil || len(ps) != 0 {
		t.Fatalf("%v %v", ps, err)
	}
	if added, err := Register(t.TempDir(), time.Now()); err != nil || !added {
		t.Fatalf("register over corrupt: %v %v", added, err)
	}
}

func TestRemoveDropsOneEntryAndKeepsTheDirectory(t *testing.T) {
	isolate(t)
	a, b := t.TempDir(), t.TempDir()
	Register(a, time.Now())
	Register(b, time.Now())
	link := filepath.Join(t.TempDir(), "link")
	os.Symlink(a, link)
	got, err := Remove(link)
	if err != nil || len(got) != 1 || got[0].Path != repos.Realpath(a) {
		t.Fatalf("remove via symlink: %+v %v", got, err)
	}
	if _, err := os.Stat(a); err != nil {
		t.Errorf("the directory must stay: %v", err)
	}
	if ps, _ := List(); len(ps) != 1 || ps[0].Path != repos.Realpath(b) {
		t.Errorf("other entry must stay: %+v", ps)
	}
	if got, _ := Remove(a); len(got) != 0 {
		t.Errorf("second remove should match nothing: %+v", got)
	}
}

func TestRemoveFindsAGoneDirectoryAndSkipsAnAbsentRegistry(t *testing.T) {
	x := isolate(t)
	if got, err := Remove("/nope"); err != nil || got != nil {
		t.Fatalf("absent registry: %+v %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(x, "state", "rota", "projects.json")); err == nil {
		t.Error("an absent registry must not be created")
	}
	gone := filepath.Join(t.TempDir(), "gone")
	os.Mkdir(gone, 0o755)
	Register(gone, time.Now())
	os.Remove(gone)
	got, err := Remove(gone)
	if err != nil || len(got) != 1 || !got[0].Missing {
		t.Fatalf("gone dir: %+v %v", got, err)
	}
}

func TestDataDir(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/x/data")
	if d, _ := DataDir(); d != "/x/data/rota" {
		t.Errorf("%q", d)
	}
	t.Setenv("XDG_DATA_HOME", "relative")
	t.Setenv("HOME", "/h")
	if d, _ := DataDir(); d != "/h/.local/share/rota" {
		t.Errorf("%q", d)
	}
}

func TestStateDirHonoursXDGAndFallsBackToHome(t *testing.T) {
	x := isolate(t)
	if d, _ := StateDir(); d != filepath.Join(x, "state", "rota") {
		t.Errorf("xdg: %s", d)
	}
	t.Setenv("XDG_STATE_HOME", "relative/ignored")
	h := t.TempDir()
	t.Setenv("HOME", h)
	if d, _ := StateDir(); d != filepath.Join(h, ".local", "state", "rota") {
		t.Errorf("home: %s", d)
	}
}

func oldRegistry(t *testing.T, x, body string) string {
	t.Helper()
	dir := filepath.Join(x, "config", "rota")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(dir, "projects.json")
	if err := os.WriteFile(old, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(old+".lock", nil, 0o644)
	return old
}

func TestListMigratesTheConfigDirRegistry(t *testing.T) {
	x := isolate(t)
	proj := t.TempDir()
	old := oldRegistry(t, x, `{"projects":[{"path":"`+proj+`","name":"p","lastSeen":"2026-01-01T00:00:00Z"}]}`)
	ps, err := List()
	if err != nil || len(ps) != 1 || ps[0].Path != proj {
		t.Fatalf("%+v %v", ps, err)
	}
	for _, f := range []string{old, old + ".lock"} {
		if _, err := os.Stat(f); err == nil {
			t.Errorf("%s must be gone", f)
		}
	}
	if _, err := os.Stat(filepath.Join(x, "state", "rota", "projects.json")); err != nil {
		t.Error(err)
	}
}

func TestMigrationKeepsTheStateRegistryWhenBothExist(t *testing.T) {
	x := isolate(t)
	proj := t.TempDir()
	if _, err := Register(proj, time.Now()); err != nil {
		t.Fatal(err)
	}
	old := oldRegistry(t, x, `{"projects":[{"path":"/old","name":"old"}]}`)
	ps, _ := List()
	if len(ps) != 1 || ps[0].Path != repos.Realpath(proj) {
		t.Fatalf("state file must win: %+v", ps)
	}
	for _, f := range []string{old, old + ".lock"} {
		if _, err := os.Stat(f); err == nil {
			t.Errorf("%s must be removed", f)
		}
	}
}
