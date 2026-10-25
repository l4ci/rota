package worker

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/exitcode"
	gatepolicy "github.com/l4ci/rota/internal/gate"
	"github.com/l4ci/rota/internal/jsonx"
)

type orderedEscalator struct {
	fakeEscalator
	steps *[]string
}

func (f *orderedEscalator) Pending(th ApprovalThread) (any, bool, error) {
	*f.steps = append(*f.steps, "pending "+th.Ref())
	return f.fakeEscalator.Pending(th)
}

func (f *orderedEscalator) Send(th ApprovalThread, body string) (any, []string, error) {
	*f.steps = append(*f.steps, "send "+th.Ref())
	return f.fakeEscalator.Send(th, body)
}

func TestGateMergeApprovalSequence(t *testing.T) {
	w := newWorld(t, ghURL)
	var steps []string
	esc := &orderedEscalator{steps: &steps}
	ref := refusalWithData()
	policy := gatepolicy.MergePolicy{Mode: gatepolicy.MergePaths, Paths: []string{"*.txt"}}
	res, err := w.gate(false, GateOpts{Approval: &MergeApproval{
		Policy: policy, Escalate: true,
		Clear: func(target string, conf gatepolicy.Confirm, paths []string) (*exitcode.Refusal, error) {
			steps = append(steps, "clear")
			if target != "w1 into main" || conf.Given || !reflect.DeepEqual(paths, []string{"work.txt"}) {
				t.Errorf("approval inputs: target=%q confirm=%+v paths=%v", target, conf, paths)
			}
			return ref, nil
		},
		Escalation: &ApprovalEscalation{Esc: esc, Policy: policy, Warn: t.Errorf},
	}})
	if res.Verdict != GateApprovalRequired || res.Refusal != ref || err != ref.Err || !res.Refused(err) {
		t.Fatalf("approval did not hold the merge: %+v, %v", res, err)
	}
	if !reflect.DeepEqual(steps, []string{"clear", "pending #7", "send #7"}) {
		t.Errorf("approval order = %v", steps)
	}
	if got, _ := ref.Data.(*jsonx.Object).Get("escalation"); got != "entry-#7" {
		t.Errorf("escalation on refusal: %v", got)
	}
	if len(esc.bodies) != 1 || !strings.Contains(esc.bodies[0], "- work.txt\n") {
		t.Errorf("request bodies: %q", esc.bodies)
	}
	if strings.Contains(w.logText(), "PRRequestMerge") {
		t.Error("refused approval merged the PR")
	}
}

func TestGateMergeApprovalResolvesBeforeAudit(t *testing.T) {
	for _, outcome := range []string{"approved", "pending", "unavailable"} {
		t.Run(outcome, func(t *testing.T) {
			w := newWorld(t, ghURL)
			var steps []string
			ref := refusalWithData()
			boom := errors.New("registry unavailable")
			res, err := w.gate(false, GateOpts{Approval: &MergeApproval{
				Policy: gatepolicy.MergePolicy{Mode: gatepolicy.MergeAll}, ID: "e1",
				Resolve: func(id string, th ApprovalThread) (gatepolicy.Confirm, *exitcode.Refusal, error) {
					steps = append(steps, "resolve "+id+" "+th.Ref())
					switch outcome {
					case "pending":
						return gatepolicy.Confirm{}, ref, nil
					case "unavailable":
						return gatepolicy.Confirm{}, nil, boom
					}
					return gatepolicy.Confirm{Given: true, Note: "yes", Escalation: "e1"}, nil, nil
				},
				Clear: func(_ string, conf gatepolicy.Confirm, _ []string) (*exitcode.Refusal, error) {
					steps = append(steps, "audit "+conf.Note)
					if !conf.Given || conf.Escalation != "e1" {
						t.Errorf("resolved confirmation lost: %+v", conf)
					}
					return nil, nil
				},
			}})
			want := []string{"resolve e1 #7"}
			switch outcome {
			case "approved":
				want = append(want, "audit yes")
				if err != nil || res.Verdict != GatePass || !res.Changed {
					t.Errorf("approved merge: %+v %v", res, err)
				}
			case "pending":
				if err != ref.Err || res.Refusal != ref || res.Verdict != GateApprovalRequired || res.Changed {
					t.Errorf("pending approval: %+v %v", res, err)
				}
			case "unavailable":
				if !errors.Is(err, boom) || res.Refusal != nil || res.Verdict != GateApprovalRequired || res.Changed {
					t.Errorf("unavailable registry: %+v %v", res, err)
				}
			}
			if !reflect.DeepEqual(steps, want) {
				t.Errorf("approval order = %v, want %v", steps, want)
			}
			if outcome != "approved" && strings.Contains(w.logText(), "PRRequestMerge") {
				t.Error("unapproved PR merged")
			}
		})
	}
}

