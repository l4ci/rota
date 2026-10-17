package worker

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/jsonx"
)

// Slot is one entry of the registry's `slots` list. It owns the slot's field
// names and its state machine: callers say what happened (Bind, Unbind, Park,
// MarkState) and the slot decides which fields change, so the record reads the
// same whichever verb wrote it. The JSON stays what the old helpers wrote.
type Slot struct{ o *jsonx.Object }

// AsSlot wraps a slot object read from the registry document.
func AsSlot(o *jsonx.Object) *Slot { return &Slot{o: o} }

// Raw is the underlying object, for output that shows the record as it is.
func (s *Slot) Raw() *jsonx.Object { return s.o }

// States a slot can record: the lowercased poll states.
var slotStates = map[string]bool{
	"busy": true, "idle": true, "blocked": true, "done": true, "dead": true,
	"limited": true, "needs-permission": true, "unknown": true, "unverifiable": true,
}

// ValidState reports whether MarkState accepts next.
func ValidState(next string) bool { return slotStates[next] }

func (s *Slot) Name() string     { return jsonx.Str(s.o, "name") }
func (s *Slot) Branch() string   { return jsonx.Str(s.o, "branch") }
func (s *Slot) Worktree() string { return jsonx.Str(s.o, "worktree") }
func (s *Slot) Base() string     { return jsonx.Str(s.o, "base") }
func (s *Slot) Handle() string   { return jsonx.Str(s.o, "handle") }

// WorkspaceID is the linked herdr workspace the slot's session was opened in
// (work.herdrGroupSlots), "" when it runs in the orchestrator's own workspace.
func (s *Slot) WorkspaceID() string { return jsonx.Str(s.o, "workspaceId") }

// SetWorkspaceID records the slot's linked workspace; "" clears it.
func (s *Slot) SetWorkspaceID(id string) {
	if id == "" {
		s.o.Delete("workspaceId")
		return
	}
	s.o.Set("workspaceId", id)
}
func (s *Slot) State() string { return jsonx.Str(s.o, "state") }

// Issue is the issue number recorded on the slot ("" when none). The prompt
// digest shows it before falling back to HeldID.
func (s *Slot) Issue() string { return jsonx.Str(s.o, "issue") }

func (s *Slot) Task() string       { return jsonx.Str(s.o, "task") }
func (s *Slot) ClaimID() string    { return jsonx.Str(s.o, "claimId") }
func (s *Slot) Kind() string       { return jsonx.Str(s.o, "kind") }
func (s *Slot) KindSource() string { return jsonx.Str(s.o, "kindSource") }
func (s *Slot) Tier() string       { return jsonx.Str(s.o, "tier") }
func (s *Slot) Model() string      { return jsonx.Str(s.o, "model") }
func (s *Slot) TierReason() string { return jsonx.Str(s.o, "tierReason") }
func (s *Slot) PR() string         { return jsonx.Str(s.o, "pr") }
func (s *Slot) Account() string    { return jsonx.Str(s.o, "account") }

// SmokeSection is the smoke section number reserved for the slot's issue, 0
// for none.
func (s *Slot) SmokeSection() int {
	n, _ := strconv.Atoi(jsonx.Str(s.o, "smokeSection"))
	return n
}
func (s *Slot) ConfigDir() string { return jsonx.Str(s.o, "configDir") }

// CodexAccount is the work.codexAccounts entry the slot last ran a codex
// worker under ("" on the default Codex home).
func (s *Slot) CodexAccount() string { return jsonx.Str(s.o, "codexAccount") }

// SetCodexAccount records the Codex account; "" removes it.
func (s *Slot) SetCodexAccount(name string) {
	if name == "" {
		s.o.Delete("codexAccount")
		return
	}
	s.o.Set("codexAccount", name)
}

// Reason is why the slot is unverifiable (host.Reason*), "" in any other state.
func (s *Slot) Reason() string { return jsonx.Str(s.o, "reason") }

// SetReason records why the slot is unverifiable; it is a no-op for the
// reason already recorded, so a poll loop does not rewrite the registry.
func (s *Slot) SetReason(r string) {
	if s.Reason() != r {
		s.o.Set("reason", r)
	}
}

func (s *Slot) ActiveAt() string { return jsonx.Str(s.o, "activeAt") }
func (s *Slot) Seen() string     { return jsonx.Str(s.o, "seen") }
func (s *Slot) Unsent() bool     { return jsonx.Bool(s.o, "unsent") }

// Relays is the relay log the gate reads for approval provenance.
func (s *Slot) Relays() []any {
	v, _ := s.o.Get("relays")
	l, _ := v.([]any)
	return l
}

