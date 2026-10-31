package rota_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/skills"
)

// The validator advises a folded block (`>-`) for a description Codex's strict
// YAML would drop. Every reader of SKILL.md front matter must accept that
// advice and read the same text: the validator, fmlite (which site-sync reads
// through) and the Go loader. This fixture runs one folded SKILL.md through all
// of them, site-sync end to end.

// Two paragraphs: YAML folds the blank line into a newline, and every reader must agree.
const foldedDescription = "Use when the user wants a thing: a colon in plain YAML\n  would break Codex.\n\n  A second paragraph, folded too.\n"

const foldedWant = "Use when the user wants a thing: a colon in plain YAML would break Codex.\nA second paragraph, folded too."

func copyTree(t *testing.T, dst string, srcs ...string) {
	t.Helper()
	for _, s := range srcs {
		if out, err := exec.Command("cp", "-R", s, dst).CombinedOutput(); err != nil {
			t.Fatalf("cp %s: %v\n%s", s, err, out)
		}
	}
}

func TestFoldedFrontMatterReadersAgree(t *testing.T) {
	root := t.TempDir()
	copyTree(t, root, "docs", "skills", "scripts", "llms.txt", "README.md", "CHANGELOG.md")
	skill := filepath.Join(root, "skills/rota-work/SKILL.md")
	b, err := os.ReadFile(skill)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(b), "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, "description:") {
			lines[i] = "description: >-\n  " + strings.TrimRight(foldedDescription, "\n")
		}
	}
	if err := os.WriteFile(skill, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("validator", func(t *testing.T) {
		abs, _ := filepath.Abs("test/validate-skills.py")
		cmd := exec.Command("python3", abs, "--fields", skill)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("validator cannot read a folded description: %v", err)
		}
		var fm map[string]string
		if err := json.Unmarshal(out, &fm); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		if fm["description"] != foldedWant {
			t.Errorf("validator read %q, want %q", fm["description"], foldedWant)
		}
		// The whole-tree run must pass too: a crash or any issue fails the test.
		full := exec.Command("python3", abs, "--root", root, "--no-prose")
		if out, err := full.CombinedOutput(); err != nil {
			t.Fatalf("validator rejects the folded fixture tree: %s", out)
		}
	})

	t.Run("fmlite", func(t *testing.T) {
		out, err := exec.Command("python3", "-I", "-c",
			"import sys, json; sys.path.insert(0, sys.argv[1]); import fmlite; print(json.dumps(fmlite.load_front_matter(sys.argv[2])[0]))",
			filepath.Join(root, "scripts"), skill).CombinedOutput()
		if err != nil {
			t.Fatalf("fmlite rejects a folded description: %s", out)
		}
		var fm map[string]string
		if err := json.Unmarshal(out, &fm); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		if fm["description"] != foldedWant {
			t.Errorf("fmlite read %q, want %q", fm["description"], foldedWant)
		}
	})

	t.Run("site-sync", func(t *testing.T) {
		site := t.TempDir()
		if out, err := exec.Command("python3", filepath.Join(root, "scripts/site-sync.py"), site).CombinedOutput(); err != nil {
			t.Fatalf("site-sync rejects a folded description: %s", out)
		}
		data, err := os.ReadFile(filepath.Join(site, "_data/rota/skills.json"))
		if err != nil {
			t.Fatal(err)
		}
		var cards []struct{ Skill, When string }
		if err := json.Unmarshal(data, &cards); err != nil {
			t.Fatal(err)
		}
		for _, c := range cards {
			if c.Skill == "rota-work" {
				if c.When != foldedWant {
					t.Errorf("site-sync card reads %q, want %q", c.When, foldedWant)
				}
				return
			}
		}
		t.Error("site-sync wrote no rota-work card")
	})

	t.Run("go-load", func(t *testing.T) {
		set, err := skills.Load(os.DirFS(filepath.Join(root, "skills")))
		if err != nil {
			t.Fatalf("Load rejects a folded description: %v", err)
		}
		// internal/skills treats front matter as opaque bytes: this leg proves
		// acceptance only, not text agreement.
		if _, err := set.Stubbed(); err != nil {
			t.Fatalf("stub rejects a folded description: %v", err)
		}
	})
}

// The plugin ships its own rota-install skill outside skills/, and it is the
// first one a plugin user hits: the validator must hold it to the same rules.
func TestValidatorLintsPluginSkills(t *testing.T) {
	abs, _ := filepath.Abs("test/validate-skills.py")
	root := t.TempDir()
	copyTree(t, root, "skills", "plugin")
	run := func() (string, error) {
		out, err := exec.Command("python3", abs, "--root", root, "--no-prose").CombinedOutput()
		return string(out), err
	}
	if out, err := run(); err != nil {
		t.Fatalf("clean tree rejected: %v\n%s", err, out)
	}
	skill := filepath.Join(root, "plugin/skills/rota-install/SKILL.md")
	b, err := os.ReadFile(skill)
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(string) string{
		"no front matter": func(s string) string { return s[strings.Index(s[3:], "---")+6:] },
		"second person": func(s string) string {
			return strings.Replace(s, "description: Use on", "description: Use when you want to install", 1)
		},
		"broken link": func(s string) string { return s + "\nSee [x](references/nope.md).\n" },
	} {
		if err := os.WriteFile(skill, []byte(mutate(string(b))), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := run()
		if err == nil || !strings.Contains(out, "plugin/skills/rota-install/SKILL.md") {
			t.Errorf("%s: validator did not flag the plugin skill: %v\n%s", name, err, out)
		}
	}
}
