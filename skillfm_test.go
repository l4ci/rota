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

const foldedDescription = "Use when the user wants a thing: a colon in plain YAML\n  would break Codex, so the text is folded.\n"

const foldedWant = "Use when the user wants a thing: a colon in plain YAML would break Codex, so the text is folded."

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
			lines[i] = "description: >-\n  " + strings.TrimSpace(foldedDescription)
		}
	}
	if err := os.WriteFile(skill, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("validator", func(t *testing.T) {
		// The fixture tree has no test/ dir; run the repo's validator against it.
		abs, _ := filepath.Abs("test/validate-skills.py")
		cmd := exec.Command("python3", abs)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "ROTA_DOCLINT_PROSE=off")
		if out, err := cmd.CombinedOutput(); err != nil && strings.Contains(string(out), "skills/rota-work/SKILL.md") {
			t.Fatalf("validator rejects a folded description: %s", out)
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
		if _, err := set.Stubbed(); err != nil {
			t.Fatalf("stub rejects a folded description: %v", err)
		}
	})
}
