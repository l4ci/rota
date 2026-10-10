package rota

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Codex drops symlinks when it caches a plugin (#676), so the skills/*/references
// links cannot ship. scripts/codex-plugin.sh builds the Codex tree at release
// time (release.yml publishes it to the codex branch); this builds it into a
// temp dir and checks what Codex would install.
func TestCodexPluginTree(t *testing.T) {
	out := t.TempDir()
	if b, err := exec.Command("bash", "scripts/codex-plugin.sh", out).CombinedOutput(); err != nil {
		t.Fatalf("codex-plugin.sh: %v\n%s", err, b)
	}
	root := filepath.Join(out, "plugins", "rota")
	got := map[string]bool{}
	err := filepath.WalkDir(root, func(p string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		got[filepath.ToSlash(rel)] = true
		if e.Type()&fs.ModeSymlink != 0 {
			t.Errorf("%s is a symlink: Codex drops it when it caches the plugin", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !got[".codex-plugin/plugin.json"] {
		t.Error("manifest missing from the built tree")
	}
	// Every skill file reaches the tree, and every shared reference reaches every skill.
	dirs, _ := filepath.Glob("skills/rota-*")
	refs, _ := filepath.Glob("skills/references/*.md")
	if len(dirs) == 0 || len(refs) == 0 {
		t.Fatal("no skills or references")
	}
	for _, d := range dirs {
		name := filepath.Base(d)
		filepath.WalkDir(d, func(p string, e fs.DirEntry, err error) error {
			if err == nil && !e.IsDir() && e.Type()&fs.ModeSymlink == 0 {
				rel, _ := filepath.Rel("skills", p)
				if !got["skills/"+filepath.ToSlash(rel)] {
					t.Errorf("%s missing from the Codex tree", rel)
				}
			}
			return nil
		})
		for _, r := range refs {
			if rel := name + "/references/" + filepath.Base(r); !got["skills/"+rel] {
				t.Errorf("skills/%s missing from the Codex tree", rel)
			}
		}
	}
	// Every references/ path a skill file names resolves in the built copy.
	link := regexp.MustCompile(`references/[A-Za-z0-9_.-]+\.md`)
	for rel := range got {
		if !strings.HasSuffix(rel, ".md") || strings.Contains(rel, "/references/") {
			continue
		}
		b, _ := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		for _, ref := range link.FindAllString(string(b), -1) {
			if !got[filepath.ToSlash(filepath.Join(filepath.Dir(rel), ref))] {
				t.Errorf("%s names %s, absent from the Codex tree", rel, ref)
			}
		}
	}
	if got["skills/rota-install/SKILL.md"] {
		t.Error("rota-install is Claude-only (CLAUDE_PLUGIN_ROOT); keep it out of the Codex tree")
	}
}

// The Codex manifest matches VERSION, and the marketplace installs it from the
// branch release.yml publishes.
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
			Source struct{ Source, URL, Ref, Path string }
		}
	}
	readJSON(t, ".agents/plugins/marketplace.json", &market)
	if len(market.Plugins) != 1 {
		t.Fatalf("marketplace plugins %+v", market.Plugins)
	}
	p := market.Plugins[0]
	if p.Name != plugin.Name || p.Source.Source != "git-subdir" || p.Source.URL != "https://github.com/l4ci/rota.git" ||
		p.Source.Ref != "codex" || p.Source.Path != "plugins/rota" {
		t.Errorf("marketplace entry %+v", p)
	}
	wf, err := os.ReadFile(".github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(wf), "scripts/codex-plugin.sh") || !strings.Contains(string(wf), "codex") {
		t.Error("release.yml does not build and publish the codex branch")
	}
}
