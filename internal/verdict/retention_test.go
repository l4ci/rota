package verdict

import (
	"context"
	"testing"

	"github.com/l4ci/rota/internal/tracker"
)

const (
	oldSha  = "aaaaaaa"
	headSha = "bbbbbbb"
)

// bury adds more than keep records of other kinds on top of a branch's list.
func bury(t *testing.T, root string) {
	t.Helper()
	kinds := []string{ReviewSpec, ReviewQuality, QA, Behaviour}
	for i := 0; i < keep+5; i++ {
		addAll(t, root, rec(kinds[i%len(kinds)], Pass, headSha))
	}
}

func addAll(t *testing.T, root string, records ...Record) {
	t.Helper()
	for _, r := range records {
		if _, err := AddBranch(root, "feature", r); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRetentionKeepsUnansweredFail(t *testing.T) {
	for _, kind := range []string{LensKind("security"), ReviewCross, SecondOpinion} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			addAll(t, root, rec(kind, Fail, oldSha))
			bury(t, root)
			list := Load(root).Branches["feature"]
			if r, ok := Blocking(list); !ok || r.Kind != kind || r.Sha != oldSha {
				t.Fatalf("unanswered %s FAIL no longer blocks: %+v %v", kind, r, ok)
			}
			if len(list) != keep+1 {
				t.Errorf("history = %d records, want %d", len(list), keep+1)
			}
			// A newer record of the kind answers it, and the old one is cut.
			addAll(t, root, rec(kind, Pass, headSha))
			list = Load(root).Branches["feature"]
			if r, ok := Blocking(list); ok {
				t.Fatalf("answered FAIL still blocks: %+v", r)
			}
			if len(list) != keep {
				t.Errorf("history after the answer = %d records, want %d", len(list), keep)
			}
		})
	}
}

// The gate reads review-cross per harness, so a newer record of one harness
// must not let retention drop another harness's FAIL.
func TestRetentionKeepsCrossFailPerHarness(t *testing.T) {
	root := t.TempDir()
	cross := func(v, harness string) Record {
		r := rec(ReviewCross, v, headSha)
		r.Harness = harness
		return r
	}
	addAll(t, root, cross(Fail, "first"), cross(Pass, "second"))
	bury(t, root)
	if r, ok := BlockingAtHead(Load(root).Branches["feature"], headSha); !ok || r.Harness != "first" {
		t.Fatalf("first harness cross FAIL at head no longer blocks: %+v %v", r, ok)
	}
}

// A recovered report counts only at its head, so a newer one must not let
// retention drop the local FAIL it does not answer elsewhere.
func TestRetentionRecoveredDoesNotAnswerLocal(t *testing.T) {
	root := t.TempDir()
	kind := LensKind("security")
	cached := rec(kind, Pass, "ccccccc")
	cached.FromReport = true
	addAll(t, root, rec(kind, Fail, oldSha), cached)
	bury(t, root)
	list := ForHead(Load(root).Branches["feature"], headSha)
	if r, ok := Blocking(list); !ok || r.Kind != kind || r.Sha != oldSha {
		t.Fatalf("local FAIL no longer blocks: %+v %v", r, ok)
	}
}

func TestRetentionAppliesToRecoveredReports(t *testing.T) {
	root := t.TempDir()
	kind := LensKind("security")
	addAll(t, root, rec(kind, Fail, oldSha))
	for i := 0; i < keep-1; i++ {
		addAll(t, root, rec(QA, Pass, oldSha))
	}
	report := func(k string) tracker.Comment {
		return tracker.Comment{Author: "me", Body: RenderReport(NewRecord(k, Pass, headSha, Body{}, recAt), nil)}
	}
	source := &lookupSource{self: "me", notes: []tracker.Comment{report(ReviewSpec), report(ReviewQuality), report(QA)}}
	if err := RecoverPR(context.Background(), root, "feature", headSha, 20, source); err != nil {
		t.Fatal(err)
	}
	if r, ok := Blocking(ForHead(Load(root).Branches["feature"], headSha)); !ok || r.Kind != kind {
		t.Fatalf("recovery dropped the unanswered FAIL: %+v %v", r, ok)
	}
}
