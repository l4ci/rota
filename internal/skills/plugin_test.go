package skills

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeFile(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPluginRoots(t *testing.T) {
	plugins, top, other := t.TempDir(), t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(plugins, "installed_plugins.json"), `{"version":2,"plugins":{
	  "rota@rota":[
	    {"scope":"user","installPath":"/c/rota/1"},
	    {"scope":"project","projectPath":"`+top+`","installPath":"/c/rota/2"},
	    {"scope":"local","projectPath":"`+other+`","installPath":"/c/rota/3"},
	    {"scope":"user","installPath":"/c/rota/1"}],
	  "other@mkt":[{"scope":"user","installPath":"/c/other/1"}]}}`)
	got := PluginRoots([]string{plugins}, top)
	want := []Root{
		{Path: "/c/rota/1/skills", Agent: Claude, Scope: User, Plugin: true},
		{Path: "/c/rota/2/skills", Agent: Claude, Scope: Project, Plugin: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("with top: %+v", got)
	}
	if got := PluginRoots([]string{plugins}, ""); len(got) != 1 || got[0].Path != "/c/rota/1/skills" {
		t.Errorf("no top: %+v", got)
	}
	writeFile(t, filepath.Join(plugins, "installed_plugins.json"), `{broken`)
	if got := PluginRoots([]string{plugins, filepath.Join(plugins, "nope")}, top); len(got) != 0 {
		t.Errorf("broken: %+v", got)
	}
}

func TestPluginsDirs(t *testing.T) {
	env := map[string]string{"CLAUDE_CODE_PLUGIN_CACHE_DIR": "/cache"}
	got := PluginsDirs(func(k string) string { return env[k] }, []string{"/a", "/b"})
	if want := []string{"/cache", "/b/plugins"}; !reflect.DeepEqual(got, want) {
		t.Errorf("%v", got)
	}
	got = PluginsDirs(func(string) string { return "" }, []string{"/a"})
	if want := []string{"/a/plugins"}; !reflect.DeepEqual(got, want) {
		t.Errorf("%v", got)
	}
}

// pluginDir writes the embedded set as a plugin install and returns its root.
func pluginDir(t *testing.T, set *Set) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".claude-plugin", "plugin.json"), `{"name":"rota","version":"0.14.0"}`)
	for _, p := range set.Paths() {
		b, _ := set.File(p)
		writeFile(t, filepath.Join(dir, "skills", filepath.FromSlash(p)), string(b))
	}
	return dir
}

func TestStatusPluginRoot(t *testing.T) {
	set, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	dir := pluginDir(t, set)
	root := Root{Path: filepath.Join(dir, "skills"), Agent: Claude, Scope: User, Plugin: true}
	status := func() RootStatus {
		rep, err := set.Status([]Root{root}, "1")
		if err != nil || len(rep.Roots) != 1 {
			t.Fatalf("%v %+v", err, rep)
		}
		return rep.Roots[0]
	}
	if st := status(); !st.Installed || !st.Current || st.Version != "0.14.0" || len(st.Missing) != 0 || len(st.Edited) != 0 {
		t.Errorf("clean: %+v", st)
	}
	p := set.Paths()[0]
	writeFile(t, filepath.Join(dir, "skills", filepath.FromSlash(p)), "changed\n")
	if st := status(); !st.Installed || st.Current {
		t.Errorf("changed: %+v", st)
	}
	os.Remove(filepath.Join(dir, "skills", filepath.FromSlash(p)))
	if st := status(); st.Current || !reflect.DeepEqual(st.Missing, []string{p}) {
		t.Errorf("deleted: %+v", st)
	}
	root.Path = filepath.Join(dir, "gone", "skills")
	if st := status(); st.Installed {
		t.Errorf("absent: %+v", st)
	}
}
