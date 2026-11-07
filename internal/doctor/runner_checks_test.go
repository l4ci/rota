package doctor

import (
	"strings"
	"testing"
)

// Each optional check owns its skip rule: the zero Input adds no line.
func TestOptionalChecksSkipOnZeroInput(t *testing.T) {
	d := &runner{}
	for name, fn := range map[string]func() (Check, bool){
		"configDir": d.configDir, "agents": d.agents, "gitignore": d.gitignore, "labels": d.labels,
		"staleMilestones": d.staleMilestones, "staleBinary": d.staleBinary, "verify": d.verify,
		"testLock": d.testLock, "stateDir": d.stateDir, "legacyState": d.legacyState,
	} {
		if c, ok := fn(); ok {
			t.Errorf("%s: zero Input added %+v", name, c)
		}
	}
	if got := d.staleClaims(); len(got) != 0 {
		t.Errorf("staleClaims: zero Input added %+v", got)
	}
}

func TestLegacyStateFails(t *testing.T) {
	c, ok := (&runner{in: Input{LegacyDir: "/p"}}).legacyState()
	if !ok || c.Status != Fail || !strings.Contains(c.Detail, "/p") {
		t.Fatalf("legacyState = %+v, %v", c, ok)
	}
}
