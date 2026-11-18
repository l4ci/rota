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
		"testLock": d.testLock, "trainFill": d.trainFill, "stateDir": d.stateDir, "legacyState": d.legacyState,
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

// round.trainMax without round.trainFillMinutes can leave a lone ready PR
// waiting for ever; every other pair is silent.
func TestTrainFillWarnsOnlyForMaxWithoutMinutes(t *testing.T) {
	c, ok := (&runner{in: Input{TrainMax: 3}}).trainFill()
	if !ok || c.Status != Warn || !strings.Contains(c.Hint, "round.trainMax") || !strings.Contains(c.Hint, "round.trainFillMinutes") {
		t.Fatalf("trainMax 3, minutes 0 = %+v, %v; want a warning naming both keys", c, ok)
	}
	for _, in := range []Input{{}, {TrainFillMinutes: 20}, {TrainMax: 3, TrainFillMinutes: 20}} {
		if c, ok := (&runner{in: in}).trainFill(); ok {
			t.Errorf("%+v added %+v, want silence", in, c)
		}
	}
}
