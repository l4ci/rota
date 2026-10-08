package pidlive

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

func fixed(stat string, ok bool) State {
	return func(int) (string, bool) { return stat, ok }
}

func TestAliveWithTable(t *testing.T) {
	self := os.Getpid()
	cases := []struct {
		name string
		pid  int
		st   State
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
		if got := AliveWith(c.st, c.pid); got != c.want {
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
	if !AliveWith(fixed("Ss", true), 1) {
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
		if s, _ := psState(zpid); strings.HasPrefix(strings.TrimSpace(s), "Z") {
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
