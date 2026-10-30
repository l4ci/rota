package pidlive

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

func selfPID() int   { return os.Getpid() }
func parentPID() int { return os.Getppid() }

// fixed is a Source whose State answers stat, ok for every pid.
func fixed(stat string, ok bool) Source { return fixedState{stat, ok} }

type fixedState struct {
	stat string
	ok   bool
}

func (f fixedState) State(int) (string, bool)   { return f.stat, f.ok }
func (fixedState) Stat(int) (int, uint64, bool) { return 0, 0, false }
func (fixedState) Comm(int) string              { return "" }
func (fixedState) Cwd(int) string               { return "" }
func (fixedState) Parents() (map[int]int, bool) { return nil, false }

func TestAliveInTable(t *testing.T) {
	self := os.Getpid()
	cases := []struct {
		name string
		pid  int
		st   Source
		want bool
	}{
		{"zero pid", 0, fixed("S", true), false},
		{"negative pid", -4, fixed("S", true), false},
		{"running", self, fixed("S", true), true},
		{"foreground running", self, fixed("R+", true), true},
		{"zombie", self, fixed("Z", true), false},
		{"zombie with flags", self, fixed("Z+", true), false},
		{"ps lists nothing", self, fixed("", true), false},
		{"ps unreadable keeps signal result", self, fixed("", false), true},
		{"no such process", 1 << 22, fixed("S", true), false},
	}
	for _, c := range cases {
		if got := AliveIn(c.st, c.pid); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

// pid 1 belongs to another user for a non-root test run: kill -0 gives EPERM,
// which must still read as alive.
func TestAliveEPERMIsAlive(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root gets no EPERM")
	}
	if !AliveIn(fixed("Ss", true), 1) {
		t.Error("EPERM pid must read as alive")
	}
}

func TestAliveRealZombie(t *testing.T) {
	// The shell backgrounds a child that exits at once and is never reaped
	// while the shell sleeps: the child is a zombie.
	cmd := exec.Command("sh", "-c", "sh -c 'echo $$ >&2' & exec sleep 30")
	errp, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	buf := make([]byte, 32)
	n, _ := errp.Read(buf)
	zpid, err := strconv.Atoi(strings.TrimSpace(string(buf[:n])))
	if err != nil {
		t.Fatalf("zombie pid: %v", err)
	}
	for i := 0; i < 50; i++ {
		if s, _ := (Real{}).State(zpid); strings.HasPrefix(strings.TrimSpace(s), "Z") {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if Alive(zpid) {
		t.Errorf("zombie pid %d must read as exited", zpid)
	}
	if !Alive(cmd.Process.Pid) {
		t.Error("the live parent must read as alive")
	}
}

func TestParseProcStat(t *testing.T) {
	// Fields after comm, starting at state: ppid is the 2nd, starttime the 20th.
	rest := " S 42 1 1 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 987654 0 0"
	cases := []struct {
		name   string
		line   string
		ppid   int
		start  uint64
		wantOK bool
	}{
		{"plain comm", "123 (bash)" + rest, 42, 987654, true},
		{"spaces in comm", "123 (my proc name)" + rest, 42, 987654, true},
		{"paren in comm", "123 (a) b (c)" + rest, 42, 987654, true},
		{"no close paren", "123 bash S 42", 0, 0, false},
		{"truncated after comm", "123 (bash) S 42 1 1 0", 0, 0, false},
		{"non-numeric ppid", "123 (bash) S x 1 1 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 987654 0 0", 0, 0, false},
		{"non-numeric start", "123 (bash) S 42 1 1 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 zz 0 0", 0, 0, false},
		{"empty", "", 0, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pp, st, ok := parseProcStat(c.line)
			if pp != c.ppid || st != c.start || ok != c.wantOK {
				t.Errorf("parseProcStat(%q) = %d, %d, %v; want %d, %d, %v", c.line, pp, st, ok, c.ppid, c.start, c.wantOK)
			}
		})
	}
}

func TestProcStatCurrentPid(t *testing.T) {
	if _, err := os.Stat("/proc/self/stat"); err != nil {
		t.Skip("no /proc")
	}
	ppid, start, ok := (Real{}).Stat(os.Getpid())
	if !ok {
		t.Fatal("ProcStat(self) not ok")
	}
	if ppid != os.Getppid() {
		t.Errorf("ppid = %d, want %d", ppid, os.Getppid())
	}
	if start == 0 {
		t.Error("start ticks = 0")
	}
	if st, ok := StartTicks(os.Getpid()); !ok || st != start {
		t.Errorf("StartTicks = %d, %v; want %d, true", st, ok, start)
	}
	if _, _, ok := (Real{}).Stat(-1); ok {
		t.Error("(Real{}).Stat(-1) ok")
	}
}

func TestCommCurrentPid(t *testing.T) {
	if _, _, ok := (Real{}).Stat(os.Getpid()); !ok {
		t.Skip("no /proc")
	}
	if got := (Real{}).Comm(os.Getpid()); got == "" {
		t.Fatalf("Comm(self) is empty, want the test binary's name")
	}
	if got := (Real{}).Comm(900000003); got != "" {
		t.Fatalf("Comm(missing pid) = %q, want empty", got)
	}
}