// AppendRelays adds entries to the relay log (a transferred PR's history).
func (s *Slot) AppendRelays(entries []any) { s.o.Set("relays", append(s.Relays(), entries...)) }

// ResetRelays starts a fresh relay log.
func (s *Slot) ResetRelays() { s.o.Set("relays", []any{}) }

// SetClaimID records the claim on the slot's issue; "" removes it.
func (s *Slot) SetClaimID(id string) {
	if id == "" {
		s.o.Delete("claimId")
		return
	}
	s.o.Set("claimId", id)
}

// SetTask renames the issue the slot works on (a tracker migration).
func (s *Slot) SetTask(t string) { s.o.Set("task", t) }

// Window is the pre-handle field name for the pane handle.
func (s *Slot) Window() string { return jsonx.Str(s.o, "window") }

// PaneHandle is the handle of the slot's session, falling back to the
// pre-handle `window` field so an unmigrated registry still dispatches.
func (s *Slot) PaneHandle() string {
	if h := s.Handle(); h != "" {
		return h
	}
	return s.Window()
}

// HarnessKind is the harness the slot last ran ("" for none, and for an
// adopted slot, whose kind field names no harness).
func (s *Slot) HarnessKind() string {
	if s.IsExternal() {
		return ""
	}
	return s.Kind()
}

// KindExternal is the slot kind of work another tool started and rota adopted
// (`rota worker adopt`): the slot has no host session, so nothing dispatches
// into it or reads its pane.
const KindExternal = "external"

// IsExternal reports whether the slot was adopted rather than driven by rota.
// It is the one predicate every fence reads; a nil slot is not external.
func (s *Slot) IsExternal() bool { return s != nil && s.Kind() == KindExternal }

// MarkExternal makes the slot an adopted one holding task, with pr when known.
func (s *Slot) MarkExternal(task, pr string) {
	s.o.Set("kind", KindExternal)
	s.o.Set("task", task)
	s.SetPR(pr)
}

// NewSlot is a fresh registry entry. Slots are seeded as already-reported idle
// so a parked slot never fires a spurious "it finished" on the first poll.
// Only a slot that has gone BUSY re-arms that report. handle "" records none.
func NewSlot(name, branch, worktree, base, handle string) *Slot {
	o := jsonx.NewObject()
	s := AsSlot(o)
	o.Set("name", name)
	o.Set("branch", branch)
	o.Set("worktree", worktree)
	o.Set("base", base)
	o.Set("handle", nil)
	s.SetHandle(handle)
	o.Set("state", "idle")
	o.Set("task", nil)
	o.Set("pr", nil)
	// Orchestrator relays sent to this slot, for the gate's
	// approval-provenance check; dispatch --relay appends.
	o.Set("relays", []any{})
	o.Set("configDir", nil)
	return s
}

// Reregister refreshes an existing slot from `pool init`: it migrates the
// pre-herdr `window` field and never lets an empty handle (herdr) clobber a
// live handle that dispatch recorded.
func (s *Slot) Reregister(branch, worktree, base, handle string) {
	legacy := s.Window()
	s.o.Delete("window")
	s.o.Set("branch", branch)
	s.o.Set("worktree", worktree)
	s.o.Set("base", base)
	switch {
	case handle != "":
	case s.Handle() != "":
		handle = s.Handle()
	default:
		handle = legacy
	}
	s.SetHandle(handle)
}

// Dispatch records a brief sent to the slot: the handle, busy, activeAt (the
// stall signal of `round reconcile`) and, for a task, the task id, clearing
// the previous task's PR, relay log and unsent mark. A dispatch re-arms
// `round wait`. The kind is recorded only when it is not the default, or the
// slot already carries one (a relay later reads it to know whether to sign).
func (s *Slot) Dispatch(handle, task, kind, now string) {
	s.o.Delete("window")
	s.SetHandle(handle)
	s.setState("busy", now)
	s.o.Set("activeAt", now)
	s.o.Delete("seen")
	s.ClearEvent() // a new brief answers whatever the worker last sent
	if task == "" {
		return
	}
	s.o.Set("task", task)
	s.o.Set("pr", nil)
	s.o.Delete("reviewSeen")
	s.o.Set("relays", []any{})
	s.o.Delete("unsent")
	if kind != "" && (!harness.IsDefault(kind) || s.Kind() != "") {
		s.o.Set("kind", kind)
	}
}

// TurnSeq is the host's state-change number read just before the last brief
// went out (herdr's `state_change_seq`), 0 when none was recorded. A turn the
// agent finished after it carries a larger `completion_seq`.
func (s *Slot) TurnSeq() int {
	v, _ := s.o.Get("turnSeq")
	n, _ := jsonx.Int(v)
	return n
}

