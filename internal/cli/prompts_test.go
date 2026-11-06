package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/prompts"
)

func promptCommand(t *testing.T, dir string, args ...string) string {
	t.Helper()
	code, out, stderr := rotaIn(t, dir, args...)
	if code != 0 {
		t.Fatalf("%v: expected success, got exit %d: %s %s", args, code, out, stderr)
	}
	return out
}

func TestPromptsSaveActivate(t *testing.T) {
	dir := agentsProject(t, `{}`)
	draft := filepath.Join(t.TempDir(), "draft.md")
	body := "Team report: use short bullets. {{status}} {{closing}}"
	if err := os.WriteFile(draft, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	promptCommand(t, dir, "agents", "write")
	path := filepath.Join(dir, ".claude/agents/rota-explorer.md")
	before, _ := os.ReadFile(path)
	promptCommand(t, dir, "prompts", "save", "agents.explorer.report", "--expect", "0", "--body-file", draft)
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Fatal("save activated the override")
	}
	shown := promptCommand(t, dir, "prompts", "show", "agents.explorer.report")
	if !strings.Contains(shown, "Team report") || !strings.Contains(shown, "revision") {
		t.Fatalf("missing effective content/revision: %s", shown)
	}
	promptCommand(t, dir, "agents", "write")
	after, _ := os.ReadFile(path)
	for _, want := range []string{"Team report", "never edit or write files", "done.", "Not found:"} {
		if !strings.Contains(string(after), want) {
			t.Errorf("generated agent lacks %q: %s", want, after)
		}
	}
	if strings.Contains(string(after), "{{") {
		t.Fatal("unexpanded required placeholder")
	}
	if code, _, errOut := initRun(t, dir, "init"); code != 0 {
		t.Fatalf("init: %d %s", code, errOut)
	}
	if initialized, _ := os.ReadFile(path); string(initialized) != string(after) {
		t.Fatal("init replaced explicitly activated override")
	}
}

func TestPromptsConflictRestore(t *testing.T) {
	dir := agentsProject(t, `{}`)
	draft := filepath.Join(t.TempDir(), "draft.md")
	body := "Custom report. {{status}} {{closing}}"
	if err := os.WriteFile(draft, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	promptCommand(t, dir, "prompts", "save", "agents.explorer.report", "--expect", "0", "--body-file", draft)
	store := filepath.Join(dir, ".rota/prompts.json")
	before, _ := os.ReadFile(store)
	code, _, stderr := rotaIn(t, dir, "prompts", "save", "agents.explorer.report", "--expect", "0", "--body-file", draft)
	if code != 4 || !strings.Contains(stderr, "revision") {
		t.Fatalf("stale save: %d %s", code, stderr)
	}
	if after, _ := os.ReadFile(store); string(after) != string(before) {
		t.Fatal("conflict changed store")
	}
	if after, _ := os.ReadFile(draft); string(after) != body {
		t.Fatal("conflict discarded draft")
	}
	promptCommand(t, dir, "prompts", "adopt", "agents.explorer.report", "--expect", "1")
	promptCommand(t, dir, "prompts", "restore", "agents.explorer.report", "--expect", "2", "--revision", "1")
	if out := promptCommand(t, dir, "prompts", "show", "agents.explorer.report"); !strings.Contains(out, "Custom report.") {
		t.Fatalf("restore lost custom text: %s", out)
	}
}

// TestPromptsSkillsUpdateReportsUpstream: a skills update in a project with
// saved prompts names each section whose built-in default changed or was
// removed since its revision, and leaves the store and agent files alone.
func TestPromptsSkillsUpdateReportsUpstream(t *testing.T) {
	home := t.TempDir()
	proj, _ := filepath.EvalSymlinks(t.TempDir())
	gittest.Init(t, proj, "main")
	if code, _, errOut := skillsRun(t, home, proj, "skills", "install", "--agent", "claude", "--scope", "project"); code != 0 {
		t.Fatalf("install: %d %s", code, errOut)
	}
	sum := func(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
	cur := prompts.Catalog()["agents.implementer.report"]
	current := cur.Report(cur.Default)
	store := fmt.Sprintf(`{"version":1,"sections":{
"agents.explorer.report":[{"revision":1,"baseline":"old explorer","baselineDigest":%q,"override":"Team {{status}} {{closing}}"}],
"agents.implementer.report":[{"revision":1,"baseline":%q,"baselineDigest":%q,"override":"Team {{status}} {{closing}}"}],
"agents.retired.report":[{"revision":1,"baseline":"gone","baselineDigest":%q,"override":null}]}}`,
		sum("old explorer"), current, sum(current), sum("gone"))
	path := filepath.Join(proj, ".rota", "prompts.json")
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(store), 0o644); err != nil {
		t.Fatal(err)
	}
	code, env, errOut := skillsRun(t, home, proj, "skills", "update", "--agent", "claude", "--scope", "project")
	if code != 0 {
		t.Fatalf("update: %d %s", code, errOut)
	}
	got, _ := json.Marshal(skData(env)["prompts"])
	want := `[{"name":"agents.explorer.report","revision":1,"status":"changed"},{"name":"agents.retired.report","revision":1,"status":"removed"}]`
	if string(got) != want {
		t.Fatalf("prompts report: got %s, want %s", got, want)
	}
	if after, _ := os.ReadFile(path); string(after) != store {
		t.Fatal("skills update changed the prompt store")
	}
	if _, err := os.Stat(filepath.Join(proj, ".claude", "agents")); err == nil {
		t.Fatal("skills update activated agent files")
	}
}
