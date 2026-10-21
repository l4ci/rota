package projects

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/repos"
)

// isolate returns a temp root and an Env whose config and state dirs live
// under it; HOME is a dir of its own, so nothing reads the real one.
func isolate(t *testing.T) (string, Env) {
	t.Helper()
	x := t.TempDir()
	return x, fakeEnv(map[string]string{
		"XDG_CONFIG_HOME": filepath.Join(x, "config"),
		"XDG_STATE_HOME":  filepath.Join(x, "state"),
	}, filepath.Join(x, "home"))
}

func TestRegisterIsIdempotentAndRefreshesLastSeen(t *testing.T) {
	_, env := isolate(t)
	proj := t.TempDir()
	if added, err := Register(env, proj, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)); err != nil || !added {
		t.Fatalf("first: %v %v", added, err)
	}
	if added, err := Register(env, proj, time.Date(2026, 2, 2, 0, 0, 0, 0, time.UTC)); err != nil || added {
		t.Fatalf("second: %v %v", added, err)
	}
	ps, _ := List(env)
	if len(ps) != 1 || ps[0].LastSeen != "2026-02-02T00:00:00Z" || ps[0].Name != filepath.Base(proj) || ps[0].Missing {
		t.Fatalf("%+v", ps)
	}
}

func TestRegisterResolvesSymlinks(t *testing.T) {
	_, env := isolate(t)
	proj := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(proj, link); err != nil {
		t.Skip(err)
	}
	Register(env, proj, time.Now())
	Register(env, link, time.Now())
	if ps, _ := List(env); len(ps) != 1 {
		t.Fatalf("%+v", ps)
	}
}

func TestListFlagsMissingPathsAndKeepsThem(t *testing.T) {
	_, env := isolate(t)
	gone := filepath.Join(t.TempDir(), "gone")
	os.Mkdir(gone, 0o755)
	live := t.TempDir()
	Register(env, gone, time.Now())
	Register(env, live, time.Now())
	os.Remove(gone)
	ps, _ := List(env)
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
	x, env := isolate(t)
	if ps, err := List(env); err != nil || len(ps) != 0 {
		t.Fatalf("%v %v", ps, err)
	}
	os.MkdirAll(filepath.Join(x, "state", "rota"), 0o755)
	os.WriteFile(filepath.Join(x, "state", "rota", "projects.json"), []byte("{nope"), 0o644)
	if ps, err := List(env); err != nil || len(ps) != 0 {
		t.Fatalf("%v %v", ps, err)
	}
	if added, err := Register(env, t.TempDir(), time.Now()); err != nil || !added {
		t.Fatalf("register over corrupt: %v %v", added, err)
	}
}

func TestRemoveDropsOneEntryAndKeepsTheDirectory(t *testing.T) {
	_, env := isolate(t)
	a, b := t.TempDir(), t.TempDir()
	Register(env, a, time.Now())
	Register(env, b, time.Now())
	link := filepath.Join(t.TempDir(), "link")
	os.Symlink(a, link)
	got, err := Remove(env, link)
	if err != nil || len(got) != 1 || got[0].Path != repos.Realpath(a) {
		t.Fatalf("remove via symlink: %+v %v", got, err)
	}
	if _, err := os.Stat(a); err != nil {
		t.Errorf("the directory must stay: %v", err)
	}
	if ps, _ := List(env); len(ps) != 1 || ps[0].Path != repos.Realpath(b) {
		t.Errorf("other entry must stay: %+v", ps)
	}
	if got, _ := Remove(env, a); len(got) != 0 {
		t.Errorf("second remove should match nothing: %+v", got)
	}
}

