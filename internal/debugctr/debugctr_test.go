package debugctr

import (
	"errors"
	"github.com/l4ci/rota/internal/exitcode"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sync"
	"sync/atomic"
	"testing"
)

func repo(t *testing.T) string {
	t.Helper()
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	for _, a := range [][]string{{"init", "-q", "-b", "feat/x"}, {"-c", "user.email=a@b", "-c", "user.name=n", "commit", "-q", "--allow-empty", "-m", "i"}} {
		c := exec.Command("git", a...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", a, err, out)
		}
	}
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
	c, err := Open(repo(t))
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
	c, _ := Open(repo(t))
	c.Init("B07")
	long := ""
	for i := 0; i < 130; i++ {
		long += "é"
	}
	c.RecordAttempt(long, "abc")
	c.Fail()
	st, _ := c.State()
	md, bug, failed := c.Summary(st)
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
	c, _ := Open(repo(t))
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
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	if _, err := Open(dir); exitOf(err) != 5 {
		t.Errorf("got %v", err)
	}
}

// Two concurrent inits: exactly one may report created.
func TestInitConcurrent(t *testing.T) {
	c, _ := Open(repo(t))
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
