package worker

import "testing"

func TestStateSets(t *testing.T) {
	cases := []struct {
		state                                                           string
		done, idle, blocked, dead, parkable, settled, reclaim, building bool
	}{
		{"done", true, false, false, false, true, true, true, false},
		{"idle", false, true, false, false, true, true, true, false},
		{"blocked", false, false, true, false, false, true, true, true},
		{"dead", false, false, false, true, false, false, true, false},
		{"busy", false, false, false, false, false, false, false, true},
		{"", false, false, false, false, false, false, false, true},
		{"limited", false, false, false, false, false, false, false, false},
	}
	for _, c := range cases {
		for _, st := range []string{c.state, upper(c.state)} {
			got := []bool{IsDone(st), IsIdle(st), IsBlocked(st), IsDead(st), Parkable(st), Settled(st), Reclaimable(st), Building(st)}
			want := []bool{c.done, c.idle, c.blocked, c.dead, c.parkable, c.settled, c.reclaim, c.building}
			for i := range got {
				if got[i] != want[i] {
					t.Errorf("state %q predicate %d = %v, want %v", st, i, got[i], want[i])
				}
			}
		}
	}
}

func upper(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'a' && c <= 'z' {
			b[i] = c - 32
		}
	}
	return string(b)
}
