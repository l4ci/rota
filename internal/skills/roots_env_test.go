package skills

import (
	"path/filepath"
	"reflect"
	"testing"
)

func fakeEnv(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestClaudeDirInjectedEnv(t *testing.T) {
	for _, c := range []struct {
		name string
		env  map[string]string
		home string
		want string
	}{
		{"env set wins", map[string]string{"CLAUDE_CONFIG_DIR": "/cfg"}, "/h", "/cfg"},
		{"env set, home unset", map[string]string{"CLAUDE_CONFIG_DIR": "/cfg"}, "", "/cfg"},
		{"env unset", nil, "/h", filepath.Join("/h", ".claude")},
		{"both unset", nil, "", ""},
	} {
		if got := ClaudeDir(fakeEnv(c.env), c.home); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func TestInstalledRootsOrder(t *testing.T) {
	j := filepath.Join
	full := InstalledRoots(fakeEnv(map[string]string{"HOME": "/h", "CLAUDE_CONFIG_DIR": "/cfg"}), "/p")
	want := []string{j("/p", ".claude", "skills"), j("/p", ".agents", "skills"), j("/cfg", "skills"), j("/h", ".agents", "skills")}
	if !reflect.DeepEqual(full, want) {
		t.Errorf("env set: %v want %v", full, want)
	}
	unset := InstalledRoots(fakeEnv(map[string]string{"HOME": "/h"}), "/p")
	want = []string{j("/p", ".claude", "skills"), j("/p", ".agents", "skills"), j("/h", ".claude", "skills"), j("/h", ".agents", "skills")}
	if !reflect.DeepEqual(unset, want) {
		t.Errorf("env unset: %v want %v", unset, want)
	}
	noHome := InstalledRoots(fakeEnv(nil), "")
	if len(noHome) != 0 {
		t.Errorf("home unset: %v want none", noHome)
	}
	onlyEnv := InstalledRoots(fakeEnv(map[string]string{"CLAUDE_CONFIG_DIR": "/cfg"}), "")
	if !reflect.DeepEqual(onlyEnv, []string{j("/cfg", "skills")}) {
		t.Errorf("home unset, env set: %v", onlyEnv)
	}
}
