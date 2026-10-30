package keepalive

import (
	"testing"
	"time"

	"github.com/l4ci/rota/internal/roundlease"
	"github.com/l4ci/rota/internal/stamp"
)

func TestSupervisedHold(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	le := roundlease.Env{Host: "h", Now: func() time.Time { return now },
		Alive:     func(p int) bool { return p == 10 },
		StartTime: func(p int) (uint64, bool) { return 100, p == 10 }}
	live := State{PID: 10, Host: "h", Start: 100, Status: StatusRunning}
	future := now.Add(time.Hour)
	withHold := func(st State, until string) State {
		st.SwitchHold = &Hold{Until: until}
		return st
	}
	cases := []struct {
		name      string
		st        *State
		supervise bool
		until     time.Time
	}{
		{"no file", nil, false, time.Time{}},
		{"not running", &State{PID: 10, Host: "h", Start: 100, Status: StatusStopped}, false, time.Time{}},
		{"dead pid", &State{PID: 11, Host: "h", Start: 100, Status: StatusRunning}, false, time.Time{}},
		{"running no hold", &live, true, time.Time{}},
		{"past hold", ptr(withHold(live, stamp.Format(now.Add(-time.Minute)))), true, time.Time{}},
		{"future hold", ptr(withHold(live, stamp.Format(future))), true, future},
		{"unparsable hold", ptr(withHold(live, "soon")), true, time.Time{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			if c.st != nil {
				if err := WriteState(StatePath(dir), *c.st); err != nil {
					t.Fatal(err)
				}
			}
			sup, until := SupervisedHold(le, dir, now)
			if sup != c.supervise || !until.Equal(c.until) {
				t.Fatalf("got (%v, %v), want (%v, %v)", sup, until, c.supervise, c.until)
			}
		})
	}
}

func ptr(s State) *State { return &s }
