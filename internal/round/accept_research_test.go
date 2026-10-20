package round

import (
	"strings"
	"testing"

	ms "github.com/l4ci/rota/internal/milestone"
	"github.com/l4ci/rota/internal/worker"
)

// researchDone makes issue 12 a research item ben holds and reports done. The
// finding comment is posted after the assignment unless before is set.
func (f *moveFx) researchDone(t *testing.T, finding, before bool) {
	t.Helper()
	f.be.items["12"].Labels = []string{"decision:research"}
	if finding && before {
		f.be.comments["12"] = append([]string{"early finding"}, f.be.comments["12"]...)
		f.be.kinds["12"] = append([]string{"finding"}, f.be.kinds["12"]...)
	}
	if finding && !before {
		if _, err := f.be.AddComment("12", "finding", "the answer is X"); err != nil {
			t.Fatal(err)
		}
	}
	if err := editSlot(f.root, "ben", func(s *worker.Slot) error { return s.MarkState("done", "") }); err != nil {
		t.Fatal(err)
	}
}

func TestAcceptResearchClosesWithFinding(t *testing.T) {
	f := newMoveFx(t)
	f.researchDone(t, true, false)

	res, err := f.accept("12", func(in *AcceptInput) { in.Gist = "use X" })
	if err != nil || !res.Changed || res.Slot != "ben" {
		t.Fatalf("%+v %v", res, err)
	}
	if !f.be.items["12"].Closed || f.slot("ben").Task() != "" || f.be.claims["12"] != "" {
		t.Errorf("closed, slot freed, claim released: %v %v %v", f.be.items["12"].Closed, f.slot("ben").Task(), f.be.claims["12"])
	}
	if got := strings.Join(f.be.comments["12"], "|"); !strings.Contains(got, "Finding accepted: use X.") {
		t.Errorf("accept note: %s", got)
	}
}

func TestAcceptResearchRefusals(t *testing.T) {
	t.Run("no finding", func(t *testing.T) {
		f := newMoveFx(t)
		f.researchDone(t, false, false)
		_, err := f.accept("12", func(in *AcceptInput) { in.Gist = "g" })
		if blockedBy(t, err) != BlockFindingMissing || f.be.items["12"].Closed {
			t.Fatalf("%v", err)
		}
	})
	t.Run("finding posted before the assignment", func(t *testing.T) {
		f := newMoveFx(t)
		f.researchDone(t, true, true)
		_, err := f.accept("12", func(in *AcceptInput) { in.Gist = "g" })
		if blockedBy(t, err) != BlockFindingMissing || f.be.items["12"].Closed {
			t.Fatalf("%v", err)
		}
	})
	t.Run("no gist", func(t *testing.T) {
		f := newMoveFx(t)
		f.researchDone(t, true, false)
		_, err := f.accept("12", nil)
		if blockedBy(t, err) != BlockNoGist || !strings.Contains(err.Error(), "--gist") || f.be.items["12"].Closed {
			t.Fatalf("%v", err)
		}
	})
	t.Run("ordinary item", func(t *testing.T) {
		f := newMoveFx(t)
		f.researchDone(t, true, false)
		f.be.items["12"].Labels = nil
		_, err := f.accept("12", func(in *AcceptInput) { in.Gist = "g" })
		if err == nil || !strings.Contains(err.Error(), "needs a PR") || f.be.items["12"].Closed {
			t.Fatalf("%v", err)
		}
	})
	t.Run("grilling item", func(t *testing.T) {
		f := newMoveFx(t)
		f.researchDone(t, true, false)
		f.be.items["12"].Labels = []string{"decision:grilling"}
		_, err := f.accept("12", func(in *AcceptInput) { in.Gist = "g" })
		if err == nil || f.be.items["12"].Closed {
			t.Fatalf("%v", err)
		}
	})
}

func TestAcceptResearchTwiceChangesNothing(t *testing.T) {
	f := newMoveFx(t)
	f.researchDone(t, true, false)
	in := func(in *AcceptInput) { in.Gist = "g" }
	if _, err := f.accept("12", in); err != nil {
		t.Fatal(err)
	}
	n := len(f.be.comments["12"])
	res, err := f.accept("12", in)
	if err != nil || res.Changed || len(f.be.comments["12"]) != n {
		t.Fatalf("rerun: %+v %v", res, err)
	}
}

// storeBoard gives a Board the milestone store of a file-mode project.
type storeBoard struct {
	Board
	st ms.Store
}

func (b storeBoard) MilestoneStore() ms.Store { return b.st }

func TestAcceptResearchLogsTheDecision(t *testing.T) {
	f := newMoveFx(t)
	f.researchDone(t, true, false)
	st := ms.FileStore{Root: t.TempDir()}
	mid, err := st.Add("Fog", "Find out.", nil)
	if err != nil {
		t.Fatal(err)
	}
	f.be.items["12"].Fields.Milestone = mid
	be := storeBoard{Board: f.be, st: st}
	for i := 0; i < 2; i++ { // the second run is a retry after a lost response
		if _, err := f.env.Accept(bg, f.root, be, AcceptInput{ID: "12", Gist: "use X"}); err != nil {
			t.Fatal(err)
		}
	}
	text, _ := st.Show(mid)
	if n := strings.Count(text, "- #12: use X"); n != 1 {
		t.Errorf("one decision line, got %d:\n%s", n, text)
	}
}
