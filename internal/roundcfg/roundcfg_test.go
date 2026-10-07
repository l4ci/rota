package roundcfg

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/harness"
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

func TestDefaults(t *testing.T) {
	s, err := Load(project(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	if s.Scope != ScopeMilestone || !reflect.DeepEqual(s.Roster, []string{"ben", "dana", "nia", "kit"}) || s.Brief != "" || len(s.SharedPaths) != 0 {
		t.Fatalf("%+v", s)
	}
}

func TestOverrides(t *testing.T) {
	s, err := Load(project(t, `{"round":{"scope":"slate","roster":["ann","bo"],"brief":"docs/b.md","sharedPaths":["docs/*.md"]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if s.Scope != "slate" || !reflect.DeepEqual(s.Roster, []string{"ann", "bo"}) || s.Brief != "docs/b.md" || !reflect.DeepEqual(s.SharedPaths, []string{"docs/*.md"}) {
		t.Fatalf("%+v", s)
	}
}

func TestInvalid(t *testing.T) {
	for cfg, want := range map[string]string{
		`{"round":{"scope":"all"}}`:      "round.scope",
		`{"round":{"roster":["a","a"]}}`: "twice",
		`{"round":{"roster":["Ann"]}}`:   "branch",
		`{"round":{"roster":["a/b"]}}`:   "branch",
		`{"round":{"roster":[]}}`:        "empty",
		`{"round":{"roster":"ben"}}`:     "list",
		`{"round":{"sharedPaths":[1]}}`:  "list of strings",
		`{"round":{"stallMinutes":-1}}`:  "stallMinutes",
		`{"round":{"stallMinutes":"x"}}`: "stallMinutes",
		`{"round":{"maxBounces":-1}}`:    "maxBounces",
		`{"round":{"maxBounces":"x"}}`:   "maxBounces",
	} {
		_, err := Load(project(t, cfg))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: want %q, got %v", cfg, want, err)
		}
	}
}

func TestTierDefaults(t *testing.T) {
	s, err := Load(project(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	if s.Tier != TierStandard {
		t.Errorf("default tier %q", s.Tier)
	}
	want := map[string]string{"light": "haiku", "standard": "sonnet", "heavy": "opus"}
	if !reflect.DeepEqual(s.Models[harness.Claude], want) {
		t.Errorf("claude map %v", s.Models[harness.Claude])
	}
	if _, ok := s.Models[harness.Codex]; ok || s.Model(harness.Codex, TierLight) != "" {
		t.Errorf("codex is unconfigured by default: %v", s.Models)
	}
}

func TestStandardTierFollowsModelsWorkerUnlessExplicit(t *testing.T) {
	s, err := Load(project(t, `{"models":{"worker":"opus"}}`))
	if err != nil || s.Model(harness.Claude, TierStandard) != "opus" {
		t.Fatalf("one knob: %v %v", err, s.Models)
	}
	s, err = Load(project(t, `{"models":{"worker":"opus"},"round":{"tiers":{"claude":{"standard":"sonnet"}}}}`))
	if err != nil || s.Model(harness.Claude, TierStandard) != "sonnet" {
		t.Fatalf("explicit wins: %v %v", err, s.Models)
	}
}

func TestCodexMapNeedsEveryTier(t *testing.T) {
	s, err := Load(project(t, `{"round":{"tiers":{"codex":{"light":"a","standard":"b","heavy":"c"}}}}`))
	if err != nil || s.Model(harness.Codex, TierHeavy) != "c" {
		t.Fatalf("a full codex map: %v %v", err, s.Models)
	}
	_, err = Load(project(t, `{"round":{"tiers":{"codex":{"light":"a","heavy":"c"}}}}`))
	if err == nil || !strings.Contains(err.Error(), "round.tiers.codex.standard") {
		t.Fatalf("a partial map names the missing tier: %v", err)
	}
}

func TestInvalidTierConfig(t *testing.T) {
	for cfg, want := range map[string]string{
		`{"round":{"tier":"ultra"}}`:                   "round.tier",
		`{"round":{"tiers":{"claude":{"heavy":""}}}}`:  "round.tiers.claude.heavy",
		`{"round":{"tiers":{"claude":{"light":" "}}}}`: "round.tiers.claude.light",
	} {
		if _, err := Load(project(t, cfg)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: want %q, got %v", cfg, want, err)
		}
	}
}

func TestTierRank(t *testing.T) {
	if !(TierRank(TierLight) < TierRank(TierStandard) && TierRank(TierStandard) < TierRank(TierHeavy)) || TierRank("x") != -1 {
		t.Fatal("tiers order light < standard < heavy")
	}
}

func TestStallMinutes(t *testing.T) {
	if s, err := Load(project(t, "")); err != nil || s.StallMinutes != 30 {
		t.Fatalf("default is 30: %v %+v", err, s)
	}
	if s, err := Load(project(t, `{"round":{"stallMinutes":0}}`)); err != nil || s.StallMinutes != 0 {
		t.Fatalf("0 turns it off: %v %+v", err, s)
	}
	if s, err := Load(project(t, `{"round":{"stallMinutes":5}}`)); err != nil || s.StallMinutes != 5 {
		t.Fatalf("%v %+v", err, s)
	}
}

func TestMaxBounces(t *testing.T) {
	if s, err := Load(project(t, "")); err != nil || s.MaxBounces != 3 {
		t.Fatalf("default is 3: %v %+v", err, s)
	}
	if s, err := Load(project(t, `{"round":{"maxBounces":0}}`)); err != nil || s.MaxBounces != 0 {
		t.Fatalf("0 turns the cap off: %v %+v", err, s)
	}
	if s, err := Load(project(t, `{"round":{"maxBounces":5}}`)); err != nil || s.MaxBounces != 5 {
		t.Fatalf("%v %+v", err, s)
	}
}

func TestArchitectureKeys(t *testing.T) {
	s, err := Load(project(t, `{}`))
	if err != nil || s.ArchitectureEvery != 20 || len(s.ArchitectureAreas) != 0 {
		t.Fatalf("defaults: %+v %v", s, err)
	}
	s, err = Load(project(t, `{"round":{"architectureEvery":0,"architectureAreas":["cli","worker"]}}`))
	if err != nil || s.ArchitectureEvery != 0 || len(s.ArchitectureAreas) != 2 {
		t.Fatalf("overrides: %+v %v", s, err)
	}
	for _, bad := range []string{`{"round":{"architectureEvery":-1}}`, `{"round":{"architectureEvery":"x"}}`, `{"round":{"architectureAreas":"cli"}}`} {
		if _, err := Load(project(t, bad)); err == nil {
			t.Errorf("%s should be refused", bad)
		}
	}
}

func TestAutopilotKeys(t *testing.T) {
	s, err := Load(project(t, `{}`))
	if err != nil || s.Autopilot || s.AutopilotCap != 3 {
		t.Fatalf("defaults: %+v %v", s, err)
	}
	s, err = Load(project(t, `{"round":{"autopilot":true,"autopilotCap":1}}`))
	if err != nil || !s.Autopilot || s.AutopilotCap != 1 {
		t.Fatalf("overrides: %+v %v", s, err)
	}
	for _, bad := range []string{`{"round":{"autopilot":"yes"}}`, `{"round":{"autopilotCap":-1}}`} {
		if _, err := Load(project(t, bad)); err == nil {
			t.Errorf("%s should be refused", bad)
		}
	}
}

func TestItemTimeoutMinutes(t *testing.T) {
	if set, err := Load(project(t, `{}`)); err != nil || set.ItemTimeoutMinutes != 0 {
		t.Fatalf("default is off: %d %v", set.ItemTimeoutMinutes, err)
	}
	if set, err := Load(project(t, `{"work":{"itemTimeoutMinutes":90}}`)); err != nil || set.ItemTimeoutMinutes != 90 {
		t.Fatalf("set: %d %v", set.ItemTimeoutMinutes, err)
	}
	if _, err := Load(project(t, `{"work":{"itemTimeoutMinutes":-1}}`)); err == nil {
		t.Fatal("a negative limit must be refused")
	}
}

func TestWorkerKind(t *testing.T) {
	if s, err := Load(project(t, "")); err != nil || s.WorkerKind != "" {
		t.Fatalf("unset is empty: %v %q", err, s.WorkerKind)
	}
	for _, k := range []string{"claude", "codex"} {
		if s, err := Load(project(t, `{"round":{"workerKind":"`+k+`"}}`)); err != nil || s.WorkerKind != k {
			t.Fatalf("%s: %v %q", k, err, s.WorkerKind)
		}
	}
	for _, cfg := range []string{`{"round":{"workerKind":"gemini"}}`, `{"round":{"workerKind":7}}`} {
		if _, err := Load(project(t, cfg)); err == nil || !strings.Contains(err.Error(), "round.workerKind") {
			t.Errorf("%s: want a round.workerKind error, got %v", cfg, err)
		}
	}
}

func TestRoleDefaultsAndOverrides(t *testing.T) {
	s, err := Load(project(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]Role{"explorer": {"light", ""}, "implementer": {"standard", ""}, "reasoner": {"heavy", ""}}
	if !reflect.DeepEqual(s.Roles, want) {
		t.Fatalf("%+v", s.Roles)
	}
	s, err = Load(project(t, `{"roles":{"explorer":{"tier":"standard","effort":"low"}}}`))
	if err != nil || s.Roles["explorer"] != (Role{"standard", "low"}) {
		t.Fatalf("%+v %v", s.Roles, err)
	}
}

func TestRoleBadValues(t *testing.T) {
	for cfg, want := range map[string]string{
		`{"roles":{"explorer":{"tier":"giant"}}}`:   "roles.explorer.tier",
		`{"roles":{"reasoner":{"effort":"ultra"}}}`: "roles.reasoner.effort",
		`{"roles":{"implementer":{"effort":3}}}`:    "roles.implementer.effort",
	} {
		if _, err := Load(project(t, cfg)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", cfg, err)
		}
	}
}
