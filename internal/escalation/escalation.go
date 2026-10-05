// Package escalation is the logic behind `rota round escalate send|check` (C4):
// a question the orchestrator puts to the human on an issue or PR thread, a
// herdr notification beside it, and the record in the top-level `escalations`
// list of .rota/workers.json. Forge calls go through internal/tracker only.
package escalation

import (
	"context"
	"errors"
	"fmt"
	"github.com/l4ci/rota/internal/exitcode"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/marker"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// Statuses. StatusTimedOut is derived on read and never stored.
const (
	StatusPending  = worker.EscalationPending
	StatusAnswered = worker.EscalationAnswered
	StatusTimedOut = worker.EscalationTimedOut
)

// Error is a verb failure with its exit code; Data is the failure data for
// exit 1 and 4.
type Error struct {
	Exit    int
	Message string
	Data    any
}

func (e *Error) Error() string { return e.Message }

// Answer is the comment that answered an escalation.
type Answer = worker.EscalationAnswer

// Entry is one record of the `escalations` list; the worker package owns its
// stored shape.
type Entry = worker.Escalation

// Forge is the part of tracker.Adapter the verbs use.
type Forge interface {
	Comments(ctx context.Context, number int) ([]tracker.Comment, error)
	AddComment(ctx context.Context, number int, body string) (string, error)
	MRNotes(ctx context.Context, number int) ([]tracker.Comment, error)
	AddMRNote(ctx context.Context, number int, body string) (string, error)
	CommentURL(ctx context.Context, pr bool, number int, commentID string) (string, error)
}

// Env is what the verbs touch outside their own memory; tests replace it.
type Env struct {
	Now    func() time.Time
	Getenv func(string) string
	// LookPath reports whether a binary is installed; nil means exec.LookPath.
	LookPath func(string) (string, error)
	// Forge returns the forge for the project, an error when none resolves.
	Forge func(ctx context.Context, root string) (Forge, error)
	// Host returns the herdr host (never tmux, which has no notifications).
	Host func() host.Host
}

func (e Env) withDefaults() Env {
	if e.Now == nil {
		e.Now = time.Now
	}
	if e.Getenv == nil {
		e.Getenv = os.Getenv
	}
	return e
}

// Time renders t as RFC 3339 UTC.
func Time(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// Load reads the escalations list; a missing or malformed list reads as empty.
func Load(root string) []Entry { return worker.LoadRegistry(root).Escalations() }

// NextID is e<N>, N one more than the highest in the list.
func NextID(list []Entry) string {
	highest := 0
	for _, e := range list {
		if n, err := strconv.Atoi(strings.TrimPrefix(e.ID, "e")); err == nil && n > highest {
			highest = n
		}
	}
	return "e" + strconv.Itoa(highest+1)
}

// PendingOn finds the stored-pending entry on a thread (one per thread).
func PendingOn(list []Entry, kind string, number int) (Entry, bool) {
	for _, e := range list {
		if e.Kind == kind && e.Number == number && e.Status == StatusPending {
			return e, true
		}
	}
	return Entry{}, false
}

// Marker is the line every escalation comment ends with.
func Marker(id string) string { return marker.Line("escalation", id) }

// Compose is the comment rota posts: heading, the body, the ask, the marker.
func Compose(id, title, body string) string {
	return fmt.Sprintf("**rota escalation %s**: %s\n\n%s\n\nAnswer in a new comment on this thread.\n\n%s\n",
		id, title, strings.Trim(body, "\n"), Marker(id))
}

func kindOf(pr bool) string {
	if pr {
		return "pr"
	}
	return "issue"
}

func comments(ctx context.Context, f Forge, kind string, number int) ([]tracker.Comment, error) {
	if kind == "pr" {
		return f.MRNotes(ctx, number)
	}
	return f.Comments(ctx, number)
}

func fromTracker(err error) *Error {
	var te *tracker.Error
	if errors.As(err, &te) {
		return &Error{Exit: te.Kind.Exit(), Message: te.Message}
	}
	return &Error{Exit: 70, Message: err.Error()}
}

// SendOpts are the flags of `rota round escalate send`.
type SendOpts struct {
	Number  int
	PR      bool
	Slot    string
	Title   string
	Body    string
	Timeout time.Duration
}

// SendResult is what a send did.
type SendResult struct {
	Entry    Entry
	URL      string
	Notified bool
	Warnings []string
}

// Send posts the escalation comment, raises the notification and records the
// entry. The comment goes first, so a failed write leaves a comment and no
// record: that is exit 1 with the comment's url in the failure data.
func Send(ctx context.Context, env Env, root string, o SendOpts) (SendResult, error) {
	env = env.withDefaults()
	var res SendResult
	if o.Slot != "" && worker.LoadRegistry(root).Slot(o.Slot) == nil {
		return res, &Error{Exit: exitcode.ExitResolution, Message: fmt.Sprintf("--slot %s is not a registered slot", o.Slot)}
	}
	kind := kindOf(o.PR)
	// The pending check and NextID below read the registry outside its lock.
	// That is safe only while one orchestrator sends escalations at a time
	// (ratified by the orchestrator, round 4).
	list := Load(root)
	if p, ok := PendingOn(list, kind, o.Number); ok {
		return res, &Error{Exit: exitcode.ExitRefused, Message: fmt.Sprintf("escalation %s is still pending on %s #%d", p.ID, kind, o.Number),
			Data: pendingData(p.ID)}
	}
	if env.Forge == nil {
		return res, &Error{Exit: exitcode.ExitUnavailable, Message: "no forge configured"}
	}
	f, err := env.Forge(ctx, root)
	if err != nil {
		return res, fromTracker(err)
	}
	id := NextID(list)
	text := Compose(id, o.Title, o.Body)
	var cid string
	if o.PR {
		cid, err = f.AddMRNote(ctx, o.Number, text)
	} else {
		cid, err = f.AddComment(ctx, o.Number, text)
	}
	if err != nil {
		te := fromTracker(err)
		if te.Exit == exitcode.ExitResolution {
			te.Message = fmt.Sprintf("no such %s #%d: %s", kind, o.Number, te.Message)
		}
		return res, te
	}
	if u, err := f.CommentURL(ctx, o.PR, o.Number, cid); err != nil {
		res.Warnings = append(res.Warnings, "comment posted but its url could not be read: "+err.Error())
	} else {
		res.URL = u
	}

	res.Notified, res.Warnings = notify(ctx, env, root, id, o, res.Warnings)

	now := env.Now()
	e := Entry{ID: id, Kind: kind, Number: o.Number, Slot: o.Slot, Title: o.Title, CommentID: cid,
		SentAt: Time(now), Notified: res.Notified, Status: StatusPending}
	if o.Timeout > 0 {
		e.Deadline = Time(now.Add(o.Timeout))
	}
	res.Entry = e
	err = worker.UpdateEscalations(root, func(list []Entry) []Entry {
		return append(list, e)
	})
	if err != nil {
		d := jsonx.NewObject()
		d.Set("id", id)
		d.Set("commentId", cid)
		d.Set("url", res.URL)
		d.Set("changed", false)
		return res, &Error{Exit: exitcode.ExitFailed, Message: fmt.Sprintf("comment %s posted (%s) but the record was not written: %v", cid, res.URL, err), Data: d}
	}
	return res, nil
}

func pendingData(id string) *jsonx.Object {
	d := jsonx.NewObject()
	d.Set("pending", id)
	d.Set("changed", false)
	return d
}

// notify raises the herdr notification when the host is herdr: work.dispatch
// is herdr or the process runs inside herdr, and the binary is installed.
func notify(ctx context.Context, env Env, root, id string, o SendOpts, warns []string) (bool, []string) {
	switch worker.ResolveHost(root, env.Getenv, env.LookPath) {
	case host.Solo: // the PR comment is the only channel
		return false, append(warns, "no notification: the round is solo, there is no herdr")
	case "herdr":
	default:
		return false, append(warns, "no notification: the host is not herdr (the round, work.dispatch and the environment do not name herdr)")
	}
	h := env.Host()
	if err := h.Require(); err != nil {
		return false, append(warns, "no notification: "+err.Error())
	}
	h.Notify(ctx, "rota escalation "+id, fmt.Sprintf("%s (#%d)", o.Title, o.Number))
	return true, warns
}

// Report is one row of `check`: the stored entry plus the status as reported.
type Report struct {
	Entry  Entry
	Status string
}

// CheckResult is what a check found.
type CheckResult struct {
	Reports  []Report
	Changed  bool
	Warnings []string
}

// Check classifies escalations. With no ids it covers every stored-pending
// entry; named ids are covered whatever their status. A found answer is stored
// under the registry lock. A forge failure on one thread is a warning and the
// entry keeps its stored status.
func Check(ctx context.Context, env Env, root string, ids []string) (CheckResult, error) {
	env = env.withDefaults()
	var res CheckResult
	list := Load(root)
	var targets []Entry
	if len(ids) == 0 {
		for _, e := range list {
			if e.Status == StatusPending {
				targets = append(targets, e)
			}
		}
	} else {
		for _, id := range ids {
			found := false
			for _, e := range list {
				if e.ID == id {
					targets, found = append(targets, e), true
					break
				}
			}
			if !found {
				return res, &Error{Exit: exitcode.ExitResolution, Message: fmt.Sprintf("no escalation %s", id)}
			}
		}
	}

	var forge Forge
	found := map[string]Answer{}
	for i, e := range targets {
		if e.Status != StatusPending {
			continue
		}
		if forge == nil {
			if env.Forge == nil {
				return res, &Error{Exit: exitcode.ExitUnavailable, Message: "no forge configured"}
			}
			f, err := env.Forge(ctx, root)
			if err != nil {
				return res, fromTracker(err)
			}
			forge = f
		}
		cs, err := comments(ctx, forge, e.Kind, e.Number)
		if err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s: cannot read %s #%d: %v", e.ID, e.Kind, e.Number, err))
			continue
		}
		a, ok, escFound := FindAnswer(cs, e.CommentID)
		if !escFound {
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s: escalation comment %s is gone from %s #%d, cannot tell what answers it", e.ID, e.CommentID, e.Kind, e.Number))
			continue
		}
		if ok {
			ans := Answer{CommentID: a.ID, Author: a.Author, Body: a.Body, SeenAt: Time(env.Now())}
			found[e.ID] = ans
			targets[i].Status, targets[i].Answer = StatusAnswered, &ans
		}
	}

	if len(found) > 0 {
		err := worker.UpdateEscalations(root, func(stored []Entry) []Entry {
			for j := range stored {
				a, hit := found[stored[j].ID]
				if !hit || stored[j].Status != StatusPending {
					continue
				}
				stored[j].Status, stored[j].Answer = StatusAnswered, &a
			}
			return stored
		})
		if err != nil {
			return res, err
		}
		res.Changed = true
	}
	now := env.Now()
	for _, e := range targets {
		res.Reports = append(res.Reports, Report{Entry: e, Status: e.Derived(now)})
	}
	return res, nil
}
