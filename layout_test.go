package rota_test

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	rota "github.com/l4ci/rota"
	"github.com/l4ci/rota/internal/skills"
)

// The supported skill-file layout is owned by skills.SupportedFile. The binary
// (embed.go + Load) selects files by it directly; the validator and the Codex
// builder ask `rota skills layout`, which is backed by the same rule. These
// fixtures cross that one seam with a good and a bad tree and check that every
// packaging path ships or rejects the same files.

// rotaBin builds the binary once; the validator and the Codex builder take it
// through ROTA_BIN instead of each building their own.
var rotaBin = sync.OnceValues(func() (string, error) {
	dir, err := os.MkdirTemp("", "layout-rota-")
	if err != nil {
		return "", err
	}
	layoutCleanup = append(layoutCleanup, dir)
	bin := filepath.Join(dir, "rota")
	if out, err := exec.Command("go", "build", "-o", bin, "./cmd/rota").CombinedOutput(); err != nil {
		return "", fmt.Errorf("go build: %v\n%s", err, out)
	}
	return bin, nil
})

var layoutCleanup []string

func TestMain(m *testing.M) {
	code := m.Run()
	for _, d := range layoutCleanup {
		os.RemoveAll(d)
	}
	os.Exit(code)
}

func rotaEnv(t *testing.T) []string {
	t.Helper()
	bin, err := rotaBin()
	if err != nil {
		t.Fatal(err)
	}
	return append(os.Environ(), "ROTA_BIN="+bin)
}

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
	binary    []string
	codex     []string
	validator string // stderr, empty when accepted
	codexErr  string
}

func runLayout(t *testing.T, root string) layoutVerdicts {
	t.Helper()
	var v layoutVerdicts
	skillsFS := os.DirFS(filepath.Join(root, "skills"))
	set, err := skills.Load(skillsFS)
	if err != nil {
		t.Fatal(err)
	}
	v.binary = set.Paths()

	env := rotaEnv(t)
	cmd := exec.Command("python3", "test/validate-skills.py", "--layout", filepath.Join(root, "skills"))
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		v.validator = string(out)
	}

	dst := t.TempDir()
	cmd = exec.Command("bash", filepath.Join(root, "scripts/codex-plugin.sh"), dst)
	cmd.Env = env
	out, err = cmd.CombinedOutput()
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
	if v.validator != "" || v.codexErr != "" {
		t.Fatalf("supported layout rejected: validator %q codex %q", v.validator, v.codexErr)
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

// A symlinked skill folder is followed by the Codex builder's glob but not by
// a directory walk, so it would ship files from outside skills/. The owner
// rejects the link itself.
func TestSkillLayoutSymlinkedSkillRejected(t *testing.T) {
	root := layoutFixture(t)
	put(t, root, "ext/notes.txt", "outside\n")
	put(t, root, "ext/sub/deep.md", "deep\n")
	if err := os.Symlink("../ext", filepath.Join(root, "skills/rota-b")); err != nil {
		t.Fatal(err)
	}
	v := runLayout(t, root)
	if !strings.Contains(v.validator, "skills/rota-b: unsupported skill content") {
		t.Errorf("validator accepted a symlinked skill folder: %q", v.validator)
	}
	if v.codexErr == "" {
		t.Errorf("Codex builder shipped a symlinked skill folder: %v", v.codex)
	}
}

// The real tree: nothing the owner rejects, and every file it accepts reaches
// the embedded FS (embed.go's patterns) and the Codex build.
func TestSkillLayoutRealTree(t *testing.T) {
	skillsFS := os.DirFS("skills")
	dst := t.TempDir()
	cmd := exec.Command("bash", "scripts/codex-plugin.sh", dst)
	cmd.Env = rotaEnv(t)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("codex-plugin.sh rejected the real tree: %v\n%s", err, b)
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
