package rota_test

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	rota "github.com/l4ci/rota"
	"github.com/l4ci/rota/internal/skills"
)

// The supported skill-file layout is owned by skills.SupportedFile. The
// binary (embed.go + Load), the validator and the Codex builder each select
// files their own way, in Go, Python and shell; these fixtures pin all of them
// to the owner's verdict, so a file one of them would drop or reject fails here.

func put(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// layoutFixture is a one-skill source tree: SKILL.md citing a shared reference
// and a flat sibling, an agents/ policy, and the references symlink.
func layoutFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	put(t, root, "skills/rota-a/SKILL.md", "---\nname: rota-a\n---\nRead [topic](topic.md) and [shared](references/shared.md).\n")
	put(t, root, "skills/rota-a/topic.md", "topic\n")
	put(t, root, "skills/rota-a/agents/openai.yaml", "policy:\n  allow_implicit_invocation: true\n")
	put(t, root, "skills/references/shared.md", "shared\n")
	if err := os.Symlink("../references", filepath.Join(root, "skills/rota-a/references")); err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile("scripts/codex-plugin.sh")
	if err != nil {
		t.Fatal(err)
	}
	put(t, root, "scripts/codex-plugin.sh", string(src))
	put(t, root, "plugins/rota/.codex-plugin/plugin.json", "{}\n")
	return root
}

// layoutVerdicts runs every packaging path over a source tree and returns what
// each shipped (sorted "<skill>/<path>" lists) or the error text of a rejection.
type layoutVerdicts struct {
	violations []string
	binary     []string
	codex      []string
	validator  string // stderr, empty when accepted
	codexErr   string
}

func runLayout(t *testing.T, root string) layoutVerdicts {
	t.Helper()
	var v layoutVerdicts
	skillsFS := os.DirFS(filepath.Join(root, "skills"))
	var err error
	if v.violations, err = skills.LayoutViolations(skillsFS); err != nil {
		t.Fatal(err)
	}
	set, err := skills.Load(skillsFS)
	if err != nil {
		t.Fatal(err)
	}
	v.binary = set.Paths()

	out, err := exec.Command("python3", "test/validate-skills.py", "--layout", filepath.Join(root, "skills")).CombinedOutput()
	if err != nil {
		v.validator = string(out)
	}

	dst := t.TempDir()
	out, err = exec.Command("bash", filepath.Join(root, "scripts/codex-plugin.sh"), dst).CombinedOutput()
	if err != nil {
		v.codexErr = string(out)
		return v
	}
	base := filepath.Join(dst, "plugins/rota/skills")
	filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if d.Type()&fs.ModeSymlink != 0 {
				t.Errorf("%s is a symlink in the Codex tree", p)
			}
			rel, _ := filepath.Rel(base, p)
			v.codex = append(v.codex, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(v.codex)
	return v
}

func TestSkillLayoutSupportedContentShips(t *testing.T) {
	v := runLayout(t, layoutFixture(t))
	if len(v.violations) != 0 || v.validator != "" || v.codexErr != "" {
		t.Fatalf("supported layout rejected: violations %v validator %q codex %q", v.violations, v.validator, v.codexErr)
	}
	want := "rota-a/SKILL.md rota-a/agents/openai.yaml rota-a/references/shared.md rota-a/topic.md"
	if got := strings.Join(v.binary, " "); got != want {
		t.Errorf("binary ships %s, want %s", got, want)
	}
	if got := strings.Join(v.codex, " "); got != want {
		t.Errorf("Codex tree ships %s, want %s (the shared reference arrives as a real file)", got, want)
	}
}

func TestSkillLayoutNestedContentRejected(t *testing.T) {
	root := layoutFixture(t)
	put(t, root, "skills/rota-a/topic.md", "see [deep](sub/deep.md)\n")
	put(t, root, "skills/rota-a/sub/deep.md", "deep\n")
	v := runLayout(t, root)
	const nested = "rota-a/sub/deep.md"
	if len(v.violations) != 1 || v.violations[0] != nested {
		t.Errorf("owner violations %v, want [%s]", v.violations, nested)
	}
	if !strings.Contains(v.validator, "skills/rota-a/sub/deep.md: unsupported skill content") {
		t.Errorf("validator did not name the nested file: %q", v.validator)
	}
	if !strings.Contains(v.codexErr, "unsupported skill content skills/rota-a/sub/deep.md") {
		t.Errorf("Codex builder accepted nested content: %q", v.codexErr)
	}
	for _, p := range v.binary {
		if p == nested {
			t.Errorf("binary ships %s: the layout owner and Load disagree", p)
		}
	}
}

// The real tree: nothing the owner rejects, and every file it accepts reaches
// the embedded FS (embed.go's patterns) and the Codex build.
func TestSkillLayoutRealTree(t *testing.T) {
	skillsFS := os.DirFS("skills")
	if bad, err := skills.LayoutViolations(skillsFS); err != nil || len(bad) != 0 {
		t.Fatalf("unsupported skill content %v (err %v)", bad, err)
	}
	dst := t.TempDir()
	if b, err := exec.Command("bash", "scripts/codex-plugin.sh", dst).CombinedOutput(); err != nil {
		t.Fatalf("codex-plugin.sh: %v\n%s", err, b)
	}
	n := 0
	fs.WalkDir(skillsFS, ".", func(p string, d fs.DirEntry, err error) error {
		skill, rel, ok := strings.Cut(p, "/")
		if err != nil || d.IsDir() || !ok || !strings.HasPrefix(skill, "rota-") || !skills.SupportedFile(rel) {
			return nil
		}
		n++
		if _, err := fs.Stat(rota.FS, "skills/"+p); err != nil {
			t.Errorf("%s is supported but not embedded: %v", p, err)
		}
		if _, err := os.Stat(filepath.Join(dst, "plugins/rota/skills", filepath.FromSlash(p))); err != nil {
			t.Errorf("%s is supported but missing from the Codex tree: %v", p, err)
		}
		return nil
	})
	if n == 0 {
		t.Fatal("no supported skill files found, the check proves nothing")
	}
}