// SetTurnSeq records the dispatch baseline; 0 clears it.
func (s *Slot) SetTurnSeq(n int) {
	if n <= 0 {
		s.o.Delete("turnSeq")
		return
	}
	s.o.Set("turnSeq", n)
}

// SetUnsent marks (or clears) a brief that stalled at the prompt line.
func (s *Slot) SetUnsent(v bool) {
	if v {
		s.o.Set("unsent", true)
		return
	}
	s.o.Delete("unsent")
}

// AppendRelay logs a relay sent to the slot.
func (s *Slot) AppendRelay(entry *jsonx.Object) {
	v, _ := s.o.Get("relays")
	l, _ := v.([]any)
	s.o.Set("relays", append(l, entry))
}

// SetAccount records the account the slot runs under and its config dir.
func (s *Slot) SetAccount(account, configDir string) {
	if configDir == "" {
		s.o.Set("configDir", nil)
	} else {
		s.o.Set("configDir", configDir)
	}
	s.o.Set("account", account)
}

// Binding is what ties a slot to an issue.
type Binding struct {
	Task, ClaimID, Kind, KindSource, Tier, Model, TierReason string
	// SmokeSection is the reserved test/sections number, 0 for none.
	SmokeSection int
}

// Bind records the issue the slot works on. Empty optional fields stay unset.
func (s *Slot) Bind(b Binding) {
	s.o.Set("task", b.Task)
	for _, f := range []struct{ k, v string }{
		{"claimId", b.ClaimID}, {"kind", b.Kind}, {"kindSource", b.KindSource}, {"tier", b.Tier},
		{"model", b.Model}, {"tierReason", b.TierReason}, {"smokeSection", smokeStr(b.SmokeSection)},
	} {
		if f.v == "" {
			s.o.Delete(f.k)
			continue
		}
		s.o.Set(f.k, f.v)
	}
}

func smokeStr(n int) string {
	if n == 0 {
		return ""
	}
	return strconv.Itoa(n)
}

// Unbind drops the issue binding: one field set for every verb. The task and
// PR read null, the optional binding fields (claim, kind, tier, model, tier
// reason) are removed so a freed slot reports nothing of the last issue.
func (s *Slot) Unbind() {
	s.o.Set("task", nil)
	s.o.Set("pr", nil)
	for _, k := range []string{"claimId", "kind", "tier", "model", "tierReason", "smokeSection", "issues", "reviewSeen", "event", "heartbeatAt"} {
		s.o.Delete(k)
	}
}

// Park unbinds the slot, marks it idle and puts it back on its park branch.
// dropHandle also forgets the pane handle (the session is gone).
func (s *Slot) Park(dropHandle bool) {
	s.Unbind()
	s.o.Set("state", "idle")
	s.o.Set("branch", ParkBranch(s.Name()))
	if dropHandle {
		s.o.Set("handle", nil)
	}
}

// MarkState records a state, rejecting one the registry does not know. A
// change stamps activeAt (the stall clock of `round reconcile`) with now.
func (s *Slot) MarkState(next, now string) error {
	next = strings.ToLower(next)
	if !ValidState(next) {
		return fmt.Errorf("unknown slot state %q", next)
	}
	s.setState(next, now)
	return nil
}

func (s *Slot) setState(next, now string) {
	if s.State() != next && now != "" {
		s.o.Set("activeAt", now)
	}
	if next != "unverifiable" {
		s.o.Delete("reason")
	}
	s.o.Set("state", next)
}

// Release is the pane going away: no handle, idle.
func (s *Slot) Release() {
	s.o.Set("handle", nil)
	s.o.Set("state", "idle")
}

// SetPR records the slot's open PR; "" clears it.
func (s *Slot) SetPR(url string) {
	if url == "" {
		s.o.Set("pr", nil)
		return
	}
	s.o.Set("pr", url)
}

// Issues are the issues a review item's worker reported filing
// (`ROTA-DONE <slot> issues:#a,#b`), the slot's done evidence in place of a PR.
func (s *Slot) Issues() []string {
	l, _ := s.o.Get("issues")
	list, _ := l.([]any)
	var out []string
	for _, e := range list {
		if v, ok := e.(string); ok {
			out = append(out, v)
		}
	}
	return out
}

// SetIssues records the reported issues; none clears them.
func (s *Slot) SetIssues(refs []string) {
	if len(refs) == 0 {
		s.o.Delete("issues")
		return
	}
	l := make([]any, len(refs))
	for i, r := range refs {
		l[i] = r
	}
	s.o.Set("issues", l)
}

// SetHandle records the pane handle; "" clears it.
func (s *Slot) SetHandle(h string) {
	if h == "" {
		s.o.Set("handle", nil)
		return
	}
	s.o.Set("handle", h)
}

