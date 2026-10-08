package round

import (
	"testing"

	"github.com/l4ci/rota/internal/roundcfg"
)

// round.scopeOverlap reaches Transfer: the receiver's item clashes with dana's
// 15 on a declared scope only, which warns by default and refuses under block.
func TestTransferScopeOverlapBlock(t *testing.T) {
	setup := func() *moveFx {
		f := newMoveFx(t)
		o := startOpts(roundcfg.ScopeMilestone, 100)
		o.Slots = 3
		if _, err := f.env.Start(bg, f.root, o); err != nil {
			t.Fatal(err)
		}
		f.be.details["12"] = "## Acceptance\n- [ ] works\n\n## Touches\n- POST /items\n"
		f.be.add("15", "Scope rival", "M01", false, "## Acceptance\n- [ ] x\n\n## Touches\n- post /items\n")
		if _, err := f.assign("15", "dana"); err != nil {
			t.Fatal(err)
		}
		return f
	}

	f := setup()
	res, err := f.transfer("12", "nia", nil)
	if err != nil || !res.Dispatched || f.be.claims["12"] != "nia@1" {
		t.Fatalf("warn: a scope-only clash does not stop the move: %v %+v", err, res)
	}

	f = setup()
	_, err = f.transfer("12", "nia", func(o *TransferOpts) { o.Settings.ScopeOverlap = "block" })
	if by := blockedBy(t, err); by != BlockOverlap {
		t.Fatalf("block: a scope-only clash refuses the move: %v", err)
	}
	if f.be.claims["12"] != "ben@1" || f.slot("ben").Task() != "12" {
		t.Error("an overlap refusal changes nothing")
	}
}
