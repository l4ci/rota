package limits

import (
	"testing"
	"time"
)

func TestCoolingCountsWaitingEntriesOfTheKind(t *testing.T) {
	r := newRig(t)
	reset := t0.Add(time.Hour)
	for _, e := range []Entry{
		{Session: "ben", Kind: KindCodex, Account: "c1", Status: StatusWaiting, ResetsAt: Time(reset)},
		{Session: "dana", Account: "a", Status: StatusWaiting, ResetsAt: Time(reset)},                                 // Claude
		{Session: "kit", Kind: KindCodex, Account: "c2", Status: StatusResumed, ResetsAt: Time(reset)},                // resolved
		{Session: "nia", Kind: KindCodex, Account: "c3", Status: StatusWaiting, ResetsAt: Time(t0.Add(-time.Minute))}, // reset passed
		{Session: "zed", Kind: KindCodex, Status: StatusWaiting, ResetsAt: Time(reset)},                               // default login
	} {
		if _, err := Append(r.root, e); err != nil {
			t.Fatal(err)
		}
	}
	got := Cooling(r.root, KindCodex, t0)
	if len(got) != 1 || !got["c1"].Equal(reset) {
		t.Fatalf("codex cooling %v", got)
	}
	if got := Cooling(r.root, "", t0); len(got) != 1 || !got["a"].Equal(reset) {
		t.Fatalf("claude cooling %v", got)
	}
}

func TestCodexPickSkipsExcludedAndCoolingLogins(t *testing.T) {
	r := newRig(t)
	if _, err := Append(r.root, Entry{Session: "ben", Kind: KindCodex, Account: "c2", Status: StatusWaiting, ResetsAt: Time(t0.Add(time.Hour))}); err != nil {
		t.Fatal(err)
	}
	if got, ok := CodexPick(r.root, []string{"c1", "c2", "c3"}, "c1", t0); !ok || got != "c3" {
		t.Fatalf("pick %q %v", got, ok)
	}
	if got, ok := CodexPick(r.root, []string{"c1", "c2"}, "c1", t0); ok {
		t.Fatalf("every login spent, picked %q", got)
	}
}

// A limit the watcher resolved by moving the issue (StatusSwitched) leaves the
// login just as spent: it must keep cooling until its reset, or CodexPick hands
// the next limit back to it and the pool never reads as closed.
func TestCoolingKeepsASwitchedLoginUntilItsReset(t *testing.T) {
	r := newRig(t)
	reset := t0.Add(time.Hour)
	if _, err := Append(r.root, Entry{Session: "ben", Kind: KindCodex, Account: "c1", Status: StatusSwitched, ResetsAt: Time(reset)}); err != nil {
		t.Fatal(err)
	}
	if got := Cooling(r.root, KindCodex, t0); len(got) != 1 || !got["c1"].Equal(reset) {
		t.Fatalf("switched login not cooling: %v", got)
	}
	if got, ok := CodexPick(r.root, []string{"c1", "c2"}, "", t0); !ok || got != "c2" {
		t.Fatalf("picked the spent login: %q %v", got, ok)
	}
	if got := Cooling(r.root, KindCodex, reset.Add(time.Second)); len(got) != 0 {
		t.Fatalf("still cooling after the reset: %v", got)
	}
}
