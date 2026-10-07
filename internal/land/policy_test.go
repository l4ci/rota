package land

import "testing"

// TestPolicy pins, per merge path, which gates it enforces. A change here is
// a change to what a path enforces: it needs its own issue, not a refactor.
func TestPolicy(t *testing.T) {
	want := map[string][]Gate{
		"ship.MergeBranch":     {GateVerdict, GateApproval},
		"backlog.MergePRGated": {GateVerdict, GateApproval, GateProof},
		"worker.Gate":          {GateFreshness, GateProvenance, GateApproval},
		"worker.Train":         {GateFreshness, GateProvenance, GateApproval},
	}
	if len(Policy) != len(want) {
		t.Fatalf("Policy has %d paths, want %d", len(Policy), len(want))
	}
	for _, p := range Policy {
		w, ok := want[p.Name]
		if !ok {
			t.Errorf("unexpected path %q", p.Name)
			continue
		}
		if len(p.Gates) != len(w) {
			t.Errorf("%s gates = %v, want %v", p.Name, p.Gates, w)
			continue
		}
		for i := range w {
			if p.Gates[i] != w[i] {
				t.Errorf("%s gates = %v, want %v", p.Name, p.Gates, w)
				break
			}
		}
	}
}

// Every path approves before it lands, and proof (which can flip items to
// changes-requested) never runs before approval.
func TestPolicyInvariants(t *testing.T) {
	for _, p := range Policy {
		if !Enforces(p.Name, GateApproval) {
			t.Errorf("%s has no approval gate", p.Name)
		}
		approved := false
		for _, g := range p.Gates {
			switch g {
			case GateApproval:
				approved = true
			case GateProof:
				if !approved {
					t.Errorf("%s runs proof before approval", p.Name)
				}
			}
		}
	}
	if Enforces("worker.Gate", GateVerdict) || Enforces("worker.Train", GateVerdict) {
		t.Error("worker paths gained a verdict gate: update the Policy note and the docs")
	}
}
