package harness

import (
	"slices"
	"testing"
)

func TestTierKey(t *testing.T) {
	if got := TierKey("codex", TierHeavy); got != "round.tiers.codex.heavy" {
		t.Errorf("TierKey = %q", got)
	}
	if !slices.Equal(Tiers, []string{"light", "standard", "heavy"}) {
		t.Errorf("Tiers = %v", Tiers)
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
