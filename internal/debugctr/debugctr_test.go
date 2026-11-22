package debugctr

import (
	"errors"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/gittest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func repo(t *testing.T) string {
	t.Helper()
	dir := gittest.TempDir(t)
	gittest.Init(t, dir, "feat/x")
	gittest.Run(t, dir, "commit", "-q", "--allow-empty", "-m", "i")
	os.MkdirAll(filepath.Join(dir, ".rota"), 0o777)
	return dir
}

func exitOf(err error) int {
	var ae *exitcode.Error
	if errors.As(err, &ae) {
		return ae.Exit
	}
	return -1
}

var ts = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d{6})?\+00:00`)

// The state file is shared with older plugin versions, so its bytes are pinned:
// key order, two-space indent, \u escapes, trailing newline, ISO timestamps.
func TestStateFileBytes(t *testing.T) {
	c, err := Open(repo(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	if created, err := c.Init("B07"); err != nil || !created {
		t.Fatalf("init: %v %v", created, err)
	}
	c.RecordAttempt("héllo", "abc")
	if _, err := c.Fail(); err != nil {
		t.Fatal(err)
	}
	c.RecordAttempt("h2", "def")
	c.Pass()
	c.IncCycle()
	raw, _ := os.ReadFile(c.Path)
	got := ts.ReplaceAllString(string(raw), "TS")
	want := `{
  "session": "feat-x",
  "bug_id": "B07",
  "started_at": "TS",
  "failed_fixes": 1,
  "hypothesis_cycles": 1,
  "attempts": [
    {
      "n": 1,
      "started_at": "TS",
      "hypothesis": "h` + "\\u00e9" + `llo",
      "commit": "abc",
      "outcome": "failed",
      "ended_at": "TS"
    },
    {
      "n": 2,
      "started_at": "TS",
      "hypothesis": "h2",
      "commit": "def",
      "outcome": "passed",
      "ended_at": "TS"
    }
  ]
}
`
	if got != want {
		t.Fatalf("state file differs:\n%s\nwant:\n%s", got, want)
	}
	if !ts.MatchString(string(raw)) {
		t.Error("timestamps are not Python isoformat")
	}
}

func TestSummaryBytes(t *testing.T) {
	c, _ := Open(repo(t), nil)
	c.Init("B07")
	long := ""
	for i := 0; i < 130; i++ {
		long += "é"
	}
	c.RecordAttempt(long, "abc")
	c.Fail()
	st, _ := c.Load()
	md, bug, failed := st.Summary()
	trunc := ""
	for i := 0; i < 117; i++ {
		trunc += "é"
	}
	want := "# Iron Law triggered for [B07]\n\n1 fix attempt failed to resolve the bug. Halting.\n\n## Attempts\n\n1. abc — " + trunc + "...\n\n## Next steps\n\n" +
		"- Run `/rota-pause` to leave a handoff note and step away.\n" +
		"- Or re-read the symptom — the root cause is likely in a different subsystem than the hypotheses so far have explored.\n" +
		"- The failed-fix count is per item and survives a new branch; only `rota debug reset B07`, after a human approves it, starts it again."
	if md != want || bug != "B07" || failed != 1 {
		t.Fatalf("summary:\n%s", md)
	}
}

func TestMissingSessionIsExit3(t *testing.T) {
	c, _ := Open(repo(t), nil)
	if _, err := c.RecordAttempt("h", "c"); exitOf(err) != 3 {
		t.Errorf("record-attempt: %v", err)
	}
	if _, err := c.IncCycle(); exitOf(err) != 3 {
		t.Errorf("inc-cycle: %v", err)
	}
	if _, err := c.Fail(); exitOf(err) != 3 {
		t.Errorf("fail: %v", err)
	}
	if _, err := c.Pass(); exitOf(err) != 3 {
		t.Errorf("pass: %v", err)
	}
	if _, err := c.Raw(); exitOf(err) != 3 {
		t.Errorf("raw: %v", err)
	}
}

func TestOpenOutsideGitIsExit5(t *testing.T) {
	dir := gittest.TempDir(t)
	if _, err := Open(dir, nil); exitOf(err) != 5 {
		t.Errorf("got %v", err)
	}
}

// Two concurrent inits: exactly one may report created.
func TestInitConcurrent(t *testing.T) {
	c, _ := Open(repo(t), nil)
	var created atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, err := c.Init("B07"); err != nil {
				t.Error(err)
			} else if ok {
				created.Add(1)
			}
		}()
	}
	wg.Wait()
	if created.Load() != 1 {
		t.Fatalf("%d inits reported created, want 1", created.Load())
	}
}

// TestTimestampsComeFromTheInjectedClock fixes the clock and asserts the
// stamped bytes; the microsecond rule is tested in stamp.
func TestTimestampsComeFromTheInjectedClock(t *testing.T) {
	c, err := Open(repo(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	c.Now = func() time.Time { return time.Date(2031, 7, 9, 23, 59, 1, 0, time.UTC) }
	if _, err := c.Init("B07"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(c.Path)
	if !strings.Contains(string(raw), `"started_at": "2031-07-09T23:59:01+00:00"`) {
		t.Errorf("init did not stamp the clock:\n%s", raw)
	}
}

func TestPendingAttemptRule(t *testing.T) {
	c, _ := Open(repo(t), nil)
	if _, _, err := c.PendingAttempt(); exitOf(err) != 3 {
		t.Fatalf("no session file: %v", err)
	}
	c.Init("B07")
	if _, ok, err := c.PendingAttempt(); err != nil || ok {
		t.Fatalf("no attempts: ok=%v err=%v", ok, err)
	}
	c.RecordAttempt("h1", "c1")
	a, ok, _ := c.PendingAttempt()
	if !ok || a.N != 1 || a.Hypothesis != "h1" || a.Commit != "c1" || a.Outcome != "pending" {
		t.Fatalf("open attempt: ok=%v %+v", ok, a)
	}
	c.Fail()
	if _, ok, _ := c.PendingAttempt(); ok {
		t.Fatal("a closed last attempt is not pending")
	}
	c.RecordAttempt("h2", "c2")
	if a, ok, _ := c.PendingAttempt(); !ok || a.N != 2 {
		t.Fatalf("second attempt: ok=%v %+v", ok, a)
	}
}

func TestBugIDAndCycles(t *testing.T) {
	c, _ := Open(repo(t), nil)
	if _, err := c.BugID(); exitOf(err) != 3 {
		t.Fatalf("bug id without file: %v", err)
	}
	if _, err := c.HypothesisCycles(); exitOf(err) != 3 {
		t.Fatalf("cycles without file: %v", err)
	}
	c.Init("B07")
	c.IncCycle()
	c.IncCycle()
	if id, err := c.BugID(); err != nil || id != "B07" {
		t.Fatalf("bug id %q %v", id, err)
	}
	if n, err := c.HypothesisCycles(); err != nil || n != 2 {
		t.Fatalf("cycles %d %v", n, err)
	}
}
