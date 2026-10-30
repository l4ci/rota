package pidlive

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// fakeTable is a recorded process table: the Source tests of every caller
// cross the seam with.
type Row struct {
	PPID  int
	Start uint64
	State string
	Comm  string
	Cwd   string
}

type fakeTable struct {
	rows map[int]Row
}

func (f fakeTable) State(pid int) (string, bool) {
	r, ok := f.rows[pid]
	return r.State, ok
}
func (f fakeTable) Stat(pid int) (int, uint64, bool) {
	r, ok := f.rows[pid]
	return r.PPID, r.Start, ok
}
func (f fakeTable) Comm(pid int) string { return f.rows[pid].Comm }
func (f fakeTable) Cwd(pid int) string  { return f.rows[pid].Cwd }
func (f fakeTable) Parents() (map[int]int, bool) {
	out := map[int]int{}
	for pid, r := range f.rows {
		out[pid] = r.PPID
	}
	return out, true
}

func TestTreeListsDescendantsBreadthFirst(t *testing.T) {
	src := fakeTable{rows: map[int]Row{
		1: {PPID: 0}, 10: {PPID: 1}, 11: {PPID: 10}, 12: {PPID: 10}, 13: {PPID: 11}, 99: {PPID: 1},
	}}
	if got := Tree(src, 10); !reflect.DeepEqual(got, []int{10, 11, 12, 13}) {
		t.Errorf("Tree = %v", got)
	}
}

func TestRunningMatchesRecordedStart(t *testing.T) {
	self := selfPID()
	src := fakeTable{rows: map[int]Row{self: {State: "S", Start: 50}}}
	for _, c := range []struct {
		name  string
		pid   int
		start uint64
		want  bool
	}{
		{"no recorded start", self, 0, true},
		{"same start", self, 50, true},
		{"reused pid", self, 51, false},
		{"unreadable start is not a mismatch", self, 7, false},
		{"gone", 1 << 22, 50, false},
	} {
		if c.name == "unreadable start is not a mismatch" {
			noStart := fakeNoStat{src}
			if !Running(noStart, c.pid, c.start) {
				t.Errorf("%s: want running", c.name)
			}
			continue
		}
		if got := Running(src, c.pid, c.start); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

// fakeNoStat answers State but cannot read a start time.
type fakeNoStat struct{ fakeTable }

func (fakeNoStat) Stat(int) (int, uint64, bool) { return 0, 0, false }

func TestParentSkipsWithoutComm(t *testing.T) {
	src := fakeTable{rows: map[int]Row{5: {PPID: 4, Comm: "zsh"}, 6: {PPID: 4}}}
	if pp, comm, ok := Parent(src, 5); !ok || pp != 4 || comm != "zsh" {
		t.Errorf("Parent(5) = %d %q %v", pp, comm, ok)
	}
	if _, _, ok := Parent(src, 6); ok {
		t.Error("a process with no comm must read as unverified")
	}
}

func TestParseLsofCwd(t *testing.T) {
	if got := ParseLsofCwd("p91\nfcwd\nn/Users/me/p/.worktrees/w1\n"); got != "/Users/me/p/.worktrees/w1" {
		t.Errorf("ParseLsofCwd = %q", got)
	}
	if got := ParseLsofCwd("p91\n"); got != "" {
		t.Errorf("ParseLsofCwd(no n line) = %q", got)
	}
}

// psTable is a Real over a recorded ps table keyed by pid: "ppid lstart" and comm.
func psTable(rows map[string][2]string) Real {
	return Real{PS: func(args ...string) (string, error) {
		col, pid := args[1], args[3]
		r, ok := rows[pid]
		if !ok {
			return "", errors.New("no such pid")
		}
		if col == "comm=" {
			return r[1], nil
		}
		return r[0], nil
	}}
}

// Where /proc is missing (macOS) ppid, comm and start time come from ps (#1167).
func TestRealReadsAncestryThroughPS(t *testing.T) {
	t.Parallel()
	const agentPath = "/Applications/Claude Code/claude"
	r := psTable(map[string][2]string{
		"900000001": {"900000002 Mon Oct 20 12:00:00 2026", "/bin/zsh"},
		"900000002": {"900000003 Mon Oct 20 11:00:00 2026", agentPath},
	})
	pp, comm, ok := Parent(r, 900000001)
	if !ok || pp != 900000002 || comm != "zsh" {
		t.Fatalf("zsh: %d %q %v", pp, comm, ok)
	}
	if _, comm, _ = Parent(r, 900000002); comm != filepath.Base(agentPath) {
		t.Fatalf("comm must be the base name: %q", comm)
	}
	want := time.Date(2026, 10, 20, 11, 0, 0, 0, time.Local).Unix()
	if _, st, ok := r.Stat(900000002); !ok || int64(st) != want {
		t.Fatalf("start %d %v, want %d", st, ok, want)
	}
}

// ps pads days below 10 with two spaces ("Oct  5"); strings.Fields collapses
// them and layout _2 still parses.
func TestRealStatParsesSingleDigitDay(t *testing.T) {
	t.Parallel()
	r := psTable(map[string][2]string{"900000011": {"900000012 Sun Oct  5 09:03:07 2025", "/bin/zsh"}})
	pp, st, ok := r.Stat(900000011)
	want := time.Date(2025, 10, 5, 9, 3, 7, 0, time.Local).Unix()
	if !ok || pp != 900000012 || int64(st) != want {
		t.Fatalf("Stat = %d %d %v, want 900000012 %d true", pp, st, ok, want)
	}
}

func TestRealParentsParsesPSTable(t *testing.T) {
	r := Real{PS: func(...string) (string, error) {
		return "  1     0\n 10     1\n 11    10\n junk\n", nil
	}}
	got, ok := r.Parents()
	if !ok || !reflect.DeepEqual(got, map[int]int{1: 0, 10: 1, 11: 10}) {
		t.Errorf("Parents = %v %v", got, ok)
	}
	if got := Tree(Real{PS: func(...string) (string, error) { return "", errors.New("no ps") }}, 7); !reflect.DeepEqual(got, []int{7}) {
		t.Errorf("Tree on unreadable table = %v", got)
	}
}

// One test runs the real ps: the table must list this process under its parent.
func TestRealParentsListsSelf(t *testing.T) {
	ppids, ok := (Real{}).Parents()
	if !ok {
		t.Skip("no ps")
	}
	if pp, found := ppids[selfPID()]; !found || pp != parentPID() {
		t.Errorf("ps lists self under %d (found %v), want %d", pp, found, parentPID())
	}
}
