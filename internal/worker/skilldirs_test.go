package worker

import (
	"path/filepath"
	"testing"

	"github.com/l4ci/rota/internal/jsonx"
)

func TestSkillClaudeDirs(t *testing.T) {
	home, acct := t.TempDir(), t.TempDir()
	gone := filepath.Join(home, "gone")
	cfg, err := jsonx.Decode([]byte(`{"round":{"accounts":[{"name":"a","configDir":"` + acct + `"},{"name":"g","configDir":"` + gone + `"},{"name":"d","configDir":"` + filepath.Join(home, ".claude") + `/"}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	noHome := func() (string, error) { return home, nil }

	// HOME unset in getenv falls back to userHome.
	h, dirs, skipped := SkillClaudeDirs(func(string) string { return "" }, noHome, cfg)
	if h != home {
		t.Errorf("home %q want %q", h, home)
	}
	want := []string{filepath.Join(home, ".claude"), acct}
	if len(dirs) != 2 || dirs[0] != want[0] || dirs[1] != want[1] {
		t.Errorf("dirs %v want %v", dirs, want)
	}
	if len(skipped) != 1 || skipped[0] != gone {
		t.Errorf("skipped %v want [%s]", skipped, gone)
	}
	// A nil config yields just the current dir.
	if _, dirs, _ = SkillClaudeDirs(func(k string) string {
		if k == "HOME" {
			return home
		}
		return ""
	}, noHome, nil); len(dirs) != 1 {
		t.Errorf("nil cfg: %v", dirs)
	}
}
