package round

import (
	"strings"
	"testing"
)

func depCheck(t *testing.T, be *fakeRemote, id string) Check {
	t.Helper()
	r, err := Assess(be, id, nil, nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range r.Checks {
		if c.Name == CheckDependencies {
			return c
		}
	}
	t.Fatal("no dependencies check")
	return Check{}
}

// AC-1: comment text never adds a dependency, even when `## Depends on` ends
// the body.
func TestDependsOnEndingTheBodyIgnoresComments(t *testing.T) {
	be := &fakeRemote{comments: map[string][]string{"5": {"see #7 and #5"}}}
	be.add("5", "last section", "", false, "## Acceptance\n- [ ] ok\n\n## Depends on\n")
	be.add("7", "open", "", false, "")
	if c := depCheck(t, be, "5"); !c.OK {
		t.Errorf("comments leaked into Depends on: %v", c.Detail)
	}
}

// AC-2: an item never lists itself as a dependency.
func TestItemIsNeverItsOwnDependency(t *testing.T) {
	be := &fakeRemote{}
	be.add("5", "self ref", "", false, "## Depends on\n- #5\n")
	if c := depCheck(t, be, "5"); !c.OK {
		t.Errorf("self dependency: %v", c.Detail)
	}
}

// AC-3: a trailing `## Depends on` is ready once its entries close.
func TestTrailingDependsOnReadyOnceClosed(t *testing.T) {
	be := &fakeRemote{comments: map[string][]string{"5": {"unrelated #7"}}}
	be.add("5", "x", "", false, "## Depends on\n- #6\n")
	be.add("6", "dep", "", false, "")
	be.add("7", "open", "", false, "")
	if c := depCheck(t, be, "5"); c.OK || !strings.Contains(strings.Join(c.Detail, ";"), "6 is not done") {
		t.Fatalf("open dep should block: %v", c)
	}
	be.items["6"].Closed = true
	if c := depCheck(t, be, "5"); !c.OK {
		t.Errorf("closed dep still blocks: %v", c.Detail)
	}
}
