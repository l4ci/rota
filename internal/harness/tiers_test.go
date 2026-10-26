package harness

import (
	"slices"
	"testing"

	"github.com/l4ci/rota/internal/config"
)

func TestTierKey(t *testing.T) {
	if got := TierKey("codex", TierHeavy); got != "round.tiers.codex.heavy" {
		t.Errorf("TierKey = %q", got)
	}
	if !slices.Equal(Tiers, []string{"light", "standard", "heavy"}) {
		t.Errorf("Tiers = %v", Tiers)
	}
}

// Dynamic keys escape the literal-key scan in config. Every registered worker
// kind must have schema entries before readers such as doctor use its defaults.
func TestHarnessKeysExistInSchema(t *testing.T) {
	for _, kind := range Kinds() {
		t.Run(kind, func(t *testing.T) {
			h, ok := Lookup(kind)
			if !ok {
				t.Fatalf("no adapter for %s", kind)
			}
			if key := h.CommandKey(); !config.IsSchemaKey(key) {
				t.Errorf("CommandKey %q is not a schema key", key)
			}
			for _, tier := range Tiers {
				if key := TierKey(kind, tier); !config.IsSchemaKey(key) {
					t.Errorf("TierKey(%q, %q) = %q is not a schema key", kind, tier, key)
				}
			}
		})
	}
}

func TestTierMapSet(t *testing.T) {
	for cfg, want := range map[string]bool{
		``: false,
		`{"round":{"tiers":{"codex":{"light":"  "}}}}`:   false,
		`{"round":{"tiers":{"codex":{"heavy":"m"}}}}`:    true,
		`{"round":{"tiers":{"opencode":{"light":"m"}}}}`: false,
		`{"round":{"tiers":{"codex":{"standard":"m"}}}}`: true,
	} {
		c := cfgOf(t, cfg)
		if got := TierMapSet(c, Codex); got != want {
			t.Errorf("TierMapSet(codex, %s) = %v, want %v", cfg, got, want)
		}
	}
	// Both readers of a kind's tier map go through the same seam.
	for _, k := range []string{Codex, OpenCode} {
		h, _ := Lookup(k)
		cfg := cfgOf(t, `{"round":{"tiers":{"`+k+`":{"standard":"m"}}}}`)
		if !h.Configured(cfg) {
			t.Errorf("%s Configured must follow its tier map", k)
		}
	}
}

func TestParseTierKeyRoundTrip(t *testing.T) {
	for _, kind := range Kinds() {
		for _, tier := range Tiers {
			k, ti, ok := ParseTierKey(TierKey(kind, tier))
			if !ok || k != kind || ti != tier {
				t.Errorf("ParseTierKey(TierKey(%q, %q)) = %q, %q, %v", kind, tier, k, ti, ok)
			}
		}
	}
}

func TestParseTierKeyRejects(t *testing.T) {
	for _, key := range []string{
		"", "round.tiers.", "round.tiers.codex", "round.tiers.codex.", "round.tiers.codex.huge",
		"round.profile", "round.tiers.codex.light.extra", "x.round.tiers.codex.light",
	} {
		if k, ti, ok := ParseTierKey(key); ok {
			t.Errorf("ParseTierKey(%q) = %q, %q, true; want rejected", key, k, ti)
		}
	}
}
