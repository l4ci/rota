package round

import (
	"testing"

	"github.com/l4ci/rota/internal/roundcfg"
)

func TestOnHoldExcludedFromCandidates(t *testing.T) {
	for _, label := range []string{"on-hold", "waiting-for-vendor"} {
		t.Run(label, func(t *testing.T) {
			f := newMoveFx(t)
			f.env.OnHold = label
			f.forge.labels = map[int][]string{13: {label}}
			candidates := func() []Candidate {
				t.Helper()
				cs, err := f.env.Candidates(bg, f.root, f.be, CandidateOpts{Scope: roundcfg.ScopeMilestone})
				if err != nil {
					t.Fatal(err)
				}
				return cs
			}
			for _, c := range candidates() {
				if c.ID == "13" {
					t.Fatal("on-hold issue offered as a candidate")
				}
			}
			f.forge.labels[13] = nil
			for _, c := range candidates() {
				if c.ID == "13" {
					return
				}
			}
			t.Fatal("removing hold did not restore the candidate")
		})
	}
}

func TestOnHoldRefusesAssignAndBundle(t *testing.T) {
	for _, bundle := range []bool{false, true} {
		t.Run(map[bool]string{false: "lead", true: "bundle"}[bundle], func(t *testing.T) {
			f := newMoveFx(t)
			f.env.OnHold = "waiting-for-vendor"
			f.forge.labels = map[int][]string{13: {"waiting-for-vendor"}}
			o := AssignOpts{ID: "13", Agent: "dana", AcceptOverlap: true, SettledAnswers: "approved"}
			if bundle {
				f.be.add("15", "Ready lead", "M01", false, "## Acceptance\n- [ ] works\n\n## Touches\n- internal/cli/round.go\n")
				o.ID, o.Bundle = "15", []string{"13"}
			}
			spawned := len(f.host.spawned)
			_, err := f.env.Assign(bg, f.root, f.be, o)
			if err == nil {
				t.Fatal("assigned an on-hold issue")
			}
			if got := blockedBy(t, err); got != "not ready" {
				t.Fatalf("blocked by %q: %v", got, err)
			}
			if f.be.claims["13"] != "" || len(f.host.spawned) != spawned {
				t.Fatal("hold refusal mutated claims or dispatched")
			}
			o.CheckOnly = true
			res, err := f.env.Assign(bg, f.root, f.be, o)
			if err != nil || res.Ready() {
				t.Fatalf("check-only should report not ready: %+v, %v", res, err)
			}
			f.forge.labels[13] = nil
			if res, err := f.env.Assign(bg, f.root, f.be, o); err != nil || !res.Ready() {
				t.Fatalf("hold removal should allow assignment preflight: %+v, %v", res, err)
			}
		})
	}
}
