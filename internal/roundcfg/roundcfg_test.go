package roundcfg

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/harness"
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
		`{"work":{"tdd":"yes"}}`:         "work.tdd",
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
	for kind, want := range map[string]map[string]string{
		harness.Codex:    {"light": "gpt-6-luna", "standard": "gpt-6.1-sol", "heavy": "gpt-6-astra"},
		harness.OpenCode: {"light": "opencode/claude-haiku-5-5", "standard": "opencode/claude-sonnet-5-5", "heavy": "opencode/claude-opus-5-5"},
	} {
		if !reflect.DeepEqual(s.Models[kind], want) {
			t.Errorf("%s ships a light-to-heavy default: %v", kind, s.Models[kind])
		}
	}
}

func TestNonClaudeTierOverrides(t *testing.T) {
	// One explicit tier wins and the other two stay on their defaults.
	s, err := Load(project(t, `{"round":{"tiers":{"codex":{"heavy":"x"}}}}`))
	if err != nil || s.Model(harness.Codex, TierHeavy) != "x" || s.Model(harness.Codex, TierLight) != "gpt-6-luna" {
		t.Fatalf("one explicit tier: %v %v", err, s.Models[harness.Codex])
	}
	// All three empty means the harness default, no --model.
	s, err = Load(project(t, `{"round":{"tiers":{"opencode":{"light":"","standard":"","heavy":""}}}}`))
	if err != nil || s.Model(harness.OpenCode, TierLight) != "" || s.Model(harness.OpenCode, TierHeavy) != "" {
		t.Fatalf("explicit empty is the harness default: %v %v", err, s.Models[harness.OpenCode])
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
	_, err = Load(project(t, `{"round":{"tiers":{"codex":{"light":"a","standard":"","heavy":"c"}}}}`))
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
	if err != nil || s.ArchitectureEvery != 20 || len(s.ArchitectureAreas) != 0 || s.ArchitectureIdleMinItems != 5 || s.ArchitectureIdleMinHours != 24 {
		t.Fatalf("defaults: %+v %v", s, err)
	}
	s, err = Load(project(t, `{"round":{"architectureEvery":0,"architectureAreas":["cli","worker"]}}`))
	if err != nil || s.ArchitectureEvery != 0 || len(s.ArchitectureAreas) != 2 {
		t.Fatalf("overrides: %+v %v", s, err)
	}
	for _, bad := range []string{`{"round":{"architectureEvery":-1}}`, `{"round":{"architectureEvery":"x"}}`, `{"round":{"architectureAreas":"cli"}}`, `{"round":{"architectureIdleMinItems":-1}}`, `{"round":{"architectureIdleMinHours":"x"}}`} {
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
	s, err = Load(project(t, `{"autonomy":{"level":"auto"},"round":{"autopilotCap":1}}`))
	if err != nil || !s.Autopilot || s.AutopilotCap != 1 {
		t.Fatalf("overrides: %+v %v", s, err)
	}
	for _, bad := range []string{`{"round":{"autopilotCap":-1}}`} {
		if _, err := Load(project(t, bad)); err == nil {
			t.Errorf("%s should be refused", bad)
		}
	}
}

func TestBatchKey(t *testing.T) {
	s, err := Load(project(t, `{}`))
	if err != nil || s.Batch != 0 {
		t.Fatalf("default is no limit: %d %v", s.Batch, err)
	}
	s, err = Load(project(t, `{"round":{"batch":2}}`))
	if err != nil || s.Batch != 2 {
		t.Fatalf("override: %d %v", s.Batch, err)
	}
	for _, bad := range []string{`{"round":{"batch":-1}}`, `{"round":{"batch":"x"}}`} {
		if _, err := Load(project(t, bad)); err == nil {
			t.Errorf("%s should be refused", bad)
		}
	}
}

func TestItemTimeoutMinutes(t *testing.T) {
	if set, err := Load(project(t, `{}`)); err != nil || set.ItemTimeoutMinutes != 0 {
		t.Fatalf("default is off: %d %v", set.ItemTimeoutMinutes, err)
	}
	if set, err := Load(project(t, `{"round":{"itemTimeoutMinutes":90}}`)); err != nil || set.ItemTimeoutMinutes != 90 {
		t.Fatalf("set: %d %v", set.ItemTimeoutMinutes, err)
	}
	if _, err := Load(project(t, `{"round":{"itemTimeoutMinutes":-1}}`)); err == nil {
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

func TestScopeOverlap(t *testing.T) {
	s, err := Load(project(t, ""))
	if err != nil || s.ScopeOverlap != "warn" {
		t.Fatalf("default: %v %+v", err, s)
	}
	s, err = Load(project(t, `{"round":{"scopeOverlap":"block"}}`))
	if err != nil || s.ScopeOverlap != "block" {
		t.Fatalf("block: %v %+v", err, s)
	}
	if _, err := Load(project(t, `{"round":{"scopeOverlap":"off"}}`)); err == nil || !strings.Contains(err.Error(), "scopeOverlap") {
		t.Errorf("invalid value: %v", err)
	}
}

func TestReviewLoop(t *testing.T) {
	if s, err := Load(project(t, "")); err != nil || s.ReviewLoop != ReviewLoopManual {
		t.Fatalf("default is manual: %v %+v", err, s)
	}
	if s, err := Load(project(t, `{"round":{"reviewLoop":"auto"}}`)); err != nil || s.ReviewLoop != ReviewLoopAuto {
		t.Fatalf("%v %+v", err, s)
	}
	for _, bad := range []string{`{"round":{"reviewLoop":"always"}}`, `{"round":{"reviewLoop":true}}`} {
		if _, err := Load(project(t, bad)); err == nil {
			t.Errorf("%s should be refused", bad)
		}
	}
}

func TestProfileFillsClaudeTiers(t *testing.T) {
	for profile, want := range map[string]map[string]string{
		"quality":  {"light": "sonnet", "standard": "opus", "heavy": "opus"},
		"balanced": {"light": "haiku", "standard": "sonnet", "heavy": "opus"},
		"budget":   {"light": "haiku", "standard": "haiku", "heavy": "sonnet"},
		"inherit":  {"light": "", "standard": "", "heavy": ""},
	} {
		s, err := Load(project(t, `{"models":{"worker":"opus"},"round":{"profile":"`+profile+`"}}`))
		if err != nil {
			t.Fatalf("%s: %v", profile, err)
		}
		if !reflect.DeepEqual(s.Models[harness.Claude], want) {
			t.Errorf("%s: claude map %v, want %v", profile, s.Models[harness.Claude], want)
		}
		if s.Profile != profile {
			t.Errorf("%s: Profile %q", profile, s.Profile)
		}
	}
}

func TestProfileExplicitTierWins(t *testing.T) {
	s, err := Load(project(t, `{"round":{"profile":"budget","tiers":{"claude":{"heavy":"opus"}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"light": "haiku", "standard": "haiku", "heavy": "opus"}
	if !reflect.DeepEqual(s.Models[harness.Claude], want) {
		t.Errorf("claude map %v, want %v", s.Models[harness.Claude], want)
	}
}

func TestProfileLeavesCodexAndOpenCodeAlone(t *testing.T) {
	s, err := Load(project(t, `{"round":{"profile":"inherit","tiers":{"codex":{"light":"a","standard":"b","heavy":"c"}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Models[harness.Codex]; got["light"] != "a" || got["heavy"] != "c" {
		t.Errorf("codex map touched: %v", got)
	}
	if got := s.Models[harness.OpenCode]["heavy"]; got != "opencode/claude-opus-5-5" {
		t.Errorf("opencode must stay on its defaults: %v", s.Models)
	}
}

func TestInvalidProfile(t *testing.T) {
	if _, err := Load(project(t, `{"round":{"profile":"adaptive"}}`)); err == nil || !strings.Contains(err.Error(), "round.profile") {
		t.Fatalf("want round.profile error, got %v", err)
	}
}

func TestEffectiveModelAndSource(t *testing.T) {
	type row struct{ model, source string }
	cases := []struct {
		name, cfg, kind, tier string
		want                  row
	}{
		{"default light", ``, "claude", "light", row{"haiku", "default"}},
		{"standard follows models.worker", `{"models":{"worker":"opus"}}`, "claude", "standard", row{"opus", "models.worker"}},
		{"explicit", `{"round":{"tiers":{"claude":{"heavy":"fable"}}}}`, "claude", "heavy", row{"fable", "explicit"}},
		{"profile", `{"round":{"profile":"budget"}}`, "claude", "heavy", row{"sonnet", "profile"}},
		{"explicit beats profile", `{"round":{"profile":"budget","tiers":{"claude":{"light":"opus"}}}}`, "claude", "light", row{"opus", "explicit"}},
		{"inherit is harness default", `{"round":{"profile":"inherit"}}`, "claude", "standard", row{"", "harness default"}},
		{"codex unset", ``, "codex", "light", row{"gpt-6-luna", "default"}},
		{"codex explicit empty", `{"round":{"tiers":{"codex":{"light":""}}}}`, "codex", "light", row{"", "harness default"}},
		{"codex explicit", `{"round":{"tiers":{"codex":{"light":"x"}}}}`, "codex", "light", row{"x", "explicit"}},
		{"profile does not reach opencode", `{"round":{"profile":"quality"}}`, "opencode", "heavy", row{"opencode/claude-opus-5-5", "default"}},
	}
	for _, c := range cases {
		cfg := config.Load(rotatree.Config(project(t, c.cfg)))
		m, src := Effective(cfg, c.kind, c.tier)
		if m != c.want.model || src != c.want.source {
			t.Errorf("%s: got (%q, %q), want %v", c.name, m, src, c.want)
		}
	}
}

func TestInheritAllowsAnExplicitTier(t *testing.T) {
	s, err := Load(project(t, `{"round":{"profile":"inherit","tiers":{"claude":{"heavy":"opus"}}}}`))
	if err != nil {
		t.Fatalf("an explicit tier beside inherit is allowed: %v", err)
	}
	want := map[string]string{"light": "", "standard": "", "heavy": "opus"}
	if !reflect.DeepEqual(s.Models[harness.Claude], want) {
		t.Errorf("claude map %v, want %v", s.Models[harness.Claude], want)
	}
}

func TestProfileNamesMatchConfigChoices(t *testing.T) {
	for _, key := range config.Keys {
		if key.Name == "round.profile" {
			if !reflect.DeepEqual(key.Choices, ProfileNames) {
				t.Errorf("round.profile choices %v != ProfileNames %v", key.Choices, ProfileNames)
			}
			return
		}
	}
	t.Fatal("round.profile key missing")
}

func TestFullOnly(t *testing.T) {
	for cfg, want := range map[string]bool{
		`{"test":{"full":["go test ./..."]}}`:                            true,
		`{"test":{"fast":["go test {files}"],"full":["go test ./..."]}}`: false,
		``: false,
	} {
		s, err := Load(project(t, cfg))
		if err != nil {
			t.Fatal(err)
		}
		if s.FullOnly != want {
			t.Errorf("%s: FullOnly = %v, want %v", cfg, s.FullOnly, want)
		}
	}
}

// The autopilot follows autonomy.level auto; round.autopilot is no longer
// read, and a leftover value of any type does not break the load.
func TestAutopilotFollowsAutonomy(t *testing.T) {
	for cfg, want := range map[string]bool{
		``:                                     false,
		`{"autonomy":{"level":"off"}}`:         false,
		`{"autonomy":{"level":"roundtrip"}}`:   false,
		`{"autonomy":{"level":"auto"}}`:        true,
		`{"round":{"autopilot":true}}`:         false,
		`{"round":{"autopilot":"yes please"}}`: false,
		`{"autonomy":{"level":"auto"},"round":{"autopilot":false}}`: true,
	} {
		s, err := Load(project(t, cfg))
		if err != nil || s.Autopilot != want {
			t.Errorf("%s: Autopilot %v, err %v, want %v", cfg, s.Autopilot, err, want)
		}
	}
}

func TestSupportKeys(t *testing.T) {
	s, err := Load(project(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	if s.Support != "" || s.Role != "main" {
		t.Fatalf("defaults: Support %q Role %q", s.Support, s.Role)
	}
	s, err = Load(project(t, `{"round":{"support":"mac","role":"support"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if s.Support != "mac" || s.Role != "support" {
		t.Fatalf("Support %q Role %q", s.Support, s.Role)
	}
}

// #1598 AC-4: the support machine prefixes every roster name with its own
// name, and main refuses a roster name a support slot could carry.
func TestSupportPrefixesRoster(t *testing.T) {
	s, err := Load(project(t, `{"round":{"support":"mac","role":"support","roster":["ben","dana"]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s.Roster, []string{"mac-ben", "mac-dana"}) {
		t.Errorf("support roster = %v", s.Roster)
	}
	s, err = Load(project(t, `{"round":{"support":"mac","roster":["ben","dana"]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s.Roster, []string{"ben", "dana"}) {
		t.Errorf("main roster = %v", s.Roster)
	}
	if _, err := Load(project(t, `{"round":{"support":"mac","roster":["ben","mac-kit"]}}`)); err == nil || !strings.Contains(err.Error(), `"mac-kit"`) {
		t.Errorf("main roster with the support prefix: %v", err)
	}
	if _, err := Load(project(t, `{"round":{"support":"Mac Book"}}`)); err == nil || !strings.Contains(err.Error(), "round.support") {
		t.Errorf("a support name unusable in a branch: %v", err)
	}
}
