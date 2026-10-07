package worker

import (
	"os"
	"path/filepath"
	"testing"
)

func sampleBestOf() BestOf {
	return BestOf{
		Issue: "404",
		Attempts: []BestOfAttempt{
			{Slot: "dana", Branch: "dana/404-x", ClaimID: "dana@8"},
			{Slot: "ben", Branch: "ben/404-x", ClaimID: "ben@8"},
		},
		Round: 8,
	}
}

func TestBestOfRoundTrip(t *testing.T) {
	root := t.TempDir()
	if LoadRegistry(root).BestOf("404") != nil {
		t.Fatal("no record yet")
	}
	if err := Update(root, func(d *Doc) { d.SetBestOf(sampleBestOf()) }); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"404", "#404", " #404 "} {
		b := LoadRegistry(root).BestOf(id)
		if b == nil || b.Issue != "404" || b.Round != 8 || len(b.Attempts) != 2 || b.Pick != "" {
			t.Fatalf("%q: %+v", id, b)
		}
		if a := b.Attempt("ben"); a == nil || a.Branch != "ben/404-x" || a.ClaimID != "ben@8" {
			t.Errorf("Attempt(ben) = %+v", a)
		}
		if a := b.Sibling("ben"); a == nil || a.Slot != "dana" {
			t.Errorf("Sibling(ben) = %+v", a)
		}
		if b.Attempt("kit") != nil || b.Sibling("kit") == nil {
			t.Error("a slot with no attempt has none; its sibling is the first other attempt")
		}
	}
	// the lowercase file-backend spelling normalizes to upper case
	if err := Update(root, func(d *Doc) { b := sampleBestOf(); b.Issue = "#b07"; d.SetBestOf(b) }); err != nil {
		t.Fatal(err)
	}
	if LoadRegistry(root).BestOf("B07") == nil {
		t.Error("B07 not found")
	}
	// SetBestOf replaces the record
	if err := Update(root, func(d *Doc) { b := sampleBestOf(); b.Round = 9; d.SetBestOf(b) }); err != nil {
		t.Fatal(err)
	}
	if b := LoadRegistry(root).BestOf("404"); b == nil || b.Round != 9 {
		t.Errorf("replace: %+v", b)
	}
}

func TestBestOfPickAndClear(t *testing.T) {
	root := t.TempDir()
	if err := SetBestOfPick(root, "404", "#7"); err == nil {
		t.Fatal("no record: want an error")
	}
	if err := ClearBestOf(root, "404"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".rota", "workers.json")); !os.IsNotExist(err) {
		t.Fatalf("clearing nothing must not create workers.json: %v", err)
	}
	Update(root, func(d *Doc) { d.SetBestOf(sampleBestOf()) })
	if err := SetBestOfPick(root, "#404", "https://github.com/o/r/pull/7"); err != nil {
		t.Fatal(err)
	}
	b := LoadRegistry(root).BestOf("404")
	if b == nil || b.Pick != "https://github.com/o/r/pull/7" {
		t.Fatalf("%+v", b)
	}
	for ref, want := range map[string]bool{"7": true, "#7": true, "https://github.com/o/r/pull/7": true, "8": false, "": false, "x": false} {
		if b.Picked(ref) != want {
			t.Errorf("Picked(%q) = %v, want %v", ref, !want, want)
		}
	}
	if (BestOf{}).Picked("7") {
		t.Error("an unpicked record picks nothing")
	}
	if err := ClearBestOf(root, "404"); err != nil {
		t.Fatal(err)
	}
	if LoadRegistry(root).BestOf("404") != nil {
		t.Error("record survived ClearBestOf")
	}
}

func TestQueuePRKeepsOneRecordPerAttempt(t *testing.T) {
	root := t.TempDir()
	Update(root, func(d *Doc) {
		d.QueuePR(QueuedPR{Issue: "404", PR: "#7", From: "dana"})
		d.QueuePR(QueuedPR{Issue: "404", PR: "#8", From: "ben"})
		d.QueuePR(QueuedPR{Issue: "404", PR: "#9", From: "dana"}) // same slot again: replaces
	})
	got := LoadRegistry(root).PRs()
	if len(got) != 2 {
		t.Fatalf("want 2 records, got %+v", got)
	}
	for _, q := range got {
		if (q.From == "dana") != (q.PR == "#9") {
			t.Errorf("dana's record must be the replaced one: %+v", got)
		}
	}
}
