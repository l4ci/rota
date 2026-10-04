package initproj

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCheck(t *testing.T) {
	dir := t.TempDir()
	if r := Check(dir, nil); r.Initialized || !reflect.DeepEqual(r.Missing, []string{".rota"}) {
		t.Errorf("no .rota: %+v", r)
	}
	if _, err := Init(dir); err != nil {
		t.Fatal(err)
	}
	if r := Check(dir, nil); !r.Initialized || len(r.Missing) != 0 || len(r.Warnings) != 0 {
		t.Errorf("initialized: %+v", r)
	}
	os.Remove(filepath.Join(dir, ".rota", "counters.json"))
	os.Remove(filepath.Join(dir, ".rota", "DECISIONS.md"))
	r := Check(dir, nil)
	want := []string{".rota/DECISIONS.md", ".rota/counters.json"}
	if r.Initialized || !reflect.DeepEqual(r.Missing, want) {
		t.Errorf("every missing path is reported: %+v", r)
	}
}

func TestCheckWarnings(t *testing.T) {
	dir := t.TempDir()
	if _, err := Init(dir); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(dir, ".rota", "config.json")
	os.WriteFile(cfg, []byte(`{"umbrella":{"enabled":true}}`), 0o644)
	r := Check(dir, func() string { return "rota drift: x" })
	if !r.Initialized || len(r.Warnings) != 2 || !strings.Contains(r.Warnings[0], "no sub-repos") || r.Warnings[1] != "rota drift: x" {
		t.Errorf("%+v", r)
	}
	os.Remove(filepath.Join(dir, ".rota", "repos.json"))
	if r := Check(dir, nil); len(r.Warnings) != 1 || !strings.Contains(r.Warnings[0], "repos.json missing") {
		t.Errorf("%+v", r)
	}
	// the registry is the truth: registered repos silence the flag warning
	os.WriteFile(filepath.Join(dir, ".rota", "repos.json"), []byte(`{"repos":[{"name":"a","path":"a"}]}`), 0o644)
	if r := Check(dir, nil); len(r.Warnings) != 0 {
		t.Errorf("%+v", r)
	}
	// a flag of false warns nothing
	os.WriteFile(cfg, []byte(`{"umbrella":{"enabled":false}}`), 0o644)
	os.WriteFile(filepath.Join(dir, ".rota", "repos.json"), []byte(`{"repos":[]}`), 0o644)
	if r := Check(dir, nil); len(r.Warnings) != 0 {
		t.Errorf("%+v", r)
	}
}

func TestBlocksWriteSixAndRerunUnchanged(t *testing.T) {
	dir := t.TempDir()
	if _, err := Init(dir); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("# mine\n\n<!-- rota-context-start -->\nold\n<!-- rota-context-end -->\n"), 0o644)
	b := Blocks(dir, nil)
	if len(b.Warnings) != 0 {
		t.Fatalf("warnings: %v", b.Warnings)
	}
	var keys []string
	for _, e := range b.Blocks {
		keys = append(keys, e.Key+":"+e.Status)
	}
	if got := strings.Join(keys, " "); !strings.HasPrefix(got, "context:removed skills:") || len(b.Blocks) != 7 {
		t.Errorf("blocks %s", got)
	}
	if !b.Changed() {
		t.Error("first run reported no change")
	}
	agents, _ := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if strings.Contains(string(agents), "hv-context") {
		t.Errorf("AGENTS.md keeps the stripped block:\n%s", agents)
	}
	again := Blocks(dir, nil)
	if again.Changed() || len(again.Blocks) != 6 {
		t.Errorf("rerun: %+v", again)
	}
}

func TestBlocksFailureDoesNotStopTheRest(t *testing.T) {
	dir := t.TempDir()
	if _, err := Init(dir); err != nil {
		t.Fatal(err)
	}
	b := Blocks(dir, func() (bool, error) { return false, os.ErrPermission })
	if len(b.Warnings) != 1 || !strings.Contains(b.Warnings[0], "milestones") {
		t.Fatalf("warnings %v", b.Warnings)
	}
	var failed, rest int
	for _, e := range b.Blocks {
		if e.Status == "failed" && e.Key == "milestones" && !e.Changed {
			failed++
		} else {
			rest++
		}
	}
	if failed != 1 || rest != 5 {
		t.Errorf("blocks %+v", b.Blocks)
	}
}
