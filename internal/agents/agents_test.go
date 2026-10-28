package agents

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/golden"
	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/projects"
	"github.com/l4ci/rota/internal/rotatree"
)

func project(t *testing.T, cfg string) string {
	t.Helper()
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".rota"), 0o755)
	if cfg != "" {
		os.WriteFile(filepath.Join(root, ".rota", "config.json"), []byte(cfg), 0o644)
	}
	return root
}

// load is the spec of the project config cfg, read as the cli does.
func load(t *testing.T, root string) (Spec, error) {
	t.Helper()
	return FromConfig(config.Load(projects.OSEnv(), rotatree.Config(root)))
}

func render(t *testing.T, cfg string) map[string]string {
	t.Helper()
	sp, err := load(t, project(t, cfg))
	if err != nil {
		t.Fatal(err)
	}
	files, err := Render(sp)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, f := range files {
		out[f.Path] = f.Content
	}
	return out
}

func TestRenderClaudeOnly(t *testing.T) {
	cfg := ``
	got := render(t, cfg)
	for p := range got {
		if strings.HasPrefix(p, ".codex/") {
			t.Fatalf("codex file without codex config: %s", p)
		}
	}
	if len(got) != 3 {
		t.Fatalf("want 3 files, got %d", len(got))
	}
	golden.Check(t, cfg, got)
}

func TestRenderWithCodexAndEffort(t *testing.T) {
	cfg := `{"round":{"tiers":{"codex":{"light":"gpt-lo","standard":"gpt-mid","heavy":"gpt-hi"}}},"roles":{"reasoner":{"effort":"xhigh"},"implementer":{"effort":"medium"}}}`
	got := render(t, cfg)
	if len(got) != 6 {
		t.Fatalf("want 6 files, got %d", len(got))
	}
	golden.Check(t, cfg, got)
}

func TestCodexWithoutTierMapOmitsModel(t *testing.T) {
	got := render(t, `{"round":{"commands":{"codex":"codex"},"tiers":{"codex":{"light":"","standard":"","heavy":""}}}}`)
	c := got[".codex/agents/rota-implementer.toml"]
	if c == "" || strings.Contains(c, "\nmodel = ") {
		t.Fatalf("want a codex file without model:\n%s", c)
	}
}

