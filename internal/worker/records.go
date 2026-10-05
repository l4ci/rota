package worker

import (
	"time"

	"github.com/l4ci/rota/internal/jsonx"
)

// Escalation statuses. StatusTimedOut is derived on read and never stored.
const (
	EscalationPending  = "pending"
	EscalationAnswered = "answered"
	EscalationTimedOut = "timed-out"
)

// EscalationAnswer is the comment that answered an escalation.
type EscalationAnswer struct {
	CommentID, Author, Body, SeenAt string
}

// Escalation is one record of the `escalations` list.
type Escalation struct {
	ID        string
	Kind      string // issue | pr
	Number    int
	Slot      string
	Title     string
	CommentID string
	SentAt    string
	Deadline  string
	Notified  bool
	Status    string // stored: pending | answered
	Answer    *EscalationAnswer
}

func escalationFrom(o *jsonx.Object) Escalation {
	nv, _ := o.Get("number")
	n, _ := intOf(nv)
	e := Escalation{
		ID: jsonx.Str(o, "id"), Kind: jsonx.Str(o, "kind"), Number: n,
		Slot: jsonx.Str(o, "slot"), Title: jsonx.Str(o, "title"), CommentID: jsonx.Str(o, "commentId"),
		SentAt: jsonx.Str(o, "sentAt"), Deadline: jsonx.Str(o, "deadline"), Status: jsonx.Str(o, "status"),
	}
	if v, _ := o.Get("notified"); v == true {
		e.Notified = true
	}
	if a, ok := o.Get("answer"); ok {
		if ao, ok := a.(*jsonx.Object); ok {
			e.Answer = &EscalationAnswer{CommentID: jsonx.Str(ao, "commentId"), Author: jsonx.Str(ao, "author"),
				Body: jsonx.Str(ao, "body"), SeenAt: jsonx.Str(ao, "seenAt")}
		}
	}
	return e
}

// Object is the stored shape of the answer.
func (a EscalationAnswer) Object() *jsonx.Object {
	o := jsonx.NewObject()
	o.Set("commentId", a.CommentID)
	o.Set("author", a.Author)
	o.Set("body", a.Body)
	o.Set("seenAt", a.SeenAt)
	return o
}

// Object is the stored shape of the escalation (the contract's `escalation`).
func (e Escalation) Object() *jsonx.Object {
	o := jsonx.NewObject()
	o.Set("id", e.ID)
	o.Set("kind", e.Kind)
	o.Set("number", e.Number)
	if e.Slot != "" {
		o.Set("slot", e.Slot)
	}
	o.Set("title", e.Title)
	o.Set("commentId", e.CommentID)
	o.Set("sentAt", e.SentAt)
	if e.Deadline != "" {
		o.Set("deadline", e.Deadline)
	}
	o.Set("notified", e.Notified)
	o.Set("status", e.Status)
	if e.Answer != nil {
		o.Set("answer", e.Answer.Object())
	}
	return o
}

// Derived is the status as reported: a stored pending escalation past its
// deadline reads as timed-out. Nothing stores it, so a late answer still lands.
func (e Escalation) Derived(now time.Time) string {
	if e.Status != EscalationPending || e.Deadline == "" {
		return e.Status
	}
	if d, err := time.Parse(time.RFC3339, e.Deadline); err == nil && now.After(d) {
		return EscalationTimedOut
	}
	return e.Status
}

// Limit is one record of the `limits` list.
type Limit struct {
	ID         string
	Session    string // the orchestrator's session name or a slot name
	Window     string
	Source     string
	DetectedAt string
	ResetsAt   string // empty when unknown
	Action     string
	Account    string // the limited slot's account
	To         string // the slot the issue moved to
	Status     string
	Cycles     int
	ResolvedAt string
	Note       string
}

// Resets is ResetsAt parsed, false when absent.
func (l Limit) Resets() (time.Time, bool) {
	if l.ResetsAt == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, l.ResetsAt)
	return t, err == nil
}

func limitFrom(o *jsonx.Object) Limit {
	n, _ := o.Get("cycles")
	cycles, _ := intOf(n)
	return Limit{
		ID: jsonx.Str(o, "id"), Session: jsonx.Str(o, "session"), Window: jsonx.Str(o, "window"),
		Source: jsonx.Str(o, "source"), DetectedAt: jsonx.Str(o, "detectedAt"), ResetsAt: jsonx.Str(o, "resetsAt"),
		Action: jsonx.Str(o, "action"), Account: jsonx.Str(o, "account"), To: jsonx.Str(o, "to"),
		Status: jsonx.Str(o, "status"), Cycles: cycles, ResolvedAt: jsonx.Str(o, "resolvedAt"), Note: jsonx.Str(o, "note"),
	}
}

// Object is the stored shape of the limit.
func (l Limit) Object() *jsonx.Object {
	o := jsonx.NewObject()
	o.Set("id", l.ID)
	o.Set("session", l.Session)
	o.Set("window", l.Window)
	o.Set("source", l.Source)
	o.Set("detectedAt", l.DetectedAt)
	if l.ResetsAt != "" {
		o.Set("resetsAt", l.ResetsAt)
	}
	o.Set("action", l.Action)
	if l.Account != "" {
		o.Set("account", l.Account)
	}
	if l.To != "" {
		o.Set("to", l.To)
	}
	o.Set("status", l.Status)
	o.Set("cycles", l.Cycles)
	if l.ResolvedAt != "" {
		o.Set("resolvedAt", l.ResolvedAt)
	}
	if l.Note != "" {
		o.Set("note", l.Note)
	}
	return o
}

// Escalations are the records of the escalation list, in list order; a missing
// or malformed list reads as empty.
func (r Registry) Escalations() []Escalation {
	var out []Escalation
	for _, o := range r.objects("escalations") {
		out = append(out, escalationFrom(o))
	}
	return out
}

// Limits are the records of the limit list, in list order.
func (r Registry) Limits() []Limit {
	var out []Limit
	for _, o := range r.objects("limits") {
		out = append(out, limitFrom(o))
	}
	return out
}

// UpdateEscalations edits the escalation list under the registry lock: mutate
// gets the list as it is and returns the new one.
func UpdateEscalations(root string, mutate func(list []Escalation) []Escalation) error {
	return Update(root, func(d *Doc) {
		var cur []Escalation
		for _, o := range d.objects("escalations") {
			cur = append(cur, escalationFrom(o))
		}
		next := mutate(cur)
		out := make([]any, 0, len(next))
		for _, e := range next {
			out = append(out, e.Object())
		}
		d.doc.Set("escalations", out)
	})
}

// UpdateLimits is UpdateEscalations for the limit list.
func UpdateLimits(root string, mutate func(list []Limit) []Limit) error {
	return Update(root, func(d *Doc) {
		var cur []Limit
		for _, o := range d.objects("limits") {
			cur = append(cur, limitFrom(o))
		}
		next := mutate(cur)
		out := make([]any, 0, len(next))
		for _, l := range next {
			out = append(out, l.Object())
		}
		d.doc.Set("limits", out)
	})
}
