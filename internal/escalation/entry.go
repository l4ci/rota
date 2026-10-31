package escalation

import (
	"strconv"
	"time"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/stamp"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// Entry statuses. StatusTimedOut is derived on read and never stored.
const (
	StatusPending  = "pending"
	StatusAnswered = "answered"
	StatusTimedOut = "timed-out"
)

// Answer is the comment that answered an escalation.
type Answer struct {
	CommentID, Author, Body, SeenAt string
	Option                          int // 1-based option the reply resolved to; 0 when it named none
}

// Entry is one record of the `escalations` list; this package owns its stored shape.
type Entry struct {
	ID        string
	Kind      string // issue | pr
	Number    int    // the PR, or a numbered issue; 0 for a key issue
	Key       string // a key-based tracker's issue (PROJ-123); empty otherwise
	Slot      string
	Title     string
	CommentID string
	SentAt    string
	Deadline  string
	Notified  bool
	Status    string // stored: pending | answered
	Options   []string
	Recommend int // 1-based number of the recommended option
	Cost      string
	Answer    *Answer
}

func entryFrom(o *jsonx.Object) Entry {
	nv, _ := o.Get("number")
	n, _ := jsonx.Int(nv)
	e := Entry{
		ID: jsonx.Str(o, "id"), Kind: jsonx.Str(o, "kind"), Number: n, Key: jsonx.Str(o, "key"),
		Slot: jsonx.Str(o, "slot"), Title: jsonx.Str(o, "title"), CommentID: jsonx.Str(o, "commentId"),
		SentAt: jsonx.Str(o, "sentAt"), Deadline: jsonx.Str(o, "deadline"), Status: jsonx.Str(o, "status"),
	}
	if v, _ := o.Get("notified"); v == true {
		e.Notified = true
	}
	if v, ok := o.Get("options"); ok {
		if l, ok := v.([]any); ok {
			for _, x := range l {
				if str, ok := x.(string); ok {
					e.Options = append(e.Options, str)
				}
			}
		}
	}
	if v, ok := o.Get("recommend"); ok {
		e.Recommend, _ = jsonx.Int(v)
	}
	e.Cost = jsonx.Str(o, "cost")
	if a, ok := o.Get("answer"); ok {
		if ao, ok := a.(*jsonx.Object); ok {
			e.Answer = &Answer{CommentID: jsonx.Str(ao, "commentId"), Author: jsonx.Str(ao, "author"),
				Body: jsonx.Str(ao, "body"), SeenAt: jsonx.Str(ao, "seenAt")}
			if v, ok := ao.Get("option"); ok {
				e.Answer.Option, _ = jsonx.Int(v)
			}
		}
	}
	return e
}

// Object is the stored shape of the answer.
func (a Answer) Object() *jsonx.Object {
	o := jsonx.NewObject()
	o.Set("commentId", a.CommentID)
	o.Set("author", a.Author)
	o.Set("body", a.Body)
	o.Set("seenAt", a.SeenAt)
	if a.Option > 0 {
		o.Set("option", a.Option)
	}
	return o
}

// Object is the stored shape of the escalation (the contract's `escalation`).
func (e Entry) Object() *jsonx.Object {
	o := jsonx.NewObject()
	o.Set("id", e.ID)
	o.Set("kind", e.Kind)
	if e.Key != "" {
		o.Set("key", e.Key)
	} else {
		o.Set("number", e.Number)
	}
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
	if len(e.Options) > 0 {
		opts := make([]any, len(e.Options))
		for i, x := range e.Options {
			opts[i] = x
		}
		o.Set("options", opts)
		o.Set("recommend", e.Recommend)
		if e.Cost != "" {
			o.Set("cost", e.Cost)
		}
	}
	o.Set("status", e.Status)
	if e.Answer != nil {
		o.Set("answer", e.Answer.Object())
	}
	return o
}

// Ref is the thread's number as users read it: `#12` for a PR or a numbered
// issue, the key itself (PROJ-123) for a key issue.
func (e Entry) Ref() string {
	if e.Key != "" {
		return e.Key
	}
	return "#" + strconv.Itoa(e.Number)
}

// IssueKey is the key of an issue thread's issue.
func (e Entry) IssueKey() tracker.IssueKey {
	if e.Key != "" {
		return tracker.IssueKey(e.Key)
	}
	return tracker.NumKey(e.Number)
}

// Derived is the status as reported: a stored pending escalation past its
// deadline reads as timed-out. Nothing stores it, so a late answer still lands.
func (e Entry) Derived(now time.Time) string {
	if e.Status != StatusPending || e.Deadline == "" {
		return e.Status
	}
	if d, err := stamp.Parse(e.Deadline); err == nil && now.After(d) {
		return StatusTimedOut
	}
	return e.Status
}

// FromRegistry is the records of the escalation list, in list order; a missing
// or malformed list reads as empty.
func FromRegistry(r worker.Registry) []Entry {
	var out []Entry
	for _, o := range r.Objects("escalations") {
		out = append(out, entryFrom(o))
	}
	return out
}

// Update edits the escalation list under the registry lock: mutate gets the
// list as it is and returns the new one.
func Update(root string, mutate func(list []Entry) []Entry) error {
	return worker.Update(root, func(d *worker.Doc) {
		next := mutate(FromRegistry(d.Registry))
		out := make([]any, 0, len(next))
		for _, e := range next {
			out = append(out, e.Object())
		}
		d.SetSection("escalations", out)
	})
}
