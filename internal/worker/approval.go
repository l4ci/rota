package worker

import (
	"fmt"
	"strconv"
	"strings"

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
