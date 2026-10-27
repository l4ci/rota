package cli

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/projects"
	"github.com/l4ci/rota/internal/repos"
	"github.com/l4ci/rota/internal/roundlease"
	"github.com/l4ci/rota/internal/tui"
)

func projectsXDG(t *testing.T) string {
	t.Helper()
	x := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", x)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", filepath.Join(x, "state"))
	return x
}

func TestInitSkipsTemporaryProject(t *testing.T) {
	x := projectsXDG(t)
	dir := t.TempDir()
	code, env, _ := initRun(t, dir, "init", "--no-blocks")
	if v, _ := initData(env).Get("projectRegistered"); code != 0 || v != false {
		t.Fatalf("temporary init: exit %d, projectRegistered %v; want false", code, v)
	}
	if _, err := os.Stat(filepath.Join(dir, ".rota", "config.json")); err != nil {
		t.Fatalf("temporary project must still be initialized: %v", err)
	}
	if _, err := os.Stat(filepath.Join(x, "state", "rota", "projects.json")); !os.IsNotExist(err) {
		t.Fatalf("temporary init must not create the registry: %v", err)
	}
	code, out, _ := runMainWith(testDeps(), "-C", dir, "init", "--no-blocks")
	if code != 0 || strings.Count(out, "project not registered: temporary directory\n") != 1 {
		t.Fatalf("temporary init must explain the skip once: exit %d, %q", code, out)
	}
}

// projectRun models OS temp roots outside the fixture tree, so these tests
// exercise normal registration even when the suite itself runs below /tmp.
func projectRun(t *testing.T, dir string, args ...string) (int, *jsonx.Object, string) {
	t.Helper()
	d := testDeps()
	d.TempDirs = func() []string { return []string{"/os-temporary-storage"} }
	return initRunWith(t, d, dir, args...)
}

func TestInitRegistersProjectIdempotently(t *testing.T) {
	x := projectsXDG(t)
	dir := t.TempDir()
	_, env, _ := projectRun(t, dir, "init", "--no-blocks")
	if v, _ := initData(env).Get("projectRegistered"); v != true {
		t.Errorf("first init: %v", v)
	}
	_, env, _ = projectRun(t, dir, "init", "--no-blocks")
	d := initData(env)
	if v, _ := d.Get("projectRegistered"); v != false {
		t.Errorf("re-init: %v", v)
	}
	if v, _ := d.Get("changed"); v != false {
		t.Errorf("registry must not make re-init changed: %v", v)
	}
	if _, err := os.Stat(filepath.Join(x, "state", "rota", "projects.json")); err != nil {
		t.Fatal(err)
	}
	if ps, _ := projects.List(projects.Env{Getenv: os.Getenv, Home: os.UserHomeDir}); len(ps) != 1 {
		t.Fatalf("%+v", ps)
	}
}

func TestInitWarnsWhenRegistryUnwritable(t *testing.T) {
	x := projectsXDG(t)
	os.WriteFile(filepath.Join(x, "state"), []byte("a file, not a dir"), 0o644)
	code, env, errOut := projectRun(t, t.TempDir(), "init", "--no-blocks")
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
	projectRun(t, live, "init", "--no-blocks")
	projectRun(t, gone, "init", "--no-blocks")
	os.RemoveAll(gone)
	code, env, _ := projectRun(t, t.TempDir(), "projects")
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

func TestProjectsReportsTheStateDir(t *testing.T) {
	x := projectsXDG(t)
	code, env, _ := projectRun(t, t.TempDir(), "projects")
	dir, _ := initData(env).Get("dir")
	if want := filepath.Join(x, "state", "rota"); code != 0 || dir != want {
		t.Fatalf("exit %d dir %v, want %s", code, dir, want)
	}
}

func TestProjectsEmpty(t *testing.T) {
	projectsXDG(t)
	code, env, _ := projectRun(t, t.TempDir(), "projects")
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
		projectRun(t, d, "init", "--no-blocks")
	}
	os.RemoveAll(gone)
	os.RemoveAll(filepath.Join(bare, ".rota"))
	code, env, _ := projectRun(t, t.TempDir(), "projects", "cleanup")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	rows, _ := initData(env).Get("removed")
	if l, _ := rows.([]any); len(l) != 2 {
		t.Fatalf("removed %v", rows)
	}
	ps, _ := projects.List(projects.Env{Getenv: os.Getenv, Home: os.UserHomeDir})
	if len(ps) != 1 || ps[0].Path != repos.Realpath(live) {
		t.Fatalf("kept %+v", ps)
	}
	_, env, _ = projectRun(t, t.TempDir(), "projects", "cleanup")
	if v, _ := initData(env).Get("changed"); v != false {
		t.Errorf("second cleanup changed: %v", env)
	}
	if _, err := os.Stat(filepath.Join(x, "state", "rota", "projects.json")); err != nil {
		t.Fatal(err)
	}
}

