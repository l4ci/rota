package limits

import (
	"testing"
	"time"

	"github.com/l4ci/rota/internal/harness"
)

func TestCoolingCountsWaitingEntriesOfTheKind(t *testing.T) {
	reset := t0.Add(time.Hour)
	entries := []Entry{
		{Session: "ben", Kind: harness.Codex, Account: "c1", Status: StatusWaiting, ResetsAt: Time(reset)},
		{Session: "dana", Account: "a", Status: StatusWaiting, ResetsAt: Time(reset)},                                     // Claude
		{Session: "kit", Kind: harness.Codex, Account: "c2", Status: StatusResumed, ResetsAt: Time(reset)},                // resolved
		{Session: "nia", Kind: harness.Codex, Account: "c3", Status: StatusWaiting, ResetsAt: Time(t0.Add(-time.Minute))}, // reset passed
		{Session: "zed", Kind: harness.Codex, Status: StatusWaiting, ResetsAt: Time(reset)},                               // default login
	}
	got := Cooling(entries, harness.Codex, t0)
	if len(got) != 1 || !got["c1"].Equal(reset) {
		t.Fatalf("codex cooling %v", got)
	}
	if got := Cooling(entries, "", t0); len(got) != 1 || !got["a"].Equal(reset) {
		t.Fatalf("claude cooling %v", got)
	}
}

// A limit the watcher resolved by moving the issue (StatusSwitched) leaves the
// login just as spent: it must keep cooling until its reset, or LoggedPick hands
// the next limit back to it and the pool never reads as closed.
func TestCoolingKeepsASwitchedLoginUntilItsReset(t *testing.T) {
	reset := t0.Add(time.Hour)
	entries := []Entry{{Session: "ben", Kind: harness.Codex, Account: "c1", Status: StatusSwitched, ResetsAt: Time(reset)}}
	if got := Cooling(entries, harness.Codex, t0); len(got) != 1 || !got["c1"].Equal(reset) {
		t.Fatalf("switched login not cooling: %v", got)
	}
	if got, ok := LoggedPick(entries, harness.Codex, []string{"c1", "c2"}, "", t0); !ok || got != "c2" {
		t.Fatalf("picked the spent login: %q %v", got, ok)
	}
	if got := Cooling(entries, harness.Codex, reset.Add(time.Second)); len(got) != 0 {
		t.Fatalf("still cooling after the reset: %v", got)
	}
}
