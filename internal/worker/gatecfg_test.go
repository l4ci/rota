package worker

import (
	"reflect"
	"testing"

	"github.com/l4ci/rota/internal/roundcfg"
)

// A bad value is held until the step that needs it asks, so a config the old
// code accepted at load time still loads.
func TestParseGateConfigDefersBadValues(t *testing.T) {
	c := ParseGateConfig(parseCfg(`{"test":{"full":[" a "],"ciTimeoutMinutes":0,"isolate":"x"},"ship":{"review":"x"},"round":{"sharedPaths":["p"]}}`))
	if !reflect.DeepEqual(c.Full, []string{"a"}) || !reflect.DeepEqual(c.SharedPaths, []string{"p"}) {
		t.Errorf("full %v shared %v", c.Full, c.SharedPaths)
	}
	if w, err := c.Where(); err != nil || w != WhereLocal {
		t.Errorf("where %q %v", w, err)
	}
	if _, err := c.CISettings(func(string) string { return "" }); err == nil {
		t.Error("ciTimeoutMinutes 0 must fail at CISettings")
	}
	if _, err := c.IsolateEnabled(); err == nil {
		t.Error("non-boolean test.isolate must fail at IsolateEnabled")
	}
	if _, err := ParseGateConfig(parseCfg(`{"work":{"tdd":"yes"}}`)).TDDEnabled(); err == nil {
		t.Error("non-boolean work.tdd must fail at TDDEnabled")
	}
}

func TestParseGateConfigLegacyKey(t *testing.T) {
	c := ParseGateConfig(parseCfg(`{"refactor":{"verifyCommands":["old"]},"test":{"fast":["f"]}}`))
	if !reflect.DeepEqual(c.Full, []string{"old"}) || !reflect.DeepEqual(c.Tier("fast"), []string{"f"}) || c.Tier("nope") != nil {
		t.Errorf("%+v", c)
	}
}

// The gate and the round settings read one parse: a malformed list is held
// as TestsErr (and reads empty) rather than dropped, and a bad work.tdd stays
// TDDEnabled's alone.
func TestParseGateConfigTestsErr(t *testing.T) {
	c := ParseGateConfig(parseCfg(`{"test":{"fast":"x","full":["a"]}}`))
	if c.TestsErr() == nil || len(c.Fast) != 0 {
		t.Errorf("malformed test.fast: err %v fast %v", c.TestsErr(), c.Fast)
	}
	c = ParseGateConfig(parseCfg(`{"work":{"tdd":"yes"}}`))
	if c.TestsErr() != nil {
		t.Errorf("bad work.tdd leaked into TestsErr: %v", c.TestsErr())
	}
}

// Pin both consumers to literal expectations: agreement alone would also pass
// if their shared parser returned the same wrong value to each.
func TestGateConfigAndRoundSettingsValues(t *testing.T) {
	for _, tc := range []struct {
		cfg      string
		shared   []string
		support  string
		fullOnly bool
	}{
		{`{"test":{"full":["go test ./..."]},"round":{"sharedPaths":[" docs/*.md "," "],"support":" mac "}}`, []string{"docs/*.md"}, "mac", true},
		{`{"test":{"fast":["f"],"full":["go test ./..."]}}`, nil, "", false},
		{`{"refactor":{"verifyCommands":["old"]}}`, nil, "", true},
	} {
		cfg := parseCfg(tc.cfg)
		set, err := roundcfg.FromConfig(cfg)
		if err != nil {
			t.Fatalf("%s: %v", tc.cfg, err)
		}
		g := ParseGateConfig(cfg)
		for name, got := range map[string]struct {
			shared   []string
			support  string
			fullOnly bool
		}{
			"gate":  {g.SharedPaths, g.Support, g.FullOnly()},
			"round": {set.SharedPaths, set.Support, set.FullOnly},
		} {
			if !reflect.DeepEqual(got.shared, tc.shared) || got.support != tc.support || got.fullOnly != tc.fullOnly {
				t.Errorf("%s %s: got %+v, want shared %v support %q fullOnly %v", tc.cfg, name, got, tc.shared, tc.support, tc.fullOnly)
			}
		}
	}
}

func TestGateConfigFullOnlyFollowsExportedFields(t *testing.T) {
	for _, c := range []struct {
		name string
		g    GateConfig
		want bool
	}{
		{"full only parsed", ParseGateConfig(parseCfg(`{"test":{"fast":[],"full":["go test ./..."]}}`)), true},
		{"fast and full parsed", ParseGateConfig(parseCfg(`{"test":{"fast":["f"],"full":["go test ./..."]}}`)), false},
		{"neither parsed", ParseGateConfig(parseCfg(`{}`)), false},
		{"full only literal", GateConfig{Full: []string{"go test ./..."}}, true},
		{"fast and full", GateConfig{Fast: []string{"f"}, Full: []string{"go test ./..."}}, false},
		{"neither", GateConfig{}, false},
	} {
		if got := c.g.FullOnly(); got != c.want {
			t.Errorf("%s: FullOnly() = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestGateConfigLabels(t *testing.T) {
	for _, tc := range []struct{ cfg, inProgress, support string }{
		{`{}`, "in-progress", ""},
		{`{"issues":{"labels":{"inProgress":"wip-x","support":"support-x"}}}`, "wip-x", ""},
		{`{"round":{"support":"mac"},"issues":{"labels":{"inProgress":"wip-x","support":"support-x"}}}`, "wip-x", "machine:mac"},
	} {
		got := ParseGateConfig(parseCfg(tc.cfg))
		if got.InProgressLabel != tc.inProgress || got.SupportLabel != tc.support {
			t.Errorf("%s: labels = %q, %q; want %q, %q", tc.cfg, got.InProgressLabel, got.SupportLabel, tc.inProgress, tc.support)
		}
	}
}
