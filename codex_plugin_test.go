package rota

import (
	"flag"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var updateCodexTree = flag.Bool("update-codex-tree", false, "regenerate plugins/rota/skills from skills/")

const codexSkills = "plugins/rota/skills"

// codexTree is the skills tree the Codex plugin ships: each skill's own files
// plus a real copy of the shared references/ in place of the symlink. Codex
// drops symlinks when it caches a plugin (#676), so the Claude plugin's
// references/ links would arrive dangling. The tree is generated, committed
// and compared; go test -run TestCodexPluginTree -update-codex-tree rewrites it.
func codexTree(t *testing.T) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	refs, err := filepath.Glob("skills/references/*.md")
	if err != nil || len(refs) == 0 {
		t.Fatalf("no shared references: %v", err)
	}
	dirs, _ := filepath.Glob("skills/rota-*")
	for _, d := range dirs {
		name := filepath.Base(d)
		err := filepath.WalkDir(d, func(p string, e fs.DirEntry, err error) error {
			if err != nil || e.IsDir() || e.Type()&fs.ModeSymlink != 0 {
				return err
			}
			rel, _ := filepath.Rel("skills", p)
			b, err := os.ReadFile(p)
			out[filepath.ToSlash(rel)] = b
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range refs {
			b, err := os.ReadFile(r)
			if err != nil {
				t.Fatal(err)
			}
			out[name+"/references/"+filepath.Base(r)] = b
		}
	}
	return out
}

func TestCodexPluginTree(t *testing.T) {
	want := codexTree(t)
	if *updateCodexTree {
		if err := os.RemoveAll(codexSkills); err != nil {
			t.Fatal(err)
		}
		for rel, b := range want {
			p := filepath.Join(codexSkills, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, b, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	const hint = "run go test . -run TestCodexPluginTree -update-codex-tree"
	got := map[string]bool{}
	err := filepath.WalkDir(codexSkills, func(p string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(codexSkills, p)
		rel = filepath.ToSlash(rel)
		got[rel] = true
		if e.Type()&fs.ModeSymlink != 0 {
			t.Errorf("%s is a symlink: Codex drops it when it caches the plugin", rel)
			return nil
		}
		b, err := os.ReadFile(p)
		if w, ok := want[rel]; !ok {
			t.Errorf("%s has no source in skills/: %s", rel, hint)
		} else if err != nil || string(b) != string(w) {
			t.Errorf("%s differs from skills/: %s", rel, hint)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for rel := range want {
		if !got[rel] {
			t.Errorf("%s missing from %s: %s", rel, codexSkills, hint)
		}
	}
	// Every references/ path a SKILL.md or its sibling files name resolves in
	// the installed copy.
	link := regexp.MustCompile(`references/[A-Za-z0-9_.-]+\.md`)
	for rel := range got {
		if !strings.HasSuffix(rel, ".md") || strings.Contains(rel, "/references/") {
			continue
		}
		b, _ := os.ReadFile(filepath.Join(codexSkills, filepath.FromSlash(rel)))
		for _, ref := range link.FindAllString(string(b), -1) {
			if !got[filepath.ToSlash(filepath.Join(filepath.Dir(rel), ref))] {
				t.Errorf("%s names %s, absent from the Codex tree", rel, ref)
			}
		}
	}
	if got["rota-install/SKILL.md"] {
		t.Error("rota-install is Claude-only (CLAUDE_PLUGIN_ROOT); keep it out of the Codex tree")
	}
}

// The Codex manifest and marketplace agree with VERSION and with each other.
func TestCodexPluginManifest(t *testing.T) {
	var plugin struct {
		Name, Version, Skills string
	}
	readJSON(t, "plugins/rota/.codex-plugin/plugin.json", &plugin)
	ver, err := os.ReadFile("VERSION")
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.TrimSpace(string(ver)); plugin.Version != want {
		t.Errorf("Codex plugin.json version %q, VERSION %q: run rota release bump --file plugins/rota/.codex-plugin/plugin.json --to %s", plugin.Version, want, want)
	}
	if plugin.Skills != "./skills/" {
		t.Errorf("Codex plugin.json skills %q, want ./skills/", plugin.Skills)
	}
	var market struct {
		Plugins []struct {
			Name   string
			Source struct{ Source, Path string }
		}
	}
	readJSON(t, ".agents/plugins/marketplace.json", &market)
	if len(market.Plugins) != 1 || market.Plugins[0].Name != plugin.Name ||
		market.Plugins[0].Source.Source != "local" || market.Plugins[0].Source.Path != "./plugins/rota" {
		t.Errorf("marketplace plugins %+v", market.Plugins)
	}
	if _, err := os.Stat(filepath.Join("plugins/rota", filepath.FromSlash(strings.TrimPrefix(market.Plugins[0].Source.Path, "./plugins/rota")), ".codex-plugin/plugin.json")); err != nil {
		t.Error(err)
	}
}
