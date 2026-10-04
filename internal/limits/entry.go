// Package limits is the logic behind `rota limit watch` (D3, #67): a loop that
// keeps a round from stalling on a 5-hour or weekly usage limit. It notices
// the limit in the orchestrator's or a worker's pane, then either sleeps until
// the reset and types a resume prompt, or moves a worker's issue to an idle
// slot on another account. Every transition is recorded in the top-level
// `limits` list of .rota/workers.json, under the registry lock.
//
// Everything outside its own memory is injected (clock, panes, the account
// meter, transfer, send, escalation, notification), so tests need no real
// herdr, tmux, forge or Claude.
package limits

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/worker"
)

// Entry statuses.
const (
	StatusWaiting  = "waiting"
	StatusResumed  = "resumed"
	StatusSwitched = "switched"
	StatusFailed   = "failed"
)

// Actions.
const (
	ActionSleep  = "sleep"
	ActionSwitch = "switch"
	// ActionRestart is a same-account restart of the orchestrator after a
	// usage handoff that found no account to move to (D4).
	ActionRestart = "restart"
)

// Sources of the reset time.
const (
	SourceData = "data"
	SourceText = "text"
)

// Windows.
const (
	WindowFiveHour = "five_hour"
	WindowSevenDay = "seven_day"
	WindowUnknown  = "unknown"
)

// Orchestrator is the session name of the orchestrator's own pane; a worker's
// session is its slot name.
const Orchestrator = "orchestrator"

// Entry is one record of the `limits` list.
type Entry struct {
	ID         string
	Session    string // Orchestrator or a slot name
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

// Time renders t as RFC 3339 UTC.
func Time(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// Resets is ResetsAt parsed, false when absent.
func (e Entry) Resets() (time.Time, bool) {
	if e.ResetsAt == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, e.ResetsAt)
	return t, err == nil
}

// Object is the stored shape of the entry.
func (e Entry) Object() *jsonx.Object {
	o := jsonx.NewObject()
	o.Set("id", e.ID)
	o.Set("session", e.Session)
	o.Set("window", e.Window)
	o.Set("source", e.Source)
	o.Set("detectedAt", e.DetectedAt)
	if e.ResetsAt != "" {
		o.Set("resetsAt", e.ResetsAt)
	}
	o.Set("action", e.Action)
	if e.Account != "" {
		o.Set("account", e.Account)
	}
	if e.To != "" {
		o.Set("to", e.To)
	}
	o.Set("status", e.Status)
	o.Set("cycles", e.Cycles)
	if e.ResolvedAt != "" {
		o.Set("resolvedAt", e.ResolvedAt)
	}
	if e.Note != "" {
		o.Set("note", e.Note)
	}
	return o
}

func fromObject(o *jsonx.Object) Entry {
	n, _ := o.Get("cycles")
	cycles, _ := strconv.Atoi(fmt.Sprint(n))
	return Entry{
		ID: worker.Str(o, "id"), Session: worker.Str(o, "session"), Window: worker.Str(o, "window"),
		Source: worker.Str(o, "source"), DetectedAt: worker.Str(o, "detectedAt"), ResetsAt: worker.Str(o, "resetsAt"),
		Action: worker.Str(o, "action"), Account: worker.Str(o, "account"), To: worker.Str(o, "to"),
		Status: worker.Str(o, "status"), Cycles: cycles, ResolvedAt: worker.Str(o, "resolvedAt"), Note: worker.Str(o, "note"),
	}
}

// Load reads the limits list; a missing or malformed list reads as empty.
func Load(root string) []Entry {
	raw, _ := worker.LoadRegistry(root).Doc.Get("limits")
	list, _ := raw.([]any)
	var out []Entry
	for _, v := range list {
		if o, ok := v.(*jsonx.Object); ok {
			out = append(out, fromObject(o))
		}
	}
	return out
}

// Waiting are the entries still waiting, in list order.
func Waiting(list []Entry) []Entry {
	var out []Entry
	for _, e := range list {
		if e.Status == StatusWaiting {
			out = append(out, e)
		}
	}
	return out
}

// NextID is l<N>, N one more than the highest in the list.
func NextID(list []Entry) string {
	highest := 0
	for _, e := range list {
		if n, err := strconv.Atoi(strings.TrimPrefix(e.ID, "l")); err == nil && n > highest {
			highest = n
		}
	}
	return "l" + strconv.Itoa(highest+1)
}

func registryDef() *jsonx.Object {
	d := jsonx.NewObject()
	d.Set("slots", []any{})
	return d
}

func listOf(doc *jsonx.Object) []any {
	raw, _ := doc.Get("limits")
	list, _ := raw.([]any)
	return list
}

// Append adds the entry under the registry lock, numbering it from the list
// as it is at that moment, and returns it with its id.
func Append(root string, e Entry) (Entry, error) {
	err := worker.Update(root, registryDef(), func(doc *jsonx.Object) {
		list := listOf(doc)
		var cur []Entry
		for _, v := range list {
			if o, ok := v.(*jsonx.Object); ok {
				cur = append(cur, fromObject(o))
			}
		}
		e.ID = NextID(cur)
		doc.Set("limits", append(list, e.Object()))
	})
	return e, err
}

// Save rewrites the entry with the same id under the registry lock.
func Save(root string, e Entry) error {
	return worker.Update(root, registryDef(), func(doc *jsonx.Object) {
		list := listOf(doc)
		for i, v := range list {
			if o, ok := v.(*jsonx.Object); ok && worker.Str(o, "id") == e.ID {
				list[i] = e.Object()
			}
		}
		doc.Set("limits", list)
	})
}
