package worker

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/exitcode"
	gatepolicy "github.com/l4ci/rota/internal/gate"
	"github.com/l4ci/rota/internal/jsonx"
)

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

// A refusal under a `paths` policy sends the request on the PR's thread, naming
// the files that put the merge under the policy, and puts the entry on the
// refusal's data.
func TestGateRefusalEscalatesOnPathsPolicy(t *testing.T) {
	w := newWorld(t, ghURL)
	esc := &fakeEscalator{}
	var warned []string
	ref := refusalWithData()
	res, err := w.gate(false, GateOpts{
		Approve: func(GateTarget, func() ([]string, error)) (*exitcode.Refusal, error) { return ref, nil },
		Escalate: &ApprovalEscalation{Esc: esc, Policy: gatepolicy.MergePolicy{Mode: gatepolicy.MergePaths, Paths: []string{"work.txt"}},
			Warn: func(f string, a ...any) { warned = append(warned, f) }},
	})
	if res.Verdict != GateApprovalRequired || err != ref.Err {
		t.Fatalf("verdict %q err %v", res.Verdict, err)
	}
	if len(esc.sent) != 1 || esc.sent[0].Kind != "pr" || esc.sent[0].Number != 7 {
		t.Fatalf("sent %+v", esc.sent)
	}
	if b := esc.bodies[0]; !strings.Contains(b, "Merge policy `paths`") || !strings.Contains(b, "- work.txt") || !strings.Contains(b, "`ship it`") {
		t.Errorf("body %q", b)
	}
	if got, _ := ref.Data.(*jsonx.Object).Get("escalation"); got != "entry-#7" {
		t.Errorf("escalation on data: %v", got)
	}
	if len(warned) != 0 {
		t.Errorf("warned %v", warned)
	}
}

// Without Escalate the refusal stands and nothing is sent.
func TestGateRefusalWithoutEscalateSendsNothing(t *testing.T) {
	w := newWorld(t, ghURL)
	ref := refusalWithData()
	res, _ := w.gate(false, GateOpts{Approve: func(GateTarget, func() ([]string, error)) (*exitcode.Refusal, error) { return ref, nil }})
	if res.Verdict != GateApprovalRequired {
		t.Fatalf("verdict %q", res.Verdict)
	}
	if _, ok := ref.Data.(*jsonx.Object).Get("escalation"); ok {
		t.Error("escalation set without Escalate")
	}
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
