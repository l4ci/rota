package roundwatch

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestSupportKeyShape(t *testing.T) {
	if got := SupportKey("12", "pr-head"); got != "support/12/pr-head" {
		t.Fatalf("SupportKey = %q", got)
	}
}

// #1827 AC-3: a moved PR head, a PR that opens and a comment by the other
// machine are changes; the line text and --json read the key and values.
func TestDiffReportsSupportChanges(t *testing.T) {
	prev := map[string]string{SupportKey("12", "pr-head"): "aaa111", "pr/state/ben": "OPEN"}
	cur := map[string]string{
		SupportKey("12", "pr-head"):   "bbb222",
		SupportKey("12", "pr-opened"): "#9",
		SupportKey("12", "comment"):   "ann#31",
		"pr/state/ben":                "OPEN",
	}
	var got []string
	for _, c := range Diff(prev, cur) {
		got = append(got, c.Key+"|"+c.From+"|"+c.To)
	}
	want := []string{"support/12/comment||ann#31", "support/12/pr-head|aaa111|bbb222", "support/12/pr-opened||#9"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("changes = %q, want %q", got, want)
	}
	id, kind, to, ok := SupportChange(Change{Key: "support/12/comment", To: "ann#31"})
	if !ok || id != "12" || kind != "comment" || to != "ann" {
		t.Fatalf("SupportChange = %q %q %q %v", id, kind, to, ok)
	}
	if _, _, _, ok := SupportChange(Change{Key: "pr/state/ben"}); ok {
		t.Fatal("a slot key is no support change")
	}
}

// A support PR that is already open when the watch starts is unseen, not part
// of the baseline (the 2026-10 report: a start baseline swallowed two of them).
func TestRunWakesOnSupportEntriesTheOrchestratorHasNotSeen(t *testing.T) {
	forge := func() map[string]string { return map[string]string{SupportKey("12", "pr-head"): "aaa111"} }
	run := func(seen map[string]string) (Result, map[string]string) {
		fc := &fakeClock{now: time.Unix(0, 0)}
		env := fc.env(func(time.Duration) (*SlotNews, error) { return nil, nil }, empty, forge)
		env.Seen = func() map[string]string { return seen }
		var marked map[string]string
		env.Mark = func(m map[string]string) { marked = m }
		res, err := Run(context.Background(), env, Opts{Heartbeat: time.Hour, Poll: 30 * time.Second, ForgeEvery: 60 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		return res, marked
	}
	res, marked := run(map[string]string{})
	if res.Reason != ReasonChange || len(res.Changes) != 1 || res.Changes[0].To != "aaa111" {
		t.Fatalf("an unseen open PR must wake the watch: %+v", res)
	}
	if marked[SupportKey("12", "pr-head")] != "aaa111" {
		t.Errorf("the woken-for value must be marked seen: %v", marked)
	}
	// Seen already: nothing to wake for, the heartbeat ends it.
	fc := &fakeClock{now: time.Unix(0, 0)}
	env := fc.env(func(time.Duration) (*SlotNews, error) { return nil, nil }, empty, forge)
	env.Seen = func() map[string]string { return map[string]string{SupportKey("12", "pr-head"): "aaa111"} }
	res2, err := Run(context.Background(), env, Opts{Heartbeat: 100 * time.Second, Poll: 30 * time.Second, ForgeEvery: 60 * time.Second})
	if err != nil || res2.Reason != ReasonHeartbeat {
		t.Fatalf("a seen entry must not wake the watch: %+v %v", res2, err)
	}
}

// A support entry that disappears (a forge read that failed, an issue handed
// back) is nothing new: it never wakes the watch.
func TestRunIgnoresVanishedSupportEntries(t *testing.T) {
	fc := &fakeClock{now: time.Unix(0, 0)}
	env := fc.env(func(time.Duration) (*SlotNews, error) { return nil, nil }, empty, empty)
	env.Seen = func() map[string]string { return map[string]string{SupportKey("12", "comment"): "ann#31"} }
	res, err := Run(context.Background(), env, Opts{Heartbeat: 100 * time.Second, Poll: 30 * time.Second, ForgeEvery: 60 * time.Second})
	if err != nil || res.Reason != ReasonHeartbeat {
		t.Fatalf("%+v %v", res, err)
	}
}
