package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/l4ci/rota/internal/projects"
	"github.com/l4ci/rota/internal/repos"
	"github.com/l4ci/rota/internal/roundlease"
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

func TestProjectsRemoveDropsOneEntryAndKeepsTheDirectory(t *testing.T) {
	projectsXDG(t)
	a, b := t.TempDir(), t.TempDir()
	initRun(t, a, "init", "--no-blocks")
	initRun(t, b, "init", "--no-blocks")
	code, env, _ := initRun(t, t.TempDir(), "projects", "remove", a)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	d := initData(env)
	if v, _ := d.Get("changed"); v != true {
		t.Errorf("changed: %v", env)
	}
	rows, _ := d.Get("removed")
	if l, _ := rows.([]any); len(l) != 1 {
		t.Fatalf("removed %v", rows)
	}
	if _, err := os.Stat(filepath.Join(a, ".rota")); err != nil {
		t.Errorf("the project itself must stay: %v", err)
	}
	if ps, _ := projects.List(); len(ps) != 1 || ps[0].Path != repos.Realpath(b) {
		t.Fatalf("kept %+v", ps)
	}
}

func TestProjectsRemoveNotRegisteredIsANoopAndNeedsOneDir(t *testing.T) {
	x := projectsXDG(t)
	code, env, _ := initRun(t, t.TempDir(), "projects", "remove", t.TempDir())
	if v, _ := initData(env).Get("changed"); code != 0 || v != false {
		t.Fatalf("%d %v", code, env)
	}
	if _, err := os.Stat(filepath.Join(x, "rota")); err == nil {
		t.Error("remove created the config dir")
	}
	for _, args := range [][]string{{"projects", "remove"}, {"projects", "remove", "a", "b"}} {
		if code, _, _ := initRun(t, t.TempDir(), args...); code != ExitUsage {
			t.Errorf("%v: exit %d, want usage", args, code)
		}
	}
}

func TestProjectsViewStatusRoundAndLease(t *testing.T) {
	projectsXDG(t)
	live, bare, gone := t.TempDir(), t.TempDir(), filepath.Join(t.TempDir(), "gone")
	os.Mkdir(gone, 0o755)
	for _, d := range []string{live, bare, gone} {
		initRun(t, d, "init", "--no-blocks")
	}
	os.RemoveAll(filepath.Join(bare, ".rota"))
	os.RemoveAll(gone)
	res, err := runProjects(&Ctx{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	rows := projectRows(res.Data, roundlease.Env{})
	byPath := map[string]string{}
	for _, r := range rows {
		byPath[r.Path] = r.Status
	}
	want := map[string]string{repos.Realpath(live): "ok", repos.Realpath(bare): "no .rota/", repos.Realpath(gone): "missing directory"}
	if !reflect.DeepEqual(byPath, want) {
		t.Errorf("statuses %v, want %v", byPath, want)
	}
}

func TestProjectStateReadsRoundAndLease(t *testing.T) {
	root := t.TempDir()
	initRun(t, root, "init", "--no-blocks")
	if _, round, lease := projectState(root, false, roundlease.Env{}); round != "none" || lease != "none" {
		t.Errorf("fresh project: round %q lease %q", round, lease)
	}
	os.WriteFile(filepath.Join(root, ".rota", "workers.json"), []byte(`{"round": 4, "slots": []}`), 0o644)
	if _, round, _ := projectState(root, false, roundlease.Env{}); round != "4" {
		t.Errorf("round from workers.json: %q", round)
	}
}

func TestProjectsUIOpensTheScreenFromTheVerbsData(t *testing.T) {
	r := newUIRig(t)
	projectsXDG(t)
	dir := t.TempDir()
	initRun(t, dir, "init", "--no-blocks")
	r.root = Tree()
	code, out, _ := r.do("projects", "--ui")
	if code != 0 || out != "" {
		t.Fatalf("exit %d out %q", code, out)
	}
	if len(r.models) != 1 {
		t.Fatalf("screens opened: %d", len(r.models))
	}
	if _, ok := r.models[0].(projects.Screen); !ok {
		t.Errorf("model %T", r.models[0])
	}
	// the sub-verbs have no view
	for _, sub := range []string{"cleanup", "remove"} {
		if code, _, _ := r.do("projects", sub, "--ui"); code != ExitUsage {
			t.Errorf("projects %s --ui exit %d", sub, code)
		}
	}
}