func TestProjectsCleanupWithoutRegistryCreatesNothing(t *testing.T) {
	x := projectsXDG(t)
	if code, _, _ := projectRun(t, t.TempDir(), "projects", "cleanup"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if _, err := os.Stat(filepath.Join(x, "state", "rota")); err == nil {
		t.Error("cleanup created the config dir")
	}
}

func TestProjectsCleanupExplainsRemovals(t *testing.T) {
	for _, jsonOutput := range []bool{false, true} {
		t.Run(map[bool]string{false: "text", true: "json"}[jsonOutput], func(t *testing.T) {
			projectsXDG(t)
			root := t.TempDir()
			temp := filepath.Join(root, "temporary")
			d := testDeps()
			d.TempDirs = func() []string { return []string{temp} }
			env := d.projectsEnv()
			stamp := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			var wantLines []string
			var wantRows []any
			for _, tc := range []struct {
				name, reason string
				exists, rota bool
			}{
				{"temporary", "temporary", true, true},
				{"temporary/gone", "temporary", false, false},
				{"gone", "stale", false, false},
				{"bare", "stale", true, false},
				{"live", "", true, true},
			} {
				path := filepath.Join(root, tc.name)
				if tc.exists {
					dir := path
					if tc.rota {
						dir = filepath.Join(path, ".rota")
					}
					if err := os.MkdirAll(dir, 0o755); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := projects.Register(env, path, stamp); err != nil {
					t.Fatal(err)
				}
				if tc.reason == "" {
					continue
				}
				path = repos.Realpath(path)
				name := filepath.Base(path)
				line := "removed\t" + name + "\t" + path
				if tc.reason == "temporary" {
					line += "\t(temporary)"
				}
				wantLines = append(wantLines, line)
				wantRows = append(wantRows, knObj("name", name, "path", path, "lastSeen", "2026-01-01T00:00:00Z", "reason", tc.reason))
			}
			if jsonOutput {
				code, result, _ := initRunWith(t, d, root, "projects", "cleanup")
				rows, _ := initData(result).Get("removed")
				changed, _ := initData(result).Get("changed")
				if code != 0 || changed != true || !reflect.DeepEqual(rows, wantRows) {
					t.Errorf("cleanup: exit %d changed %v removed %v; want %v", code, changed, rows, wantRows)
				}
			} else {
				code, out, _ := runMainWith(d, "projects", "cleanup")
				if want := strings.Join(wantLines, "\n") + "\n"; code != 0 || out != want {
					t.Errorf("cleanup: exit %d output %q; want %q", code, out, want)
				}
			}
			code, out, _ := runMainWith(d, "projects", "cleanup")
			if code != 0 || out != "noop: no stale or temporary projects\n" {
				t.Errorf("second cleanup: exit %d output %q", code, out)
			}
			kept, err := projects.List(env)
			if err != nil || len(kept) != 1 || kept[0].Path != repos.Realpath(filepath.Join(root, "live")) {
				t.Errorf("kept projects: %+v, %v", kept, err)
			}
		})
	}
}

func TestProjectsRemoveDropsOneEntryAndKeepsTheDirectory(t *testing.T) {
	projectsXDG(t)
	a, b := t.TempDir(), t.TempDir()
	projectRun(t, a, "init", "--no-blocks")
	projectRun(t, b, "init", "--no-blocks")
	code, env, _ := projectRun(t, t.TempDir(), "projects", "remove", a)
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
	if ps, _ := projects.List(projects.Env{Getenv: os.Getenv, Home: os.UserHomeDir}); len(ps) != 1 || ps[0].Path != repos.Realpath(b) {
		t.Fatalf("kept %+v", ps)
	}
}

func TestProjectsRemoveNotRegisteredIsANoopAndNeedsOneDir(t *testing.T) {
	x := projectsXDG(t)
	code, env, _ := projectRun(t, t.TempDir(), "projects", "remove", t.TempDir())
	if v, _ := initData(env).Get("changed"); code != 0 || v != false {
		t.Fatalf("%d %v", code, env)
	}
	if _, err := os.Stat(filepath.Join(x, "state", "rota")); err == nil {
		t.Error("remove created the config dir")
	}
	for _, args := range [][]string{{"projects", "remove"}, {"projects", "remove", "a", "b"}} {
		if code, _, _ := projectRun(t, t.TempDir(), args...); code != ExitUsage {
			t.Errorf("%v: exit %d, want usage", args, code)
		}
	}
}

func TestProjectsViewStatusRoundAndLease(t *testing.T) {
	projectsXDG(t)
	live, bare, gone := t.TempDir(), t.TempDir(), filepath.Join(t.TempDir(), "gone")
	os.Mkdir(gone, 0o755)
	for _, d := range []string{live, bare, gone} {
		projectRun(t, d, "init", "--no-blocks")
	}
	os.RemoveAll(filepath.Join(bare, ".rota"))
	os.RemoveAll(gone)
	res, err := runProjects(&Ctx{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	rows := projectRows(&Ctx{}, res.Data, roundlease.Env{})
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
	projectRun(t, root, "init", "--no-blocks")
	if _, round, lease := projectState(&Ctx{}, root, false, roundlease.Env{}); round != "none" || lease != "none" {
		t.Errorf("fresh project: round %q lease %q", round, lease)
	}
	os.WriteFile(filepath.Join(root, ".rota", "workers.json"), []byte(`{"round": 4, "slots": []}`), 0o644)
	if _, round, _ := projectState(&Ctx{}, root, false, roundlease.Env{}); round != "4" {
		t.Errorf("round from workers.json: %q", round)
	}
}

func TestProjectsViewHonorsCancellation(t *testing.T) {
	projectsXDG(t)
	root, common := trProject(t, "{}"), t.TempDir()
	d := testDeps()
	if _, err := projects.Register(d.projectsEnv(), root, time.Now()); err != nil {
		t.Fatal(err)
	}
	lease := roundlease.Env{Host: "test-host", Alive: func(int) bool { return true }, Now: time.Now}
	if _, _, _, err := lease.Acquire(common, root, roundlease.Holder{PID: 100}, 7); err != nil {
		t.Fatal(err)
	}
	d.LeaseEnv = func() roundlease.Env { return lease }
	d.Git = func(ctx context.Context, _ string, _ ...string) (git.Result, error) {
		return git.Result{Stdout: common}, ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := &Ctx{Deps: d, ctx: ctx}
	res, err := runProjects(c, nil)
	if err != nil {
		t.Fatal(err)
	}
	m, err := projectsView(c, res)
	if err != nil {
		t.Fatal(err)
	}
	if rendered := m.Render(160, 20, tui.Style{}); !strings.Contains(rendered, "round lease: none") {
		t.Fatalf("canceled view read the lease:\n%s", rendered)
	}
	rows, err := m.(projects.Screen).Load()
	if err != nil || len(rows) != 1 || rows[0].Round != "none" || rows[0].Lease != "none" {
		t.Fatalf("canceled reload: %+v, %v; want no round or lease", rows, err)
	}
}

func TestProjectsUIOpensTheScreenFromTheVerbsData(t *testing.T) {
	r := newUIRig(t)
	projectsXDG(t)
	dir := t.TempDir()
	projectRun(t, dir, "init", "--no-blocks")
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

func TestProjectsListsTheSameAfterTheStateMigration(t *testing.T) {
	x := projectsXDG(t)
	proj := t.TempDir()
	os.MkdirAll(filepath.Join(x, "rota"), 0o755)
	old := filepath.Join(x, "rota", "projects.json")
	os.WriteFile(old, []byte(`{"projects":[{"path":"`+proj+`","name":"p","lastSeen":"2026-01-01T00:00:00Z"}]}`), 0o644)
	_, env, _ := projectRun(t, t.TempDir(), "projects")
	rows, _ := initData(env).Get("projects")
	if l, _ := rows.([]any); len(l) != 1 {
		t.Fatalf("rows %v", rows)
	}
	if _, err := os.Stat(old); err == nil {
		t.Error("old registry must be moved")
	}
	if _, err := os.Stat(filepath.Join(x, "state", "rota", "projects.json")); err != nil {
		t.Error(err)
	}
}