func TestGateMergeApprovalSkipsUnusedAdapters(t *testing.T) {
	for _, tc := range []struct {
		name      string
		policy    gatepolicy.MergePolicy
		checkOnly bool
	}{
		{name: "none", policy: gatepolicy.MergePolicy{Mode: gatepolicy.MergeNone}},
		{name: "unmatched paths", policy: gatepolicy.MergePolicy{Mode: gatepolicy.MergePaths, Paths: []string{"*.md"}}},
		{name: "check only", policy: gatepolicy.MergePolicy{Mode: gatepolicy.MergeAll}, checkOnly: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t, ghURL)
			res, err := w.gate(false, GateOpts{CheckOnly: tc.checkOnly, Approval: &MergeApproval{
				Policy: tc.policy, ID: "e1",
				Resolve: func(string, ApprovalThread) (gatepolicy.Confirm, *exitcode.Refusal, error) {
					t.Error("unused approval resolved")
					return gatepolicy.Confirm{}, nil, errors.New("unused adapter")
				},
				Clear: func(string, gatepolicy.Confirm, []string) (*exitcode.Refusal, error) {
					t.Error("unused approval audited")
					return nil, errors.New("unused adapter")
				},
			}})
			want := GatePass
			if tc.checkOnly {
				want = GateFresh
			}
			if err != nil || res.Verdict != want {
				t.Errorf("unused approval blocked merge: %+v %v", res, err)
			}
		})
	}
}

// fakeEscalator is the in-memory forge seam: it records what is sent.
type fakeEscalator struct {
	pending map[string]any // thread ref -> entry
	sent    []ApprovalThread
	bodies  []string
	sendErr error
}

func (f *fakeEscalator) Pending(th ApprovalThread) (any, bool, error) {
	e, ok := f.pending[th.Ref()]
	return e, ok, nil
}

func (f *fakeEscalator) Send(th ApprovalThread, body string) (any, []string, error) {
	if f.sendErr != nil {
		return nil, nil, f.sendErr
	}
	f.sent = append(f.sent, th)
	f.bodies = append(f.bodies, body)
	return "entry-" + th.Ref(), nil, nil
}

func (*fakeEscalator) ReplyWords() []string { return []string{"approve", "yes", "ship it"} }

func refusalWithData() *exitcode.Refusal {
	return &exitcode.Refusal{Err: &exitcode.Error{Exit: exitcode.ExitRefused, Message: "gate"}, Data: jsonx.NewObject()}
}

func TestEscalateApprovalReusesPendingAndWarnsOnFailure(t *testing.T) {
	th := ApprovalThread{Kind: "pr", Number: 4}
	var warned []string
	warn := func(f string, a ...any) { warned = append(warned, f) }
	esc := &fakeEscalator{pending: map[string]any{"#4": "old"}}
	a := ApprovalEscalation{Esc: esc, Policy: gatepolicy.MergePolicy{Mode: gatepolicy.MergeAll}, Warn: warn}
	if got := a.EscalateApproval(nil, th); got != "old" || len(esc.sent) != 0 {
		t.Errorf("pending not reused: %v %v", got, esc.sent)
	}
	esc = &fakeEscalator{sendErr: errors.New("boom")}
	a.Esc = esc
	if got := a.EscalateApproval(nil, th); got != nil || len(warned) != 1 {
		t.Errorf("send failure: %v %v", got, warned)
	}
}

