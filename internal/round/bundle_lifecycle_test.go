package round

import (
	"reflect"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/worker"
)

// bundled turns ben's slot (it holds 12) into the lead of a bundle with the
// given members, claimed and in progress as assign leaves them.
func (f *moveFx) bundled(t *testing.T, members ...string) {
	t.Helper()
	for _, m := range members {
		f.be.add(m, "Member "+m, "M01", false, "## Acceptance\n- [ ] ok\n")
		if ok, _, err := f.be.Claim(m, "ben@1"); err != nil || !ok {
			t.Fatalf("claim %s: %v %v", m, ok, err)
		}
		if _, err := f.be.SetState(m, "in-progress"); err != nil {
			t.Fatal(err)
		}
	}
	if err := editSlot(f.root, "ben", func(s *worker.Slot) error { s.SetBundle(members); return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestReclaimReleasesBundle(t *testing.T) {
	f := newMoveFx(t)
	f.bundled(t, "15", "16")
	f.agents()
	rawSlot(f.root, "ben", func(s *jsonx.Object) { s.Set("handle", "w1:ben") })
	if _, err := f.reclaim("ben", nil); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"12", "15", "16"} {
		if f.be.claims[id] != "" || f.be.bstates[id] != "" {
			t.Errorf("%s: claim %q state %q", id, f.be.claims[id], f.be.bstates[id])
		}
	}
	for _, id := range []string{"15", "16"} {
		c := f.be.comments[id]
		if len(c) != 1 || !strings.Contains(c[0], "Reason: reclaimed, dead") || !strings.Contains(c[0], "(reclaim, from ben)") {
			t.Errorf("%s handoff: %q", id, c)
		}
	}
	if s := f.slot("ben"); s.Task() != "" || len(s.Bundle()) != 0 {
		t.Errorf("slot: %v", s)
	}
}

func TestReturnReleasesBundle(t *testing.T) {
	f := newMoveFx(t)
	f.bundled(t, "15")
	if _, err := f.ret("ben", "stuck", nil); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"12", "15"} {
		if f.be.claims[id] != "" || f.be.bstates[id] != "" {
			t.Errorf("%s: claim %q state %q", id, f.be.claims[id], f.be.bstates[id])
		}
	}
	if c := f.be.comments["15"]; len(c) != 1 || !strings.Contains(c[0], "(return, from ben)") {
		t.Errorf("member handoff: %q", c)
	}
	if s := f.slot("ben"); s.Task() != "" || len(s.Bundle()) != 0 {
		t.Errorf("slot: %v", s)
	}
}

func TestTransferMemberToHumanDropsIt(t *testing.T) {
	f := newMoveFx(t)
	f.bundled(t, "15", "16")
	res, err := f.transfer("15", HumanTarget, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.From != "ben" || res.Issue != "15" || res.To != HumanTarget || !res.Changed || res.Dispatched {
		t.Fatalf("%+v", res)
	}
	if f.be.claims["15"] != "" || f.be.bstates["15"] != "" {
		t.Errorf("member released: claim %q state %q", f.be.claims["15"], f.be.bstates["15"])
	}
	if got := f.forge.labels[15]; len(got) != 1 || got[0] != "needs-human" {
		t.Errorf("labels %v", got)
	}
	if c := f.be.comments["15"]; len(c) != 1 || !strings.Contains(c[0], "left the bundle of #12 on slot ben: transferred to the human") {
		t.Errorf("comment: %q", c)
	}
	s := f.slot("ben")
	if s.Task() != "12" || !reflect.DeepEqual(s.Bundle(), []string{"16"}) || s.State() == "idle" {
		t.Errorf("the lead keeps the slot, the bundle shrinks: task %q bundle %v state %s", s.Task(), s.Bundle(), s.State())
	}
	if f.be.claims["12"] != "ben@1" || f.be.claims["16"] != "ben@1" {
		t.Errorf("lead and the other member stay claimed: %v", f.be.claims)
	}
	if cur := gitIn(t, f.wt("ben"), "symbolic-ref", "--short", "HEAD"); cur == "park/ben" {
		t.Error("the lead's slot is not parked")
	}
	// The last member leaves: the bundle field goes.
	if _, err := f.transfer("16", HumanTarget, nil); err != nil {
		t.Fatal(err)
	}
	if b := f.slot("ben").Bundle(); len(b) != 0 {
		t.Errorf("bundle %v", b)
	}
}

func TestTransferBundleLeadRefused(t *testing.T) {
	for _, to := range []string{HumanTarget, "dana"} {
		t.Run(to, func(t *testing.T) {
			f := newMoveFx(t)
			f.bundled(t, "15", "16")
			notes := len(f.be.comments["12"])
			_, err := f.transfer("12", to, nil)
			if blockedBy(t, err) != BlockBundle || !strings.Contains(err.Error(), "#12 leads a bundle with #15, #16") ||
				!strings.Contains(err.Error(), "transfer each member to the human first") {
				t.Fatalf("%v", err)
			}
			s := f.slot("ben")
			if s.Task() != "12" || len(s.Bundle()) != 2 || f.be.claims["12"] != "ben@1" || len(f.be.comments["12"]) != notes {
				t.Errorf("nothing moves: %v %v", s, f.be.claims)
			}
		})
	}
}

func TestTransferMemberToSlotRefused(t *testing.T) {
	f := newMoveFx(t)
	f.bundled(t, "15")
	_, err := f.transfer("15", "dana", nil)
	if blockedBy(t, err) != BlockBundle || !strings.Contains(err.Error(), "only to the human") {
		t.Fatalf("%v", err)
	}
	if f.be.claims["15"] != "ben@1" || len(f.slot("ben").Bundle()) != 1 || len(f.be.comments["15"]) != 0 {
		t.Errorf("nothing moves: %v %v", f.be.claims, f.slot("ben").Bundle())
	}
}

func TestWindDownReleasesBundleMemberClaims(t *testing.T) {
	f := bundleFixture(t)
	if _, err := f.assign("12", "ben", bundleOf("15", "16")); err != nil {
		t.Fatal(err)
	}
	f.verifyWith(t, `["true"]`)
	res, err := f.windDown(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range res.Warnings {
		if strings.Contains(w, "release claim") {
			t.Errorf("warning: %s", w)
		}
	}
	for _, id := range []string{"12", "15", "16"} {
		if _, held := f.be.claims[id]; held {
			t.Errorf("%s claim must be released: %v", id, f.be.claims)
		}
	}
	if s := worker.LoadRegistryTolerant(f.root).Slot("ben"); s.Task() != "" || len(s.Bundle()) != 0 {
		t.Errorf("slot: %v", s)
	}
}
