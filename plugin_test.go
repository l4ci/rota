package rota

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The Claude Code plugin is the repo root (#602). Its version is VERSION's, so
// /rota:rota-install and the session-start hook ask for the binary this tree
// builds; every skill reaches the shared references through its own
// references/ link, as an installed skill does; and /rota:rota-install lives
// outside skills/, so the embedded set (rota skills install, Codex) never
// carries it.
func TestPluginManifest(t *testing.T) {
	var plugin struct {
		Name, Version string
		Skills        []string
		Hooks         string
	}
	readJSON(t, ".claude-plugin/plugin.json", &plugin)
	ver, err := os.ReadFile("VERSION")
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.TrimSpace(string(ver)); plugin.Version != want {
		t.Errorf("plugin.json version %q, VERSION %q: run rota release bump --file .claude-plugin/plugin.json --to %s", plugin.Version, want, want)
	}
	if plugin.Name != "rota" {
		t.Errorf("plugin name %q", plugin.Name)
	}

	var market struct {
		Name    string
		Plugins []map[string]any
	}
	readJSON(t, ".claude-plugin/marketplace.json", &market)
	if len(market.Plugins) != 1 || market.Plugins[0]["name"] != plugin.Name || market.Plugins[0]["source"] != "./" {
		t.Errorf("marketplace plugins %v", market.Plugins)
	}
	if _, ok := market.Plugins[0]["version"]; ok {
		t.Error("marketplace entry sets a version: plugin.json's wins and the validator warns; keep it in plugin.json only")
	}

	if _, err := os.Stat("plugin/skills/rota-install/SKILL.md"); err != nil {
		t.Error(err)
	}
	if _, err := os.Stat("skills/rota-install"); err == nil {
		t.Error("skills/rota-install would be embedded and installed for Codex too")
	}
	dirs, _ := filepath.Glob("skills/rota-*")
	if len(dirs) == 0 {
		t.Fatal("no skills")
	}
	for _, d := range dirs {
		link := filepath.Join(d, "references")
		if dst, err := os.Readlink(link); err != nil || dst != "../references" {
			t.Errorf("%s: want a symlink to ../references, got %q %v", link, dst, err)
		}
	}
}

func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}
