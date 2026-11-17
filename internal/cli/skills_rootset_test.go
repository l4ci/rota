package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/l4ci/rota/internal/skills"
)

// skillsRootSet is the one place that decides which roots status, doctor and
// update look at: project and user roots always, plugin roots only on request.
func TestSkillsRootSet(t *testing.T) {
	home, top := t.TempDir(), t.TempDir()
	claude := filepath.Join(home, ".claude")
	install := filepath.Join(t.TempDir(), "rota-plugin")
	if err := os.MkdirAll(filepath.Join(claude, "plugins"), 0o755); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"plugins": map[string]any{"rota@rota": []map[string]string{{"scope": "user", "installPath": install}}}})
	if err := os.WriteFile(filepath.Join(claude, "plugins", "installed_plugins.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	getenv := func(string) string { return "" }
	type key struct {
		path, scope string
		plugin      bool
	}
	keys := func(rs []skills.Root) map[key]bool {
		m := map[key]bool{}
		for _, r := range rs {
			m[key{r.Path, r.Scope, r.Plugin}] = true
		}
		return m
	}
	userRoot := key{filepath.Join(claude, "skills"), skills.User, false}
	projRoot := key{filepath.Join(top, ".claude", "skills"), skills.Project, false}
	plugRoot := key{filepath.Join(install, "skills"), skills.User, true}

	for _, tc := range []struct {
		name    string
		scope   string
		plugins bool
		top     string
		want    []key
		absent  []key
	}{
		{"both scopes with plugins", "", true, top, []key{userRoot, projRoot, plugRoot}, nil},
		{"plugins excluded on request", "", false, top, []key{userRoot, projRoot}, []key{plugRoot}},
		{"user scope leaves the project root out", skills.User, true, top, []key{userRoot, plugRoot}, []key{projRoot}},
		{"outside a work tree there is no project root", "", true, "", []key{userRoot, plugRoot}, []key{projRoot}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			roots, warns, err := skillsRootSet(getenv, home, []string{claude}, tc.scope, "claude", tc.top, tc.plugins)
			if err != nil || len(warns) != 0 {
				t.Fatalf("err %v warns %v", err, warns)
			}
			got := keys(roots)
			for _, k := range tc.want {
				if !got[k] {
					t.Errorf("missing %+v in %+v", k, got)
				}
			}
			for _, k := range tc.absent {
				if got[k] {
					t.Errorf("unexpected %+v", k)
				}
			}
		})
	}
}