func TestCodexRefusesEffortItCannotExpress(t *testing.T) {
	sp, err := load(t, project(t, `{"round":{"commands":{"codex":"codex"}},"roles":{"reasoner":{"effort":"max"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	_, err = Render(sp)
	if !errors.Is(err, ErrEffort) || !strings.Contains(err.Error(), "roles.reasoner.effort") || !strings.Contains(err.Error(), `"max"`) {
		t.Fatalf("got %v", err)
	}
	// Claude alone accepts max.
	if _, err := Render(Spec{Roles: sp.Roles, Kinds: []string{harness.Claude}}); err != nil {
		t.Fatal(err)
	}
}

func TestWriteIdempotentAndCheck(t *testing.T) {
	root := project(t, "")
	es, err := Write(root, config.Load(projects.OSEnv(), rotatree.Config(root)))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range es {
		if e.Status != StatusMissing {
			t.Fatalf("first write: %s %s", e.Path, e.Status)
		}
	}
	es, err = Write(root, config.Load(projects.OSEnv(), rotatree.Config(root)))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range es {
		if e.Status != StatusCurrent {
			t.Fatalf("second write: %s %s", e.Path, e.Status)
		}
	}
	if p := Problems(root, config.Load(projects.OSEnv(), rotatree.Config(root))); len(p) != 0 {
		t.Fatalf("problems after write: %v", p)
	}
	// Config change makes the files drift; Compare writes nothing.
	os.WriteFile(filepath.Join(root, ".rota", "config.json"), []byte(`{"roles":{"explorer":{"effort":"low"}}}`), 0o644)
	p := Problems(root, config.Load(projects.OSEnv(), rotatree.Config(root)))
	if len(p) != 1 || !strings.Contains(p[0], "rota-explorer.md: drift") {
		t.Fatalf("problems: %v", p)
	}
	b, _ := os.ReadFile(filepath.Join(root, ".claude/agents/rota-explorer.md"))
	if strings.Contains(string(b), "effort") {
		t.Fatal("Compare wrote")
	}
	if _, err := Write(root, config.Load(projects.OSEnv(), rotatree.Config(root))); err != nil {
		t.Fatal(err)
	}
	if p := Problems(root, config.Load(projects.OSEnv(), rotatree.Config(root))); len(p) != 0 {
		t.Fatalf("problems after rewrite: %v", p)
	}
}

func TestWriteRefusesForeignFile(t *testing.T) {
	root := project(t, "")
	foreign := filepath.Join(root, ".claude/agents/rota-reasoner.md")
	os.MkdirAll(filepath.Dir(foreign), 0o755)
	os.WriteFile(foreign, []byte("mine\n"), 0o644)
	_, err := Write(root, config.Load(projects.OSEnv(), rotatree.Config(root)))
	if !errors.Is(err, ErrForeign) || !strings.Contains(err.Error(), "rota-reasoner.md") {
		t.Fatalf("got %v", err)
	}
	if b, _ := os.ReadFile(foreign); string(b) != "mine\n" {
		t.Fatal("foreign file overwritten")
	}
	if _, err := os.Stat(filepath.Join(root, ".claude/agents/rota-explorer.md")); err == nil {
		t.Fatal("wrote other files despite refusal")
	}
}

func TestBadRoleConfig(t *testing.T) {
	for cfg, want := range map[string]string{
		`{"roles":{"explorer":{"tier":"giant"}}}`:  "roles.explorer.tier",
		`{"roles":{"reasoner":{"effort":"huge"}}}`: "roles.reasoner.effort",
	} {
		if _, err := load(t, project(t, cfg)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v", cfg, err)
		}
	}
}

func TestWriteRefusesSymlinkedDir(t *testing.T) {
	root := project(t, "")
	outside := t.TempDir()
	os.Symlink(outside, filepath.Join(root, ".claude"))
	_, err := Write(root, config.Load(projects.OSEnv(), rotatree.Config(root)))
	if !errors.Is(err, ErrForeign) || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("got %v", err)
	}
	if ents, _ := os.ReadDir(outside); len(ents) != 0 {
		t.Fatalf("wrote through the link: %v", ents)
	}
}

// FromConfig renders from a config tree already in hand: no project on disk.
func TestFromConfigNeedsNoOnDiskConfig(t *testing.T) {
	cfg, err := jsonx.Decode([]byte(`{"roles":{"reasoner":{"effort":"xhigh"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	sp, err := FromConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var effort string
	for _, r := range sp.Roles {
		if r.Name == "reasoner" {
			effort = r.Effort
		}
	}
	if effort != "xhigh" || len(sp.Kinds) == 0 {
		t.Fatalf("effort %q kinds %v", effort, sp.Kinds)
	}
}

func TestProblemsOnlyListsKeptPaths(t *testing.T) {
	cfg := `{"round":{"tiers":{"codex":{"light":"gpt-lo","standard":"gpt-mid","heavy":"gpt-hi"}}}}`
	root := project(t, cfg)
	c := config.Load(projects.OSEnv(), rotatree.Config(root))
	all := Problems(root, c)
	if len(all) != 6 {
		t.Fatalf("unrestricted problems: %v", all)
	}
	got := ProblemsOnly(root, c, Untracked)
	if len(got) != 3 {
		t.Fatalf("want the 3 .codex files, got %v", got)
	}
	for _, p := range got {
		if !strings.HasPrefix(p, ".codex/") {
			t.Errorf("tracked path listed: %s", p)
		}
	}
}
