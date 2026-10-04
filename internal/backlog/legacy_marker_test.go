package backlog

import (
	"testing"

	"github.com/l4ci/rota/internal/tracker"
)

// Issues hv wrote before the rename (#236) carry `<!-- hv:... -->` markers.
// rota reads them as its own, so claims, notes and fields on those issues
// keep working until the migration rewrites them.

func TestParseFieldsBlockReadsLegacyMarker(t *testing.T) {
	text, fields, order := ParseFieldsBlock("Body.\n\n<!-- hv:fields\nRelated: B9\n-->")
	if text != "Body." || fields["Related"] != "B9" || len(order) != 1 {
		t.Errorf("text %q fields %v order %v", text, fields, order)
	}
}

func TestLegacyClaimHoldsTheIssue(t *testing.T) {
	is := open(1)
	is.Comments = []tracker.Comment{{ID: "7", Body: "<!-- hv:claim alice -->\nClaimed by alice"}}
	b, _ := newIssues(t, `{}`, is)
	won, holder, err := b.Claim("1", "bob")
	if won || holder != "alice" || err != nil {
		t.Fatalf("won %v holder %q err %v", won, holder, err)
	}
}

func TestLegacyNoteIsReadAndRewrittenInPlace(t *testing.T) {
	is := open(1)
	is.Comments = []tracker.Comment{{ID: "7", Body: "<!-- hv:proof -->\nold proof"}}
	b, tr := newIssues(t, `{}`, is)
	if got, ok, err := b.NoteGet("1", "proof"); !ok || err != nil || got != "old proof" {
		t.Fatalf("get %q %v %v", got, ok, err)
	}
	if _, err := b.NotePut("1", "proof", "new proof"); err != nil {
		t.Fatal(err)
	}
	cs := tr.Issues[0].Comments
	if len(cs) != 1 || cs[0].ID != "7" || cs[0].Body != "<!-- rota:proof -->\nnew proof" {
		t.Errorf("comments %+v", cs)
	}
}