func TestApprovalRequestBodyAllPolicy(t *testing.T) {
	b := ApprovalRequestBody(gatepolicy.MergePolicy{Mode: gatepolicy.MergeAll}, nil, []string{"approve", "ship it"})
	if !strings.Contains(b, "Merge policy `all`: every merge needs approval.") || !strings.Contains(b, "Reply `approve` or `ship it` to merge.") {
		t.Errorf("body %q", b)
	}
}

func TestTargetApprovalThread(t *testing.T) {
	th, err := TargetApprovalThread(GateTarget{Name: "ben", Branch: "ben/PROJ-123-widen", Issue: "PROJ-123"})
	if err != nil || th.Kind != "issue" || th.Key != "PROJ-123" || th.Number != 0 || th.Ref() != "PROJ-123" || th.Slot != "ben" {
		t.Fatalf("key issue: %+v %v", th, err)
	}
	if num, err := TargetApprovalThread(GateTarget{Name: "ben", Issue: "804"}); err != nil || num.Key != "" || num.Number != 804 || num.Ref() != "#804" {
		t.Errorf("numbered issue: %+v %v", num, err)
	}
	if _, err := TargetApprovalThread(GateTarget{Name: "ben", Issue: "B07"}); err == nil {
		t.Error("an item id is not a thread")
	}
	// the PR wins over the issue
	if pr, err := TargetApprovalThread(GateTarget{Name: "a", PR: "https://github.com/o/r/pull/42", Issue: "5"}); err != nil || pr.Kind != "pr" || pr.Number != 42 {
		t.Errorf("pr: %+v %v", pr, err)
	}
}

func TestSlotApprovalThread(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".rota"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".rota", "workers.json"), []byte(`{"slots":[
{"name":"a","branch":"a/5-x","pr":"https://github.com/o/r/pull/42"},
{"name":"b","task":"9","branch":"b/3-y"},
{"name":"c","branch":"c/3-y"},
{"name":"d","branch":"park/d"},
{"name":"e","task":"do things","branch":"e/x"},
{"name":"f","branch":"f/3-y","pr":"https://github.com/o/r/pull/9"}],
"prs":[{"issue":"5","branch":"g/5-x","pr":"https://github.com/o/r/pull/77","from":"g"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		ref, kind string
		n         int
		title     string
		slot      string
	}{
		{"a", "pr", 42, "Merge approval: PR #42", "a"},
		{"b", "issue", 9, "Merge approval: b (b/3-y)", "b"},
		{"c", "issue", 3, "Merge approval: c (c/3-y)", "c"},
		// a queued PR record: its slot has moved on, so the thread names none
		{"#77", "pr", 77, "Merge approval: PR #77", ""},
		{"77", "pr", 77, "Merge approval: PR #77", ""},
		{"#9", "pr", 9, "Merge approval: PR #9", "f"},
		// an unrecorded PR is gated by number
		{"#123", "pr", 123, "Merge approval: PR #123", ""},
	} {
		th, err := SlotApprovalThread(root, c.ref)
		if err != nil || th.Kind != c.kind || th.Number != c.n || th.Title != c.title || th.Slot != c.slot {
			t.Errorf("%s: %+v %v", c.ref, th, err)
		}
	}
	for _, s := range []string{"d", "e"} {
		if _, err := SlotApprovalThread(root, s); err == nil || !strings.Contains(err.Error(), "approval thread") {
			t.Errorf("%s: %v", s, err)
		}
	}
	if _, err := SlotApprovalThread(root, "zz"); err == nil {
		t.Error("unknown slot")
	}
}