// SetBranch records the checked-out branch.
func (s *Slot) SetBranch(b string) { s.o.Set("branch", b) }

// ClearSeen re-arms `round wait`: the slot's last seen row no longer counts.
func (s *Slot) ClearSeen() { s.o.Delete("seen") }

// SetSeen records the row `round wait` last reported.
func (s *Slot) SetSeen(key string) { s.o.Set("seen", key) }

// Worker event kinds: what `rota worker send`, `worker done` and `worker reply`
// record on the slot.
const (
	EventBlocked     = "blocked"
	EventQuestion    = "question"
	EventDone        = "done"
	EventReviewReply = "review-reply"
	// EventHeartbeat is a send kind only: it is recorded by Heartbeat and
	// never stands as a slot's event.
	EventHeartbeat = "heartbeat"
)

// EventKinds are the kinds SetEvent accepts.
var EventKinds = []string{EventBlocked, EventQuestion, EventDone, EventReviewReply, EventHeartbeat}

// Event is the latest event a worker sent for its slot. Seq is the slot's
// monotonic event id: it survives ClearEvent, so an acknowledged event never
// shares a key with the next one.
type Event struct {
	Seq        int
	Kind, Body string
	TS         string
	// Notified is set once the poll raised the host notification for it.
	Notified bool
}

// Event is the slot's latest worker-sent event, nil when none stands.
func (s *Slot) Event() *Event {
	v, _ := s.o.Get("event")
	o, ok := v.(*jsonx.Object)
	if !ok {
		return nil
	}
	n, _ := jsonx.Int(mustGet(o, "seq"))
	return &Event{Seq: n, Kind: jsonx.Str(o, "kind"), Body: jsonx.Str(o, "body"), TS: jsonx.Str(o, "ts"), Notified: jsonx.Bool(o, "notified")}
}

func mustGet(o *jsonx.Object, k string) any { v, _ := o.Get(k); return v }

// SetEvent records a worker event under the next id and stamps activeAt. It
// does not touch state; the writer decides which kinds move it. A heartbeat is
// not an event: it replaces nothing (see Heartbeat).
func (s *Slot) SetEvent(kind, body, now string) {
	seq, _ := jsonx.Int(mustGet(s.o, "eventSeq"))
	seq++
	s.o.Set("eventSeq", seq)
	e := jsonx.NewObject()
	e.Set("seq", seq)
	e.Set("kind", kind)
	e.Set("ts", now)
	if body != "" {
		e.Set("body", body)
	}
	s.o.Set("event", e)
	s.o.Set("activeAt", now)
}

// Heartbeat records that the worker is alive: heartbeatAt only. It leaves the
// standing event, the state and activeAt alone, so a question the worker has
// not had answered is not lost and the event stays newer than the last state
// change. LastAlive is what the stall clock reads.
func (s *Slot) Heartbeat(now string) { s.o.Set("heartbeatAt", now) }

// HeartbeatAt is the stamp of the worker's last heartbeat, "" for none.
func (s *Slot) HeartbeatAt() string { return jsonx.Str(s.o, "heartbeatAt") }

// LastAlive is the later of activeAt and heartbeatAt: the stall clock.
func (s *Slot) LastAlive() string {
	if hb := s.HeartbeatAt(); hb > s.ActiveAt() { // RFC 3339 UTC stamps order as strings
		return hb
	}
	return s.ActiveAt()
}

// MarkEventNotified records that the host notification for the standing event
// went out.
func (s *Slot) MarkEventNotified() {
	if v, ok := s.o.Get("event"); ok {
		if o, ok := v.(*jsonx.Object); ok {
			o.Set("notified", true)
		}
	}
}

// ClearEvent drops the standing event; eventSeq stays.
func (s *Slot) ClearEvent() { s.o.Delete("event") }

func parseStamp(v string) time.Time {
	t, _ := time.Parse("2006-01-02T15:04:05Z", v)
	return t
}

// Touch stamps activeAt.
func (s *Slot) Touch(now string) { s.o.Set("activeAt", now) }

// UpdateSlot edits one slot under the registry lock, reporting whether it was
// found. It is the one locked per-slot write.
func UpdateSlot(root, name string, mutate func(s *Slot)) (found bool, err error) {
	err = Update(root, func(d *Doc) {
		if s := d.Slot(name); s != nil {
			mutate(s)
			found = true
		}
	})
	return found, err
}

// UpdateSlots edits every slot under the registry lock.
func UpdateSlots(root string, mutate func(s *Slot)) error {
	return Update(root, func(d *Doc) {
		for _, s := range d.Slots() {
			mutate(s)
		}
	})
}

func slotsDefault() *jsonx.Object {
	def := jsonx.NewObject()
	def.Set("slots", []any{})
	return def
}
