package worker

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/blockedby"
	"github.com/l4ci/rota/internal/exitcode"
	gatepolicy "github.com/l4ci/rota/internal/gate"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/tracker"
)

// ApprovalThread is the forge thread a merge's approval request lives on (C5).
type ApprovalThread struct {
	Kind   string           // pr | issue
	Number int              // the PR, or a numbered issue
	Key    tracker.IssueKey // a key-based tracker's issue; empty for a numbered one
	Slot   string
	Title  string
}

// MergeApproval holds the merge policy and the adapters needed to enforce it.
// Worker owns the order; callers supply confirmation, audit and forge I/O.
type MergeApproval struct {
	Policy   gatepolicy.MergePolicy
	Confirm  gatepolicy.Confirm
	ID       string
	Escalate bool
	// Answers reads the escalation behind ID. Required only with ID.
	Answers ApprovalReader
	// Now is the clock for the status a refusal reports. Required only with ID.
	Now func() time.Time
	// Extra is the verb's own envelope, which a refusal of ID carries first.
	Extra *jsonx.Object
	// Clear checks and audits confirmation. Required when the policy covers
	// the merge; a refusal is a value, an error is a step that could not run.
	Clear func(target string, conf gatepolicy.Confirm, paths []string) (*exitcode.Refusal, error)
	// Escalation supplies thread I/O. Required only with Escalate.
	Escalation *ApprovalEscalation
}

// Check applies the policy before resolving a thread or approval, then clears
// the gate and escalates a refusal when requested. Files and thread stay lazy:
// an uncovered merge performs no approval I/O and writes no audit entry.
func (a MergeApproval) Check(target string, files func() ([]string, error), thread func() (ApprovalThread, error)) (*exitcode.Refusal, error) {
	var changed []string
	if a.Policy.NeedsFiles() {
		var err error
		if changed, err = files(); err != nil {
			return nil, err
		}
	}
	covered, hit := a.Policy.Covers(changed)
	if !covered {
		return nil, nil
	}
	var th ApprovalThread
	if a.ID != "" || a.Escalate {
		var err error
		if th, err = thread(); err != nil {
			return nil, err
		}
	}
	conf := a.Confirm
	if a.ID != "" {
		var ref *exitcode.Refusal
		var err error
		if conf, ref, err = a.Confirmation(a.ID, th); ref != nil || err != nil {
			return ref, err
		}
	}
	ref, err := a.Clear(target, conf, hit)
	if ref != nil && a.Escalate {
		a.Escalation.Attach(ref, hit, th)
	}
	return ref, err
}

// ApprovalEntry is the part of an escalation the approval rule reads: the
// escalation package owns the record, the gate needs only its thread, its
// answer and its status as reported.
type ApprovalEntry struct {
	Kind, Ref string
	Answered  bool                       // stored status is answered
	Answer    *string                    // the answer's body, nil when unanswered
	Status    func(now time.Time) string // the status as reported (pending, timed-out, answered)
}

// ApprovalReader is the thread-reading seam of --approval: the escalation
// registry as the cli wires it, a test a fake.
type ApprovalReader interface {
	// Entry is the escalation with that id; ok is false when there is none.
	Entry(id string) (entry ApprovalEntry, ok bool, err error)
	// Approves reports whether an answer's body approves the merge.
	Approves(answer string) bool
}

// Confirmation turns --approval <id> into the confirmation the gate takes:
// the answered escalation on this merge's thread whose answer approves. A
// refusal is a value, an error is a step that could not run.
func (a MergeApproval) Confirmation(id string, th ApprovalThread) (gatepolicy.Confirm, *exitcode.Refusal, error) {
	e, ok, err := a.Answers.Entry(id)
	if err != nil {
		return gatepolicy.Confirm{}, nil, err
	}
	switch {
	case !ok:
		return gatepolicy.Confirm{}, approvalError(exitcode.ExitResolution, "no escalation %s", id), nil
	case e.Kind != th.Kind || e.Ref != th.Ref():
		return gatepolicy.Confirm{}, approvalError(exitcode.ExitUsage, "escalation %s is on %s %s, not on this merge's approval thread (%s %s)", id, e.Kind, e.Ref, th.Kind, th.Ref()), nil
	case !e.Answered:
		status := e.Status(a.Now())
		return gatepolicy.Confirm{}, a.refuse(fmt.Sprintf("escalation %s is %s; no approval yet", id, status),
			"blockedBy", blockedby.ApprovalPending, "escalation", id, "status", status), nil
	}
	body := ""
	if e.Answer != nil {
		body = *e.Answer
	}
	if !a.Answers.Approves(body) {
		return gatepolicy.Confirm{}, a.refuse(fmt.Sprintf("escalation %s was answered without approving the merge", id),
			"blockedBy", blockedby.ApprovalDeclined, "escalation", id, "answer", body), nil
	}
	return gatepolicy.Confirm{Given: true, Note: body, Escalation: id}, nil, nil
}

// approvalError is a refusal that carries no envelope of its own.
func approvalError(exit int, format string, args ...any) *exitcode.Refusal {
	return &exitcode.Refusal{Err: &exitcode.Error{Exit: exit, Message: fmt.Sprintf(format, args...)}}
}

