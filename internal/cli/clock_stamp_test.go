package cli

import (
	"strings"
	"testing"
	"time"
)

// pinnedDeps pins Today and Now to different days no wall clock reaches during
// the test run, so a verb reading the wrong clock fails its assertion.
func pinnedDeps() *Deps {
	d := testDeps()
	d.Today = func() time.Time { return time.Date(2031, 4, 5, 9, 0, 0, 0, time.UTC) }
	d.Now = func() time.Time { return time.Date(2031, 4, 6, 9, 30, 0, 0, time.UTC) }
	return d
}

func TestDesignAddStampsDepsToday(t *testing.T) {
	root := trackerProject(t, "")
	deps := pinnedDeps()
	if code, _, se := rotaInWith(t, deps, root, "design", "add", "B01", "--title", "T"); code != 0 {
		t.Fatalf("add: %d %s", code, se)
	}
	_, out, _ := rotaInWith(t, deps, root, "design", "show", "B01")
	if !strings.Contains(out, "2031-04-05") {
		t.Errorf("design not stamped from Deps.Today:\n%s", out)
	}
}

func TestPlanAddStampsDepsToday(t *testing.T) {
	root := trackerProject(t, "")
	deps := pinnedDeps()
	if code, _, se := rotaInWith(t, deps, root, "plan", "add", "M01-B01", "--title", "T"); code != 0 {
		t.Fatalf("add: %d %s", code, se)
	}
	_, out, _ := rotaInWith(t, deps, root, "plan", "show", "M01-B01")
	if !strings.Contains(out, "created: 2031-04-05") {
		t.Errorf("plan not stamped from Deps.Today:\n%s", out)
	}
}

func TestSpikeStampsDepsToday(t *testing.T) {
	dir := gitRepo(t)
	deps := pinnedDeps()
	if code, _, se := rotaInWith(t, deps, dir, "spike", "add", "sse", "--question", "q"); code != 0 {
		t.Fatalf("add: %d %s", code, se)
	}
	_, out, _ := rotaInWith(t, deps, dir, "spike", "show", "sse")
	if !strings.Contains(out, "created: 2031-04-05") {
		t.Errorf("spike add not stamped from Deps.Today:\n%s", out)
	}
	deps.Today = func() time.Time { return time.Date(2032, 6, 7, 9, 0, 0, 0, time.UTC) }
	if code, _, se := rotaInWith(t, deps, dir, "spike", "finish", "sse"); code != 0 {
		t.Fatalf("finish: %d %s", code, se)
	}
	_, out, _ = rotaInWith(t, deps, dir, "spike", "show", "sse")
	if !strings.Contains(out, "finished: 2032-06-07") {
		t.Errorf("spike finish not stamped from Deps.Today:\n%s", out)
	}
}

func TestContradictionAddStampsDepsNow(t *testing.T) {
	root := trackerProject(t, "")
	deps := pinnedDeps()
	if code, _, se := rotaInWith(t, deps, root, "knowledge", "contradiction", "add", "--topic", "A", "--title", "B", "--text", "c"); code != 0 {
		t.Fatalf("add: %d %s", code, se)
	}
	_, out, _ := rotaInWith(t, deps, root, "knowledge", "contradiction", "list", "--json")
	if !strings.Contains(out, "2031-04-06T09:30:00Z") {
		t.Errorf("loggedAt not stamped from Deps.Now:\n%s", out)
	}
}
