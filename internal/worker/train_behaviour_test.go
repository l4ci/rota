package worker

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/verdict"
)

// #915: the merge train runs each member's `## Try it` steps in its scratch
// tree after test.full and test.e2e pass, as the gate does for one PR (#899).

// behaviourTrain is a trainWorld whose slot bN holds item N, with bodies[N-1]
// as that item's Try it text.
func behaviourTrain(t *testing.T, bodies ...string) (*world, func(TrainOpts) (TrainResult, error)) {
	t.Helper()
	var branches, slots []string
	for i := range bodies {
		b := fmt.Sprintf("b%d", i+1)
		branches = append(branches, b)
		slots = append(slots, fmt.Sprintf(`{"name":%q,"branch":%q,"task":"%d"}`, b, b, i+1))
	}
	w := trainWorld(t, "true", branches...)
	w.setConfig(`{"issues":{"source":"file"},"test":{"full":["true"]}}`)
	os.WriteFile(filepath.Join(w.dir, ".rota", "workers.json"), []byte(`{"slots":[`+strings.Join(slots, ",")+`]}`), 0o644)
	return w, func(o TrainOpts) (TrainResult, error) {
		o.Targets = branches
		if o.ItemBody != nil {
			return w.train(o)
		}
		o.ItemBody = func(issue string) (string, bool, error) {
			var n int
			if _, err := fmt.Sscanf(issue, "%d", &n); err != nil || n < 1 || n > len(bodies) {
				return "", false, fmt.Errorf("asked for %q", issue)
			}
			return bodies[n-1], true, nil
		}
		return w.train(o)
	}
}

func trainHasNote(res TrainResult, sub string) bool {
	for _, n := range res.Notes {
		if strings.Contains(n, sub) {
			return true
		}
	}
	return false
}

func TestTrainBehaviourRunsEachMemberInTheScratchTree(t *testing.T) {
	w, train := behaviourTrain(t,
		"## Try it\n\n- test -f b1.txt && case \"$PWD\" in *rota-train-*) echo scratch;; esac\n  expect: scratch\n",
		"## Try it\n\n- test -f b1.txt && test -f b2.txt && ! test -f b3.txt\n",
		"## Try it\n\n- test -f b1.txt && test -f b2.txt && test -f b3.txt\n")
	res, err := train(TrainOpts{})
	if err != nil || res.Verdict != GatePass || strings.Join(res.Landed, ",") != "b1,b2,b3" {
		t.Fatalf("%+v %v", res, err)
	}
	for _, slot := range []string{"b1", "b2", "b3"} {
		if !trainHasNote(res, "BEHAVIOUR "+slot+" — PASS") {
			t.Errorf("no BEHAVIOUR PASS note for %s: %q", slot, res.Notes)
		}
		rec, ok := verdict.Latest(verdict.Load(w.dir).Branches[slot], verdict.Behaviour)
		if !ok || rec.Verdict != "PASS" || rec.Branch != slot {
			t.Errorf("behaviour verdict of %s: %+v %v", slot, rec, ok)
		}
	}
}

func TestTrainBehaviourFailureNamesTheCulpritAndLandsNothing(t *testing.T) {
	w, train := behaviourTrain(t,
		"## Try it\n\n- echo fine\n",
		"## Try it\n\n- echo boom; exit 3\n",
		"## Try it\n\n- echo fine\n")
	res, err := train(TrainOpts{})
	if err != nil || res.Verdict != GateVerifyFailed || res.Culprit != "b2" || len(res.Landed) != 0 || res.Changed {
		t.Fatalf("%+v %v", res, err)
	}
	if !strings.Contains(res.Err, "fails Try it step 1") || w.onMain("b1.txt") {
		t.Errorf("err %q, b1 on main %v", res.Err, w.onMain("b1.txt"))
	}
	if !res.Members[1].Culprit || res.Members[0].Culprit {
		t.Errorf("members %+v", res.Members)
	}
	rec, ok := verdict.Latest(verdict.Load(w.dir).Branches["b2"], verdict.Behaviour)
	if !ok || rec.Verdict != "FAIL" {
		t.Errorf("b2 behaviour verdict: %+v %v", rec, ok)
	}
	if _, ok := verdict.Latest(verdict.Load(w.dir).Branches["b3"], verdict.Behaviour); ok {
		t.Error("b3's steps ran after b2 failed")
	}
}

