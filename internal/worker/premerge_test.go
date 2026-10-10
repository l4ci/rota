package worker

import (
	"strings"
	"testing"
)

// The gate and the train share one pre-merge policy: the same input is refused
// with the same verdict in the same order (premerge.go).
func TestGateAndTrainRefuseTheSameInput(t *testing.T) {
	cases := []struct {
		name, config, ledger, want string
	}{
		{"no-verify", `{"test":{"full":[]}}`, "", GateNoVerify},
		{"expired ledger", `{"test":{"full":["true"]}}`, ledgerEntry("TestFlaky", pastDay), GateVerifyFailed},
		// Both rules fire: the ledger is checked first by both.
		{"expired ledger before no-verify", `{"test":{"full":[]}}`, ledgerEntry("TestFlaky", pastDay), GateVerifyFailed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := trainWorld(t, "true", "b1")
			w.setConfig(c.config)
			if c.ledger != "" {
				w.setLedger(c.ledger)
			}
			g, gerr := w.env(false).Gate(bg, w.dir, GateOpts{Slot: "b1", Base: "main"})
			tr, terr := w.train(TrainOpts{Targets: []string{"b1"}})
			if gerr != nil || terr != nil {
				t.Fatalf("gate err %v, train err %v", gerr, terr)
			}
			if g.Verdict != c.want || tr.Verdict != c.want {
				t.Fatalf("verdicts gate %q train %q, want %q", g.Verdict, tr.Verdict, c.want)
			}
			if strings.ReplaceAll(g.Hint, "re-gate", "re-run the train") != tr.Hint {
				t.Errorf("hints differ: gate %q train %q", g.Hint, tr.Hint)
			}
			if len(g.Expired) != len(tr.Expired) {
				t.Errorf("expired entries differ: gate %v train %v", g.Expired, tr.Expired)
			}
			if w.onMain("b1.txt") {
				t.Error("a refused input landed")
			}
		})
	}
}
