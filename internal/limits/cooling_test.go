package limits

import (
	"testing"
	"time"

	"github.com/l4ci/rota/internal/harness"
)

func TestCoolingCountsWaitingEntriesOfTheKind(t *testing.T) {
	r := newRig(t)
	reset := t0.Add(time.Hour)
	for _, e := range []Entry{
		{Session: "ben", Kind: harness.Codex, Account: "c1", Status: StatusWaiting, ResetsAt: Time(reset)},
		{Session: "dana", Account: "a", Status: StatusWaiting, ResetsAt: Time(reset)},                                     // Claude
		{Session: "kit", Kind: harness.Codex, Account: "c2", Status: StatusResumed, ResetsAt: Time(reset)},                // resolved
		{Session: "nia", Kind: harness.Codex, Account: "c3", Status: StatusWaiting, ResetsAt: Time(t0.Add(-time.Minute))}, // reset passed
		{Session: "zed", Kind: harness.Codex, Status: StatusWaiting, ResetsAt: Time(reset)},                               // default login
	} {
		if _, err := Append(r.root, e); err != nil {
			t.Fatal(err)
		}
	}
	got := Cooling(r.root, harness.Codex, t0)
	if len(got) != 1 || !got["c1"].Equal(reset) {
		t.Fatalf("codex cooling %v", got)
	}
	if got := Cooling(r.root, "", t0); len(got) != 1 || !got["a"].Equal(reset) {
		t.Fatalf("claude cooling %v", got)
	}
}

func TestLoggedPickSkipsExcludedAndCoolingLogins(t *testing.T) {
	r := newRig(t)
	if _, err := Append(r.root, Entry{Session: "ben", Kind: harness.Codex, Account: "c2", Status: StatusWaiting, ResetsAt: Time(t0.Add(time.Hour))}); err != nil {
		t.Fatal(err)
	}
	if got, ok := LoggedPick(r.root, harness.Codex, []string{"c1", "c2", "c3"}, "c1", t0); !ok || got != "c3" {
		t.Fatalf("pick %q %v", got, ok)
	}
	if got, ok := LoggedPick(r.root, harness.Codex, []string{"c1", "c2"}, "c1", t0); ok {
		t.Fatalf("every login spent, picked %q", got)
	}
}

// A limit the watcher resolved by moving the issue (StatusSwitched) leaves the
// login just as spent: it must keep cooling until its reset, or LoggedPick hands
// the next limit back to it and the pool never reads as closed.
func TestCoolingKeepsASwitchedLoginUntilItsReset(t *testing.T) {
	r := newRig(t)
	reset := t0.Add(time.Hour)
	if _, err := Append(r.root, Entry{Session: "ben", Kind: harness.Codex, Account: "c1", Status: StatusSwitched, ResetsAt: Time(reset)}); err != nil {
		t.Fatal(err)
	}
	if got := Cooling(r.root, harness.Codex, t0); len(got) != 1 || !got["c1"].Equal(reset) {
		t.Fatalf("switched login not cooling: %v", got)
	}
	if got, ok := LoggedPick(r.root, harness.Codex, []string{"c1", "c2"}, "", t0); !ok || got != "c2" {
		t.Fatalf("picked the spent login: %q %v", got, ok)
	}
	if got := Cooling(r.root, harness.Codex, reset.Add(time.Second)); len(got) != 0 {
		t.Fatalf("still cooling after the reset: %v", got)
	}
}