func TestTrainBehaviourLandGreenLandsOnlyTheMembersBeforeTheCulprit(t *testing.T) {
	w, train := behaviourTrain(t,
		"## Try it\n\n- echo fine\n",
		"## Try it\n\n- echo boom; exit 3\n",
		"## Try it\n\n- echo fine\n")
	res, err := train(TrainOpts{LandGreen: true})
	if err != nil || res.Verdict != GateVerifyFailed || res.Culprit != "b2" || strings.Join(res.Landed, ",") != "b1" {
		t.Fatalf("%+v %v", res, err)
	}
	if !w.onMain("b1.txt") || w.onMain("b2.txt") || w.onMain("b3.txt") {
		t.Errorf("main has b1=%v b2=%v b3=%v", w.onMain("b1.txt"), w.onMain("b2.txt"), w.onMain("b3.txt"))
	}
}

func TestTrainBehaviourFirstMemberFailingLandsNothingUnderLandGreen(t *testing.T) {
	w, train := behaviourTrain(t,
		"## Try it\n\n- exit 1\n",
		"## Try it\n\n- echo fine\n")
	res, err := train(TrainOpts{LandGreen: true})
	if err != nil || res.Verdict != GateVerifyFailed || res.Culprit != "b1" || len(res.Landed) != 0 || w.onMain("b1.txt") {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestTrainBehaviourFailureReplacesAnEarlierBisectCulprit(t *testing.T) {
	w, train := behaviourTrain(t,
		"## Try it\n\n- echo fine\n",
		"## Try it\n\n- exit 1\n",
		"## Try it\n\n- echo fine\n")
	w.setConfig(`{"issues":{"source":"file"},"test":{"full":["test ! -f b3.txt"]}}`)
	res, err := train(TrainOpts{LandGreen: true})
	if err != nil || res.Culprit != "b2" || strings.Join(res.Landed, ",") != "b1" {
		t.Fatalf("%+v %v", res, err)
	}
	var culprits []string
	for _, m := range res.Members {
		if m.Culprit {
			culprits = append(culprits, m.Target)
		}
	}
	if strings.Join(culprits, ",") != "b2" {
		t.Errorf("members flagged culprit: %v", culprits)
	}
}

func TestTrainBehaviourStepFilesDoNotReachTheNextMember(t *testing.T) {
	_, train := behaviourTrain(t,
		"## Try it\n\n- touch leak.txt\n",
		"## Try it\n\n- test ! -f leak.txt\n")
	res, err := train(TrainOpts{})
	if err != nil || res.Verdict != GatePass || strings.Join(res.Landed, ",") != "b1,b2" {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestTrainBehaviourHeadMovedBeforeTheRunEndsTheTrain(t *testing.T) {
	w, train := behaviourTrain(t,
		"## Try it\n\n- echo fine\n",
		"## Try it\n\n- echo fine\n")
	moved := false
	res, err := train(TrainOpts{ItemBody: func(issue string) (string, bool, error) {
		if issue == "2" && !moved { // b2's gate is reading its item: push a commit to b2
			moved = true
			c := gitq(t, w.dir, "commit-tree", "b2^{tree}", "-p", "b2", "-m", "late")
			gitq(t, w.dir, "update-ref", "refs/heads/b2", strings.TrimSpace(c))
		}
		if issue == "2" { // its steps fail, but on a head they never ran on: base-moved, not verify-failed
			return "## Try it\n\n- exit 1\n", true, nil
		}
		return "## Try it\n\n- echo fine\n", true, nil
	}})
	if err != nil || res.Verdict != GateBaseMoved || res.Culprit != "b2" || len(res.Landed) != 0 || w.onMain("b1.txt") {
		t.Fatalf("%+v %v", res, err)
	}
}