func TestRemoveFindsAGoneDirectoryAndSkipsAnAbsentRegistry(t *testing.T) {
	x, env := isolate(t)
	if got, err := Remove(env, "/nope"); err != nil || got != nil {
		t.Fatalf("absent registry: %+v %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(x, "state", "rota", "projects.json")); err == nil {
		t.Error("an absent registry must not be created")
	}
	gone := filepath.Join(t.TempDir(), "gone")
	os.Mkdir(gone, 0o755)
	Register(env, gone, time.Now())
	os.Remove(gone)
	got, err := Remove(env, gone)
	if err != nil || len(got) != 1 || !got[0].Missing {
		t.Fatalf("gone dir: %+v %v", got, err)
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
	x, env := isolate(t)
	proj := t.TempDir()
	old := oldRegistry(t, x, `{"projects":[{"path":"`+proj+`","name":"p","lastSeen":"2026-01-01T00:00:00Z"}]}`)
	ps, err := List(env)
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
	x, env := isolate(t)
	proj := t.TempDir()
	if _, err := Register(env, proj, time.Now()); err != nil {
		t.Fatal(err)
	}
	old := oldRegistry(t, x, `{"projects":[{"path":"/old","name":"old"}]}`)
	ps, _ := List(env)
	if len(ps) != 1 || ps[0].Path != repos.Realpath(proj) {
		t.Fatalf("state file must win: %+v", ps)
	}
	for _, f := range []string{old, old + ".lock"} {
		if _, err := os.Stat(f); err == nil {
			t.Errorf("%s must be removed", f)
		}
	}
}

func fakeEnv(vars map[string]string, home string) Env {
	return Env{
		Getenv: func(k string) string { return vars[k] },
		Home: func() (string, error) {
			if home == "" {
				return "", os.ErrNotExist
			}
			return home, nil
		},
	}
}

func TestResolveReadsOnlyTheInjectedEnv(t *testing.T) {
	t.Setenv("ROTA_CONFIG_DIR", "/process/override")
	t.Setenv("XDG_CONFIG_HOME", "/process/xdg")
	t.Setenv("HOME", "/process/home")
	cases := []struct {
		name         string
		vars         map[string]string
		home, dir    string
		source, warn string
	}{
		{"override", map[string]string{"ROTA_CONFIG_DIR": "/o/dir", "XDG_CONFIG_HOME": "/x"}, "/h", "/o/dir", SourceOverride, ""},
		{"xdg", map[string]string{"XDG_CONFIG_HOME": "/x"}, "/h", "/x/rota", SourceXDG, ""},
		{"home", nil, "/h", "/h/.config/rota", SourceDefault, ""},
		{"relative override", map[string]string{"ROTA_CONFIG_DIR": "rel"}, "/h", "/h/.config/rota", SourceDefault, "ROTA_CONFIG_DIR=rel is not an absolute path, ignored"},
	}
	for _, c := range cases {
		dir, src, warn, err := Resolve(fakeEnv(c.vars, c.home))
		if err != nil || dir != c.dir || src != c.source || warn != c.warn {
			t.Errorf("%s: got %q %q %q %v, want %q %q %q", c.name, dir, src, warn, err, c.dir, c.source, c.warn)
		}
	}
	if _, _, _, err := Resolve(fakeEnv(nil, "")); err == nil {
		t.Error("no env and no home: want an error")
	}
}

func TestDirLookupsReadOnlyTheInjectedEnv(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/process/xdg")
	t.Setenv("XDG_DATA_HOME", "/process/xdg")
	t.Setenv("XDG_STATE_HOME", "/process/xdg")
	t.Setenv("HOME", "/process/home")
	lookups := []struct {
		name, xdg, rel string
		fn             func(Env) (string, error)
	}{
		{"Dir", "XDG_CONFIG_HOME", ".config", Dir},
		{"DataDir", "XDG_DATA_HOME", ".local/share", DataDir},
		{"StateDir", "XDG_STATE_HOME", ".local/state", StateDir},
	}
	for _, l := range lookups {
		if d, err := l.fn(fakeEnv(map[string]string{l.xdg: "/x"}, "/h")); err != nil || d != "/x/rota" {
			t.Errorf("%s xdg: %q %v", l.name, d, err)
		}
		if d, err := l.fn(fakeEnv(map[string]string{l.xdg: "relative/ignored"}, "/h")); err != nil || d != "/h/"+l.rel+"/rota" {
			t.Errorf("%s home: %q %v", l.name, d, err)
		}
		if _, err := l.fn(fakeEnv(nil, "")); err == nil {
			t.Errorf("%s: no env and no home must error", l.name)
		}
	}
}
