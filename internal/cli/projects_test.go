package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/l4ci/rota/internal/projects"
	"github.com/l4ci/rota/internal/repos"
)

func projectsXDG(t *testing.T) string {
	t.Helper()
	x := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", x)
	return x
}

func TestInitRegistersProjectIdempotently(t *testing.T) {
	x := projectsXDG(t)
	dir := t.TempDir()
	_, env, _ := initRun(t, dir, "init", "--no-blocks")
	if v, _ := initData(env).Get("projectRegistered"); v != true {
		t.Errorf("first init: %v", v)
	}
	_, env, _ = initRun(t, dir, "init", "--no-blocks")
	d := initData(env)
	if v, _ := d.Get("projectRegistered"); v != false {
		t.Errorf("re-init: %v", v)
	}
	if v, _ := d.Get("changed"); v != false {
		t.Errorf("registry must not make re-init changed: %v", v)
	}
	if _, err := os.Stat(filepath.Join(x, "rota", "projects.json")); err != nil {
		t.Fatal(err)
	}
	if ps, _ := projects.List(); len(ps) != 1 {
		t.Fatalf("%+v", ps)
	}
}

func TestInitWarnsWhenRegistryUnwritable(t *testing.T) {
	x := projectsXDG(t)
	os.WriteFile(filepath.Join(x, "rota"), []byte("a file, not a dir"), 0o644)
	code, env, errOut := initRun(t, t.TempDir(), "init", "--no-blocks")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if _, ok := initData(env).Get("warnings"); !ok || errOut == "" {
		t.Errorf("no warning: %v %q", env, errOut)
	}
}

func TestProjectsListsAndMarksMissing(t *testing.T) {
	projectsXDG(t)
	live, gone := t.TempDir(), filepath.Join(t.TempDir(), "gone")
	os.Mkdir(gone, 0o755)
	initRun(t, live, "init", "--no-blocks")
	initRun(t, gone, "init", "--no-blocks")
	os.RemoveAll(gone)
	code, env, _ := initRun(t, t.TempDir(), "projects")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	rows, _ := initData(env).Get("projects")
	list, _ := rows.([]any)
	if len(list) != 2 {
		t.Fatalf("%v", rows)
	}
	missing := 0
	for _, r := range list {
		o := r.(interface{ Get(string) (any, bool) })
		if m, _ := o.Get("missing"); m == true {
			missing++
		}
	}
	if missing != 1 {
		t.Errorf("missing = %d: %v", missing, rows)
	}
}

func TestProjectsEmpty(t *testing.T) {
	projectsXDG(t)
	code, env, _ := initRun(t, t.TempDir(), "projects")
	rows, _ := initData(env).Get("projects")
	if l, _ := rows.([]any); code != 0 || len(l) != 0 {
		t.Fatalf("%d %v", code, env)
	}
}

func TestProjectsCleanupDropsGoneAndNoLongerRota(t *testing.T) {
	x := projectsXDG(t)
	live, gone, bare := t.TempDir(), filepath.Join(t.TempDir(), "gone"), t.TempDir()
	os.Mkdir(gone, 0o755)
	for _, d := range []string{live, gone, bare} {
		initRun(t, d, "init", "--no-blocks")
	}
	os.RemoveAll(gone)
	os.RemoveAll(filepath.Join(bare, ".rota"))
	code, env, _ := initRun(t, t.TempDir(), "projects", "cleanup")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	rows, _ := initData(env).Get("removed")
	if l, _ := rows.([]any); len(l) != 2 {
		t.Fatalf("removed %v", rows)
	}
	ps, _ := projects.List()
	if len(ps) != 1 || ps[0].Path != repos.Realpath(live) {
		t.Fatalf("kept %+v", ps)
	}
	_, env, _ = initRun(t, t.TempDir(), "projects", "cleanup")
	if v, _ := initData(env).Get("changed"); v != false {
		t.Errorf("second cleanup changed: %v", env)
	}
	if _, err := os.Stat(filepath.Join(x, "rota", "projects.json")); err != nil {
		t.Fatal(err)
	}
}

func TestProjectsCleanupWithoutRegistryCreatesNothing(t *testing.T) {
	x := projectsXDG(t)
	if code, _, _ := initRun(t, t.TempDir(), "projects", "cleanup"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if _, err := os.Stat(filepath.Join(x, "rota")); err == nil {
		t.Error("cleanup created the config dir")
	}
}
