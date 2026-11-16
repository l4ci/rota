package round

import (
	"reflect"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/worker"
)

// `round transfer <lead> --to <slot>` moves the whole bundle in one call and
// keeps the bundle's branch (#3310).
func TestTransferBundleLeadToSlotMovesTheWholeBundle(t *testing.T) {
	f := newMoveFx(t)
	f.bundled(t, "15", "16")
	branch := f.slot("ben").Branch()
	f.host.sent = ""
	res, err := f.transfer("12", "dana", nil)
	if err != nil {
		t.Fatalf("transfer of a bundle lead must succeed: %v", err)
	}
	if res.From != "ben" || res.To != "dana" || res.Issue != "12" || res.Branch != branch || !res.Changed || !res.Dispatched {
		t.Fatalf("transfer: %+v", res)
	}
	d, b := f.slot("dana"), f.slot("ben")
	if d.Task() != "12" || d.Branch() != branch || !reflect.DeepEqual(d.Bundle(), []string{"15", "16"}) {
		t.Errorf("receiver must hold the lead and every member on the bundle's branch: %+v", d.Raw())
	}
	if b.Task() != "" || len(b.Bundle()) != 0 || b.ClaimID() != "" {
		t.Errorf("nothing may be left on the sender: %+v", b.Raw())
	}
	for _, id := range []string{"12", "15", "16"} {
		if f.be.claims[id] != "dana@1" || f.be.bstates[id] != "in-progress" {
			t.Errorf("%s: claim %q state %q", id, f.be.claims[id], f.be.bstates[id])
		}
	}
	for _, id := range []string{"15", "16"} {
		c := f.be.comments[id]
		if len(c) == 0 || !strings.Contains(c[len(c)-1], "(transfer, from ben)") {
			t.Errorf("%s lacks the handoff comment: %q", id, c)
		}
	}
	for _, want := range []string{"This assignment is a bundle of", "### Issue #15", "### Issue #16"} {
		if !strings.Contains(f.host.sent, want) {
			t.Errorf("brief lacks %q:\n%s", want, f.host.sent)
		}
	}
	f.env.Board = f.be
	rep, err := f.env.Status(bg, f.root)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rep.Rows {
		switch row.Name {
		case "dana":
			if row.Issue != "12" || !reflect.DeepEqual(row.Bundle, []string{"15", "16"}) {
				t.Errorf("status row of the target: %+v", row)
			}
		case "ben":
			if row.Issue != "" || len(row.Bundle) != 0 {
				t.Errorf("status still shows the bundle on the source: %+v", row)
			}
		}
	}
}

// A bundle's PR waiting in the queue moves as a whole too.
func TestTransferQueuedBundleLeadToSlotMovesTheWholeBundle(t *testing.T) {
	f := newMoveFx(t)
	f.bundled(t, "15")
	f.finish(t, "ben", pr7)
	if _, err := f.assign("13", "ben"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.transfer("12", "dana", nil); err != nil {
		t.Fatalf("transfer: %v", err)
	}
	if d := f.slot("dana"); d.Task() != "12" || !reflect.DeepEqual(d.Bundle(), []string{"15"}) || d.PR() != pr7 {
		t.Errorf("receiver: %+v", d.Raw())
	}
	if len(f.queued()) != 0 || f.slot("ben").HeldID() != "13" {
		t.Errorf("queue/sender: %+v %+v", f.queued(), f.slot("ben").Raw())
	}
	if f.be.claims["15"] != "dana@1" {
		t.Errorf("member claim: %v", f.be.claims)
	}
}

// When the move is refused for a real reason, the refusal names the members and
// prints the commands that do the move by hand.
func TestTransferBundleLeadRefusalNamesMembersAndCommands(t *testing.T) {
	f := newMoveFx(t)
	f.bundled(t, "15", "16")
	if _, err := f.assign("13", "dana"); err != nil {
		t.Fatal(err)
	}
	_, err := f.transfer("12", "dana", nil)
	if err == nil {
		t.Fatal("a busy receiver must refuse")
	}
	for _, want := range []string{"#15", "#16", "rota round transfer 15 --to human", "rota round transfer 16 --to human", "rota round transfer 12 --to dana"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal lacks %q: %v", want, err)
		}
	}
	if s := f.slot("ben"); s.HeldID() != "12" || !reflect.DeepEqual(s.Bundle(), []string{"15", "16"}) {
		t.Errorf("a refusal moves nothing: %+v", s.Raw())
	}
	_ = worker.NormID
}
