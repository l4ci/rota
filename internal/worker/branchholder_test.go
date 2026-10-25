package worker

import "testing"

func holderDoc() *Doc {
	d := &Doc{Registry{doc: slotsDefault()}}
	s := NewSlot("ben", "ben/7-x", "/wt/ben", "main", "")
	s.SetBundle([]string{"#8"})
	d.AppendSlot(s)
	d.QueuePR(QueuedPR{Issue: "#9", Branch: "dana/9-y", PR: "#90", Bundle: []string{"#10"}})
	return d
}

func TestBranchHolder(t *testing.T) {
	reg := holderDoc().Registry
	s, q := reg.BranchHolder("ben/7-x")
	if s == nil || q != nil || s.Name() != "ben" {
		t.Fatalf("slot branch: got slot=%v queued=%v", s, q)
	}
	s, q = reg.BranchHolder("dana/9-y")
	if s != nil || q == nil || q.PR != "#90" {
		t.Fatalf("queued branch: got slot=%v queued=%v", s, q)
	}
	if s, q = reg.BranchHolder("nobody/1-z"); s != nil || q != nil {
		t.Fatalf("unheld branch: got slot=%v queued=%v", s, q)
	}
	if s, q = reg.BranchHolder(""); s != nil || q != nil {
		t.Fatalf("empty branch must match nothing: got slot=%v queued=%v", s, q)
	}
}

func TestBranchHolderSlotWinsOverQueued(t *testing.T) {
	d := holderDoc()
	d.QueuePR(QueuedPR{Issue: "#7", Branch: "ben/7-x", PR: "#70"})
	s, q := d.BranchHolder("ben/7-x")
	if s == nil || q != nil {
		t.Fatalf("slot must win: slot=%v queued=%v", s, q)
	}
}
