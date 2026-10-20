package round

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// Seam tests for the step lists of assign and transfer: the plan is made by the
// verb's own planner, the steps are the ones the verb runs, and a failure is
// injected by a step appended after the prefix under test.

func stepNames(steps []step) []string {
	var names []string
	for _, s := range steps {
		names = append(names, s.name)
	}
	return names
}

// assignPlan is the plan and steps of assigning id to agent on the fixture's
// started round.
func (f *moveFx) assignPlan(t *testing.T, id, agent string) []step {
	t.Helper()
	it, err := f.be.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	o := AssignOpts{ID: id, Agent: agent, HolderPID: 100, Settings: f.set}
	res := &Assigned{ID: it.ID, Type: it.Type}
	run, stop, err := f.env.planAssign(bg, f.root, f.be, o, it, nil, f.set.Tier, "", res)
	if err != nil || stop {
		t.Fatalf("plan: stop=%v err=%v", stop, err)
	}
	steps, _, err := f.env.assignSteps(bg, f.root, f.be, o, it, run, registryRound(f.root), res)
	if err != nil {
		t.Fatal(err)
	}
	return steps
}

// transferPlan is the plan and steps of moving id to the slot or the human.
func (f *moveFx) transferPlan(t *testing.T, id, to string) (*transferRun, []step) {
	t.Helper()
	o := TransferOpts{Issue: id, To: to, HolderPID: 100, Settings: f.set}
	res := &Transferred{}
	run, err := f.env.planTransfer(bg, f.root, f.be, o, res)
	if err != nil {
		t.Fatal(err)
	}
	if run.toHuman {
		return run, f.env.transferToHumanSteps(bg, f.root, f.be, o, run, res)
	}
	steps, _, err := f.env.transferToSlotSteps(bg, f.root, f.be, o, run, res)
	if err != nil {
		t.Fatal(err)
	}
	return run, steps
}

var assignMarkSteps = []string{"queue the slot's PR", "claim", "in-progress", "comment",
	"reset the slot onto its branch", "bind the slot", "start the item clock"}

func TestAssignStepsOrder(t *testing.T) {
	f := newMoveFx(t)
	names := stepNames(f.assignPlan(t, "13", "dana"))
	if len(names) <= len(assignMarkSteps) || !reflect.DeepEqual(names[:len(assignMarkSteps)], assignMarkSteps) {
		t.Errorf("marks, then the delivery: %v", names)
	}
}

// A failure after each assign step takes the claim and the state back and
// leaves the slot unbound. The comment, the reset and the item clock have no
// undo: the comment is deduplicated and the rest are overwritten by a retry.
func TestAssignStepFailureCompensates(t *testing.T) {
	boom := errors.New("boom")
	for k := 1; k <= len(assignMarkSteps); k++ {
		name := assignMarkSteps[k-1]
		t.Run(name, func(t *testing.T) {
			f := newMoveFx(t)
			steps := append(f.assignPlan(t, "13", "dana")[:k:k], step{name: "boom", do: func() error { return boom }})
			err := runSteps(steps)
			var se *StageError
			if !errors.As(err, &se) || se.Stage != "boom" || !errors.Is(err, boom) || len(se.Residual) != 0 {
				t.Fatalf("want a clean rollback: %v", err)
			}
			if c := f.be.claims["13"]; c != "" {
				t.Errorf("claim left behind: %q", c)
			}
			if st := f.be.bstates["13"]; st != "" {
				t.Errorf("state left behind: %q", st)
			}
			// The reset step has no undo, so dana may stay on the branch; the binding goes.
			if d := f.slot("dana"); d.Task() != "" || d.ClaimID() != "" {
				t.Errorf("dana still bound: task %q claim %q", d.Task(), d.ClaimID())
			}
		})
	}
}

func TestAssignStepsSkipWhatAResumeAlreadyDid(t *testing.T) {
	f := newMoveFx(t)
	byName := map[string]step{}
	for _, s := range f.assignPlan(t, "12", "ben") { // ben already holds 12
		byName[s.name] = s
	}
	if !byName["comment"].skip() {
		t.Error("a resume posts no second comment")
	}
	if !byName["queue the slot's PR"].skip() {
		t.Error("a slot that holds only this issue queues no PR")
	}
}

func TestTransferToSlotStepsOrder(t *testing.T) {
	f := newMoveFx(t)
	_, steps := f.transferPlan(t, "12", "dana")
	names := stepNames(steps)
	head := []string{"queue the receiver's PR", "park the sender", "handoff comment", "release the old claim",
		"claim", "free the sender", "check out the branch", "bind the receiver"}
	if len(names) < len(head)+1 || !reflect.DeepEqual(names[:len(head)], head) || names[len(names)-1] != "adopt the queued PR" {
		t.Errorf("got %v", names)
	}
}

func TestTransferToHumanStepsOrder(t *testing.T) {
	f := newMoveFx(t)
	_, steps := f.transferPlan(t, "12", HumanTarget)
	want := []string{"queue the receiver's PR", "park the sender", "handoff comment", "release the old claim",
		"reset the state", "needs-human label", "free the sender", "stop the item clock"}
	if got := stepNames(steps); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// Transfer only goes forward: a claim another holder won stops the verb with a
// refusal, and the parked sender and its handoff comment stay for the retry.
func TestTransferClaimStepRefusedKeepsTheHeadSteps(t *testing.T) {
	f := newMoveFx(t)
	_, steps := f.transferPlan(t, "12", "dana")
	f.be.claimedBy = "kit@1"
	const through = 5 // the head and the claim
	err := runSteps(steps[:through])
	if blockedBy(t, err) != BlockClaimed {
		t.Fatalf("got %v", err)
	}
	n := 0
	for _, c := range f.be.comments["12"] {
		if strings.Contains(c, "rota:handoff") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("the head steps are not undone: handoff comments %d", n)
	}
	if h := f.slot("dana").HeldID(); h != "" {
		t.Errorf("dana must not hold 12 after a refused claim: %q", h)
	}
}
