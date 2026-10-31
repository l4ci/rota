package limits

import (
	"time"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/stamp"
	"github.com/l4ci/rota/internal/worker"
)

// Entry is one record of the `limits` list; this package owns its stored shape.
type Entry struct {
	ID         string
	Session    string // the orchestrator's session name or a slot name
	Window     string
	Source     string
	DetectedAt string
	ResetsAt   string // empty when unknown
	Action     string
	Account    string // the limited slot's account
	Kind       string // the limited slot's harness kind, "" for Claude
	To         string // the slot the issue moved to
	Status     string
	Cycles     int
	ResolvedAt string
	Note       string
}

// Resets is ResetsAt parsed, false when absent.
func (l Entry) Resets() (time.Time, bool) {
	if l.ResetsAt == "" {
		return time.Time{}, false
	}
	t, err := stamp.Parse(l.ResetsAt)
	return t, err == nil
}

func entryFrom(o *jsonx.Object) Entry {
	n, _ := o.Get("cycles")
	cycles, _ := jsonx.Int(n)
	return Entry{
		ID: jsonx.Str(o, "id"), Session: jsonx.Str(o, "session"), Window: jsonx.Str(o, "window"),
		Source: jsonx.Str(o, "source"), DetectedAt: jsonx.Str(o, "detectedAt"), ResetsAt: jsonx.Str(o, "resetsAt"),
		Action: jsonx.Str(o, "action"), Account: jsonx.Str(o, "account"), Kind: jsonx.Str(o, "kind"), To: jsonx.Str(o, "to"),
		Status: jsonx.Str(o, "status"), Cycles: cycles, ResolvedAt: jsonx.Str(o, "resolvedAt"), Note: jsonx.Str(o, "note"),
	}
}

// Object is the stored shape of the limit.
func (l Entry) Object() *jsonx.Object {
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
	if l.Kind != "" {
		o.Set("kind", l.Kind)
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

// FromRegistry is the records of the limit list, in list order.
func FromRegistry(r worker.Registry) []Entry {
	var out []Entry
	for _, o := range r.Objects("limits") {
		out = append(out, entryFrom(o))
	}
	return out
}

// Update is escalation.Update for the limit list.
func Update(root string, mutate func(list []Entry) []Entry) error {
	return worker.Update(root, func(d *worker.Doc) {
		next := mutate(FromRegistry(d.Registry))
		out := make([]any, 0, len(next))
		for _, l := range next {
			out = append(out, l.Object())
		}
		d.SetSection("limits", out)
	})
}
