package worker

import (
	"errors"
	"strings"
	"testing"
)

// The gate and the train share one pre-merge policy: the same input is refused
// with the same verdict in the same order (premerge.go).
func TestGateAndTrainRefuseTheSameInput(t *testing.T) {
	cases := []struct {
		name, config, ledger, want string
		approve                    bool
	}{
		{name: "no-verify", config: `{"test":{"full":[]}}`, want: GateNoVerify},
		{name: "expired ledger", config: `{"test":{"full":["true"]}}`, ledger: ledgerEntry("TestFlaky", pastDay), want: GateVerifyFailed},
		// Both rules fire: the ledger is checked first by both.
		{name: "expired ledger before no-verify", config: `{"test":{"full":[]}}`, ledger: ledgerEntry("TestFlaky", pastDay), want: GateVerifyFailed},
		{name: "approval required", config: `{"test":{"full":["true"]}}`, want: GateApprovalRequired, approve: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := trainWorld(t, "true", "b1")
			w.setConfig(c.config)
			if c.ledger != "" {
				w.setLedger(c.ledger)
			}
			gopts := GateOpts{Slot: "b1", Base: "main"}
			topts := TrainOpts{Targets: []string{"b1"}}
			errApprove := errors.New("approval declined")
			if c.approve {
				gopts.Approve = func(func() ([]string, error)) error { return errApprove }
				topts.Approve = gopts.Approve
			}
			g, gerr := w.env(false).Gate(bg, w.dir, gopts)
			tr, terr := w.train(topts)
			if c.approve {
				if !errors.Is(gerr, errApprove) || !errors.Is(terr, errApprove) {
					t.Fatalf("gate err %v, train err %v, want the approval error", gerr, terr)
				}
			} else if gerr != nil || terr != nil {
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
