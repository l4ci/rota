package projects

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/cfgdir"
	"github.com/l4ci/rota/internal/repos"
)

// isolate returns a temp root and an Env whose config and state dirs live
// under it; HOME is a dir of its own, so nothing reads the real one.
func isolate(t *testing.T) (string, cfgdir.Env) {
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

func TestCleanupDropsTemporaryProjectAndKeepsLiveProject(t *testing.T) {
	x, env := isolate(t)
	temp := filepath.Join(x, "scratch")
	live := filepath.Join(x, "scratch-projects")
	env.TempDirs = func() []string { return []string{temp} }
	for _, dir := range []string{temp, live} {
		if err := os.MkdirAll(filepath.Join(dir, ".rota"), 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := Register(env, dir, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := Cleanup(env)
	if err != nil || len(removed) != 1 || removed[0].Path != repos.Realpath(temp) || removed[0].Missing {
		t.Fatalf("removed %+v, %v; want the existing temporary project", removed, err)
	}
	if ps, err := List(env); err != nil || len(ps) != 1 || ps[0].Path != repos.Realpath(live) {
		t.Fatalf("kept %+v, %v; want the live non-temporary project", ps, err)
	}
	if _, err := os.Stat(filepath.Join(temp, ".rota")); err != nil {
		t.Fatalf("cleanup must leave temporary project files alone: %v", err)
	}
}

func TestTemporaryUsesOSTempRootsAndDirectoryBoundaries(t *testing.T) {
	t.Setenv("TMPDIR", "/process-temp")
	env := fakeEnv(map[string]string{"TMPDIR": "/custom-temp"}, "/home")
	for _, tc := range []struct {
		path string
		want bool
	}{
		{"/tmp", true},
		{"/tmp/repo", true},
		{"/tmp-projects/repo", false},
		{"/custom-temp/repo", true},
		{"/custom-temp-projects/repo", false},
		{"/process-temp/repo", false},
		{"/var/folders/ab/cd/T/repo", true},
		{"/private/var/folders/ab/cd/T/repo", true},
		{"/private/var/folders-projects/repo", false},
		{"/home/project", false},
	} {
		t.Run(tc.path, func(t *testing.T) {
			if got := Temporary(env, tc.path); got != tc.want {
				t.Errorf("Temporary(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

func TestTemporaryResolvesProjectAndTempRootSymlinks(t *testing.T) {
	x, env := isolate(t)
	temp, live := filepath.Join(x, "temp"), filepath.Join(x, "live")
	for _, dir := range []string{temp, live} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	alias := filepath.Join(x, "temp-alias")
	if err := os.Symlink(temp, alias); err != nil {
		t.Fatal(err)
	}
	env.TempDirs = func() []string { return []string{alias} }
	intoTemp, outOfTemp := filepath.Join(x, "checkout"), filepath.Join(temp, "checkout")
	for link, target := range map[string]string{intoTemp: temp, outOfTemp: live} {
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
	if !Temporary(env, intoTemp) || !Temporary(env, filepath.Join(alias, "repo")) {
		t.Error("a resolved path under the temp root must be temporary")
	}
	if Temporary(env, outOfTemp) {
		t.Error("a symlink resolving outside temporary storage must stay eligible")
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

func TestListMigrationPreservesRegistryMode(t *testing.T) {
	x, env := isolate(t)
	old := oldRegistry(t, x, `{"projects":[{"path":"/private-project","name":"private"}]}`)
	if err := os.Chmod(old, 0o600); err != nil {
		t.Fatal(err)
	}
	ps, err := List(env)
	if err != nil || len(ps) != 1 || ps[0].Path != "/private-project" {
		t.Fatalf("migrated registry: %+v, %v", ps, err)
	}
	info, err := os.Stat(filepath.Join(x, "state", "rota", "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("migrated registry mode: %04o", info.Mode().Perm())
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("migrated registry mode = %04o, want 0600", got)
	}
	for _, f := range []string{old, old + ".lock"} {
		if _, err := os.Stat(f); !os.IsNotExist(err) {
			t.Errorf("%s must be gone, got %v", f, err)
		}
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

func fakeEnv(vars map[string]string, home string) cfgdir.Env {
	return cfgdir.Env{
		Getenv: func(k string) string { return vars[k] },
		Home: func() (string, error) {
			if home == "" {
				return "", os.ErrNotExist
			}
			return home, nil
		},
	}
}