// refuse is the exit 4 refusal of --approval: Extra's fields first (the
// verb's own shape), then fields, changed false.
func (a MergeApproval) refuse(msg string, fields ...any) *exitcode.Refusal {
	d := a.Extra
	if d == nil {
		d = jsonx.NewObject()
	}
	for i := 0; i+1 < len(fields); i += 2 {
		d.Set(fields[i].(string), fields[i+1])
	}
	d.Set("changed", false)
	return &exitcode.Refusal{Err: &exitcode.Error{Exit: exitcode.ExitRefused, Message: msg}, Data: d}
}

// Ref is the thread's number as users read it: `#12`, or the key PROJ-123.
func (t ApprovalThread) Ref() string {
	if t.Key != "" {
		return t.Key.Ref()
	}
	return "#" + strconv.Itoa(t.Number)
}

// TargetApprovalThread is the approval thread of a resolved gate target: its
// recorded PR, else its issue. A queued record's slot has moved on, so its
// thread names none. A target with neither is exit 2.
func TargetApprovalThread(t GateTarget) (ApprovalThread, error) {
	slot := t.Name
	if n, ok := PRRefNumber(t.PR); ok {
		return ApprovalThread{Kind: "pr", Number: n, Slot: slot, Title: fmt.Sprintf("Merge approval: PR #%d", n)}, nil
	}
	if k, ok := tracker.ParseKey(t.Issue); ok {
		th := ApprovalThread{Kind: "issue", Slot: slot, Title: fmt.Sprintf("Merge approval: %s (%s)", slot, t.Branch)}
		if n, isNum := k.Number(); isNum {
			th.Number = n
		} else {
			th.Key = k
		}
		return th, nil
	}
	return ApprovalThread{}, &exitcode.Error{Exit: exitcode.ExitUsage,
		Message: fmt.Sprintf("--approval and --escalate need an approval thread: slot %s has no PR and no issue number or key", slot)}
}

// SlotApprovalThread is the approval thread of a worker gate or train (C5):
// the slot's recorded PR, else the slot's issue. The argument may also be a PR
// ref, which resolves to a queued PR record. Neither is exit 2.
func SlotApprovalThread(root, slot string) (ApprovalThread, error) {
	reg, err := LoadRegistry(root)
	if err != nil {
		return ApprovalThread{}, err
	}
	t, err := reg.GateTargetAny(slot)
	if err != nil {
		return ApprovalThread{}, err
	}
	return TargetApprovalThread(t)
}

// ApprovalEscalator is the forge-facing seam of an approval request: the
// escalation registry and the thread it posts on. The cli wires the real one,
// a test a fake.
type ApprovalEscalator interface {
	// Pending is the entry already waiting on the thread, if any; an error
	// means the registry could not be read.
	Pending(th ApprovalThread) (entry any, ok bool, err error)
	// Send posts body on the thread and returns the new entry and any
	// warnings the send raised.
	Send(th ApprovalThread, body string) (entry any, warnings []string, err error)
	// ReplyWords are the replies that approve the merge, the last one a phrase.
	ReplyWords() []string
}

// ApprovalEscalation asks a merge verb to send the approval request when the
// merge-approval gate refuses (--escalate).
type ApprovalEscalation struct {
	Esc    ApprovalEscalator
	Policy gatepolicy.MergePolicy
	// Warn receives a failure to send: the refusal stands.
	Warn func(format string, a ...any)
}

// ApprovalRequestBody is the escalation body: the policy, then the reply words.
func ApprovalRequestBody(p gatepolicy.MergePolicy, hit, replies []string) string {
	var b strings.Builder
	if p.Mode == gatepolicy.MergePaths {
		b.WriteString("Merge policy `paths`: these changed files need approval:\n")
		for _, f := range hit {
			b.WriteString("- " + f + "\n")
		}
	} else {
		b.WriteString("Merge policy `all`: every merge needs approval.\n")
	}
	words := make([]string, len(replies))
	for i, w := range replies {
		words[i] = "`" + w + "`"
	}
	last := len(words) - 1
	fmt.Fprintf(&b, "\nReply %s or %s to merge. Any other reply holds the merge.", strings.Join(words[:last], ", "), words[last])
	return b.String()
}

// EscalateApproval sends the approval request on the thread, or reuses the
// pending one, and returns the entry for the refusal data. A failure is a
// warning and nil: the refusal stands.
func (a ApprovalEscalation) EscalateApproval(hit []string, th ApprovalThread) any {
	e, ok, err := a.Esc.Pending(th)
	if err != nil {
		a.Warn("approval request not sent: %v", err)
		return nil
	}
	if ok {
		return e
	}
	entry, warns, err := a.Esc.Send(th, ApprovalRequestBody(a.Policy, hit, a.Esc.ReplyWords()))
	for _, w := range warns {
		a.Warn("%s", w)
	}
	if err != nil {
		a.Warn("approval request not sent: %v", err)
		return nil
	}
	return entry
}

// Attach sends the request for a refused merge and sets the entry on the
// refusal's data, where the verb's JSON carries it. It is the one sender for
// the gate, the train and ship pr-merge; hit is the list the refusal was
// judged on, so no caller lists the merge's files again.
func (a ApprovalEscalation) Attach(ref *exitcode.Refusal, hit []string, th ApprovalThread) {
	d, ok := ref.Data.(*jsonx.Object)
	if !ok {
		return
	}
	if e := a.EscalateApproval(hit, th); e != nil {
		d.Set("escalation", e)
	}
}
