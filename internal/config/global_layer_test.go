package config

import (
	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/projects"
	"os"
	"path/filepath"
	"testing"
)

func layerRig(t *testing.T, global, project, local string) (root string) {
	t.Helper()
	gdir := t.TempDir()
	t.Setenv("ROTA_CONFIG_DIR", gdir)
	root = t.TempDir()
	write := func(path, body string) {
		if body == "" {
			return
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(GlobalPath(gdir), global)
	write(configPath(root), project)
	write(localPath(root), local)
	return root
}

func showOne(t *testing.T, root, key string) Entry {
	t.Helper()
	rows, err := Show(root, key, true)
	if err != nil || len(rows) != 1 {
		t.Fatalf("show %s: %v %v", key, rows, err)
	}
	return rows[0]
}

func TestGlobalIsALayerUnderProjectAndLocal(t *testing.T) {
	root := layerRig(t,
		`{"work":{"isolation":"worktree","mergeStrategy":"pr"},"ship":{"review":false}}`,
		`{"work":{"mergeStrategy":"direct"}}`,
		`{"ship":{"review":true}}`)
	cases := []struct {
		key  string
		want any
		src  string
	}{
		{"work.isolation", "worktree", "global"},
		{"work.mergeStrategy", "direct", "project"},
		{"ship.review", true, "local"},
	}
	for _, c := range cases {
		e := showOne(t, root, c.key)
		if e.Value != c.want || e.Source != c.src {
			t.Errorf("%s = %v (%s), want %v (%s)", c.key, e.Value, e.Source, c.want, c.src)
		}
		if v, _ := Lookup(Load(configPath(root)), c.key); v != c.want {
			t.Errorf("Load %s = %v, want %v", c.key, v, c.want)
		}
	}
}

func TestGlobalNeverSuppliesProjectOwnedKeys(t *testing.T) {
	root := layerRig(t, `{"git":{"baseBranch":"trunk"},"umbrella":{"enabled":true},"rota":{"version":"9.9.9"}}`, `{}`, "")
	for _, k := range []string{"git.baseBranch", "umbrella.enabled", VersionKey} {
		if v, ok := Lookup(Load(configPath(root)), k); ok {
			t.Errorf("%s read from global: %v", k, v)
		}
	}
}

func TestGlobalChangeReachesEveryProject(t *testing.T) {
	a := layerRig(t, `{"work":{"isolation":"worktree"}}`, `{}`, "")
	b := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(configPath(b)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath(b), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	gdir := os.Getenv("ROTA_CONFIG_DIR")
	if err := os.WriteFile(GlobalPath(gdir), []byte(`{"work":{"isolation":"branch"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, r := range []string{a, b} {
		if e := showOne(t, r, "work.isolation"); e.Value != "branch" || e.Source != "global" {
			t.Errorf("%s: %v (%s)", r, e.Value, e.Source)
		}
	}
}

func TestMissingOrCorruptGlobalIsSkipped(t *testing.T) {
	root := layerRig(t, "", `{"work":{"mergeStrategy":"pr"}}`, "")
	if e := showOne(t, root, "work.mergeStrategy"); e.Value != "pr" || e.Source != "project" {
		t.Errorf("missing global: %v (%s)", e.Value, e.Source)
	}
	root = layerRig(t, `{not json`, `{"work":{"mergeStrategy":"pr"}}`, "")
	if e := showOne(t, root, "work.mergeStrategy"); e.Value != "pr" {
		t.Errorf("corrupt global: %v", e.Value)
	}
	if e := showOne(t, root, "work.isolation"); e.Source != "default" {
		t.Errorf("corrupt global leaked: %v (%s)", e.Value, e.Source)
	}
}

func TestResetAppliesTheRequiredKeyRule(t *testing.T) {
	const key = "work.mergeStrategy" // a required key
	cases := []struct {
		name, global string
		wantWritten  bool
	}{
		{"no global supplier", ``, true},
		{"global supplies it", `{"work":{"mergeStrategy":"pr"}}`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := layerRig(t, c.global, `{"work":{"mergeStrategy":"pr"}}`, `{"work":{"mergeStrategy":"direct"}}`)
			if err := Reset(root, projects.OSEnv(), key); err != nil {
				t.Fatal(err)
			}
			if _, ok := walk(fsio.LoadJSON(localPath(root), jsonx.NewObject()), key); ok {
				t.Errorf("local override survived Reset")
			}
			v, ok := walk(fsio.LoadJSON(configPath(root), jsonx.NewObject()), key)
			if ok != c.wantWritten {
				t.Fatalf("project holds %s = %v (present %v), want present %v", key, v, ok, c.wantWritten)
			}
			if ok && v != Default(keyByName(t, key)) {
				t.Errorf("project %s = %v, want the default", key, v)
			}
		})
	}
}

func keyByName(t *testing.T, name string) Key {
	t.Helper()
	for _, k := range Keys {
		if k.Name == name {
			return k
		}
	}
	t.Fatalf("no key %s", name)
	return Key{}
}
