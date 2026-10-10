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
	"strconv"
	"strings"
	"time"

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

// Entry is one record of the `limits` list; the worker package owns its
// stored shape.
type Entry = worker.Limit

// Time renders t as RFC 3339 UTC.
func Time(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// Load reads the limits list; a missing or malformed list reads as empty. It
// tolerates a corrupt registry: it feeds display and the tick's passive reads,
// and the verbs that write the list refuse a corrupt registry themselves.
func Load(root string) []Entry { return worker.LoadRegistryTolerant(root).Limits() }

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

// Append adds the entry under the registry lock, numbering it from the list
// as it is at that moment, and returns it with its id.
func Append(root string, e Entry) (Entry, error) {
	err := worker.UpdateLimits(root, func(list []Entry) []Entry {
		e.ID = NextID(list)
		return append(list, e)
	})
	return e, err
}

// Save rewrites the entry with the same id under the registry lock.
func Save(root string, e Entry) error {
	return worker.UpdateLimits(root, func(list []Entry) []Entry {
		for i := range list {
			if list[i].ID == e.ID {
				list[i] = e
			}
		}
		return list
	})
}
