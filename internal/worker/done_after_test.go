package worker

import (
	"testing"
	"time"

	"github.com/l4ci/rota/internal/stamp"
)

// One rule decides "did the slot's done come after the relay or delivery":
// seq when the marker recorded one, else a strictly later stamp.
func TestDoneAfter(t *testing.T) {
	at := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	ts := func(d time.Duration) string { return stamp.Format(at.Add(d)) }
	done := func(seq int, d time.Duration) *Event { return &Event{Seq: seq, Kind: EventDone, TS: ts(d)} }
	cases := []struct {
		name string
		ev   *Event
		seq  int
		want bool
	}{
		{"no event", nil, 3, false},
		{"not a done event", &Event{Seq: 9, Kind: EventBlocked, TS: ts(time.Minute)}, 3, false},
		{"seq newer", done(4, 0), 3, true},
		{"seq equal", done(3, time.Minute), 3, false},
		{"seq older, later stamp", done(2, time.Minute), 3, false},
		{"seq newer, same-second stamp", done(4, 0), 3, true},
		{"no seq, later stamp", done(1, time.Second), -1, true},
		{"no seq, same-second stamp", done(1, 0), -1, false},
		{"no seq, earlier stamp", done(1, -time.Second), -1, false},
		{"no seq, bad stamp", &Event{Seq: 1, Kind: EventDone, TS: "junk"}, -1, false},
	}
	for _, c := range cases {
		if got := doneAfter(c.ev, c.seq, at); got != c.want {
			t.Errorf("%s: doneAfter = %v, want %v", c.name, got, c.want)
		}
	}
}
