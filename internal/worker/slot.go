package worker

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"github.com/l4ci/rota/internal/stamp"
	"strconv"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/exitcode"
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
	"limited": true, "needs-permission": true, "unknown": true, LivenessUnverifiable: true,
}

// ValidState reports whether MarkState accepts next.
func ValidState(next string) bool { return slotStates[next] }

func (s *Slot) Name() string     { return jsonx.Str(s.o, "name") }
func (s *Slot) Branch() string   { return jsonx.Str(s.o, "branch") }
func (s *Slot) Worktree() string { return jsonx.Str(s.o, "worktree") }
func (s *Slot) Base() string     { return jsonx.Str(s.o, "base") }
func (s *Slot) Handle() string   { return jsonx.Str(s.o, "handle") }

// RoutedTag is the issue's milestone tag as assign read it when it routed the
// slot (possibly ""), and ok false for a slot assigned before the tag was
// recorded. Drift is the tag changing since, never a milestone going active.
func (s *Slot) RoutedTag() (tag string, ok bool) {
	v, ok := s.o.Get("routedTag")
	str, _ := v.(string)
	return str, ok
}

// SetRoutedTag records the issue's milestone tag at assignment.
func (s *Slot) SetRoutedTag(tag string) { s.o.Set("routedTag", tag) }

// HomeBase is the base RouteBase remembered, "" when the slot is not routed.
func (s *Slot) HomeBase() string { return jsonx.Str(s.o, "homeBase") }

// RouteBase points the slot at target, the milestone branch of the issue it is
// given, remembering the base it was registered with; "" routes it back to
// that base. The registered base is what every verb reads when the slot is on
// no milestone, so nothing else changes.
func (s *Slot) RouteBase(target string) {
	home := jsonx.Str(s.o, "homeBase")
	switch {
	case target == "" && home != "":
		s.o.Set("base", home)
		s.o.Delete("homeBase")
	case target != "":
		if home == "" {
			s.o.Set("homeBase", s.Base())
		}
		s.o.Set("base", target)
	}
}

// WorkspaceID is the linked herdr workspace the slot's session was opened in
// (round.herdrGroupSlots), "" when it runs in the orchestrator's own workspace.
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

// BriefSettledAnswers preserves answers supplied only at assignment. Slots
// written before the rename keep their answers until their next context update.
func (s *Slot) BriefSettledAnswers() string {
	if v, ok := s.o.Get("briefSettledAnswers"); ok {
		text, _ := v.(string)
		return text
	}
	return jsonx.Str(s.o, "briefDecisions")
}

// BriefSiblings preserves the siblings supplied only at assignment.
func (s *Slot) BriefSiblings() []string { return strList(s.o, "briefSiblings") }

// BriefDecisionContext is the decision text captured for this assignment.
func (s *Slot) BriefDecisionContext() string { return jsonx.Str(s.o, "briefDecisionContext") }

// SetBriefDecisionContext stores the snapshot, or removes it when empty.
func (s *Slot) SetBriefDecisionContext(text string) {
	if text == "" {
		s.o.Delete("briefDecisionContext")
	} else {
		s.o.Set("briefDecisionContext", text)
	}
}

func (s *Slot) SetBriefContext(settledAnswers string, siblings []string) {
	s.o.Delete("briefDecisions")
	if settledAnswers == "" {
		s.o.Delete("briefSettledAnswers")
	} else {
		s.o.Set("briefSettledAnswers", settledAnswers)
	}
	setStrList(s.o, "briefSiblings", siblings)
}

func (s *Slot) PR() string      { return jsonx.Str(s.o, "pr") }
func (s *Slot) Account() string { return jsonx.Str(s.o, "account") }

// Attempt is the id minted for the slot's current assignment, "" for none. A
// worker's done and report carry the id from its brief; one that does not match
// is from an assignment that was reclaimed or transferred away.
func (s *Slot) Attempt() string { return jsonx.Str(s.o, "attempt") }

// AttemptBase is the tip of the slot's branch when the current attempt took it
// over, "" when the attempt minted the branch itself or none was recorded. The
// commits up to it belong to earlier attempts and carry their trailers; the
// gate judges only what was pushed after it.
func (s *Slot) AttemptBase() string { return jsonx.Str(s.o, "attemptBase") }

// NewAttempt mints an attempt id: 8 hex characters.
func NewAttempt() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

// Bundle is the member issues the slot holds beside its task, the lead
// (`rota round assign <lead> --bundle <id>,<id>`): one branch and one PR
// close them all. None for a slot holding one issue, and for a registry
// written before bundles.
func (s *Slot) Bundle() []string { return strList(s.o, "bundle") }

// SetBundle records the member issues; none removes the field.
func (s *Slot) SetBundle(ids []string) { setStrList(s.o, "bundle", ids) }

// BundleIssues is every issue the slot holds: the lead (HeldID) first, then
// its members. None when the slot holds nothing.
func (s *Slot) BundleIssues() []string {
	lead := s.HeldID()
	if lead == "" {
		return nil
	}
	return append([]string{lead}, s.Bundle()...)
}

// SmokeSection is the smoke section number reserved for the slot's issue, 0
// for none.
func (s *Slot) SmokeSection() int {
	n, _ := strconv.Atoi(jsonx.Str(s.o, "smokeSection"))
	return n
}
func (s *Slot) ConfigDir() string { return jsonx.Str(s.o, "configDir") }

// HomeAccount is the account entry (a codex or opencode entry of round.accounts)
// the slot last ran a worker under ("" on the kind's default home). The JSON
// key stays codexAccount so existing registries read.
func (s *Slot) HomeAccount() string { return jsonx.Str(s.o, "codexAccount") }

// SetHomeAccount records the account; "" removes it.
func (s *Slot) SetHomeAccount(name string) {
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
	s.o.Delete("homeBase")
	s.o.Delete("routedTag")
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
	s.ClearRelayPending()
	s.ClearEvent() // a new brief answers whatever the worker last sent
	s.o.Delete("outcome")
	if task == "" {
		return
	}
	s.o.Set("task", task)
	s.o.Set("pr", nil)
	s.o.Delete("reviewSeen")
	s.o.Set("relays", []any{})
	s.o.Delete("inbox")
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
	// Attempt is the id of this assignment, "" for none.
	Attempt string
	// AttemptBase is the branch tip the attempt inherited, "" for none.
	AttemptBase string
	// Bundle is the member issues held beside Task, none for one issue.
	Bundle []string
}

// Bind records the issue the slot works on. Empty optional fields stay unset.
func (s *Slot) Bind(b Binding) {
	s.SetBriefContext("", nil)
	s.SetBriefDecisionContext("")
	s.o.Delete("awaitingDone")
	s.o.Set("task", b.Task)
	for _, f := range []struct{ k, v string }{
		{"claimId", b.ClaimID}, {"kind", b.Kind}, {"kindSource", b.KindSource}, {"tier", b.Tier},
		{"model", b.Model}, {"tierReason", b.TierReason}, {"smokeSection", smokeStr(b.SmokeSection)}, {"attempt", b.Attempt},
		{"attemptBase", b.AttemptBase},
	} {
		if f.v == "" {
			s.o.Delete(f.k)
			continue
		}
		s.o.Set(f.k, f.v)
	}
	s.SetBundle(b.Bundle)
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
	s.SetBriefContext("", nil)
	s.SetBriefDecisionContext("")
	s.o.Set("task", nil)
	s.o.Set("pr", nil)
	for _, k := range []string{"prMerged", "claimId", "kind", "tier", "model", "tierReason", "smokeSection", "attempt", "attemptBase", "bundle", "issues", "reviewNoFindings", "reviewSeen", "event", "heartbeatAt", "outcome", "inbox", "finding", "awaitingDone"} {
		s.o.Delete(k)
	}
}

// AwaitingDone says the slot took over a branch whose PR it did not open (a
// transfer): its idle pane is not "opened the PR and stopped", so poll must not
// promote it from the forge. Its own done (event or sentinel) clears the mark.
func (s *Slot) AwaitingDone() bool { return jsonx.Bool(s.o, "awaitingDone") }

// SetAwaitingDone records or clears the mark.
func (s *Slot) SetAwaitingDone(v bool) {
	if v {
		s.o.Set("awaitingDone", true)
		return
	}
	s.o.Delete("awaitingDone")
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
	if next != LivenessUnverifiable {
		s.o.Delete("reason")
	}
	s.o.Set("state", next)
}

// Release is the pane going away: no handle, idle.
func (s *Slot) Release() {
	s.o.Set("handle", nil)
	s.o.Set("state", "idle")
}

// PRMerged says the forge showed the slot's recorded PR merged when the watch
// last looked: a done slot in that case waits on nothing from the orchestrator.
func (s *Slot) PRMerged() bool { return jsonx.Bool(s.o, "prMerged") }

// SetPRMerged records or clears the merged-PR mark.
func (s *Slot) SetPRMerged(v bool) {
	if v {
		s.o.Set("prMerged", true)
		return
	}
	s.o.Delete("prMerged")
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
// (`ROTA-DONE <slot> issues:#a,#b`). ReviewResult distinguishes a completed
// empty review from missing evidence.
func (s *Slot) Issues() []string { return strList(s.o, "issues") }

// strList is the string entries of the list under key, skipping any other.
func strList(o *jsonx.Object, key string) []string {
	l, _ := o.Get(key)
	list, _ := l.([]any)
	var out []string
	for _, e := range list {
		if v, ok := e.(string); ok {
			out = append(out, v)
		}
	}
	return out
}

// setStrList records vals under key; none removes the key.
func setStrList(o *jsonx.Object, key string, vals []string) {
	if len(vals) == 0 {
		o.Delete(key)
		return
	}
	l := make([]any, len(vals))
	for i, v := range vals {
		l[i] = v
	}
	o.Set(key, l)
}

// Finding is the comment id a research item's worker reported
// (`ROTA-DONE <slot> finding:<id>`), the slot's done evidence in place of a PR.
func (s *Slot) Finding() string {
	v, _ := s.o.Get("finding")
	id, _ := v.(string)
	return id
}

// SetFinding records the reported finding comment; "" clears it.
func (s *Slot) SetFinding(id string) {
	if id == "" {
		s.o.Delete("finding")
		return
	}
	s.o.Set("finding", id)
}

// SetIssues records the reported issues; none clears them.
func (s *Slot) SetIssues(refs []string) {
	result := ParseReviewResult("issues:" + strings.Join(refs, ","))
	s.SetReviewResult(result)
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

// RelayPending is the sequence number of the relay sent whose echo the pane
// has not shown a done line after; 0 for none.
func (s *Slot) RelayPending() int {
	v, _ := s.o.Get("relayPending")
	n, _ := jsonx.Int(v)
	return n
}

// SetRelayPending records relay k as sent at the stamp; ClearRelayPending ends
// the mark. A pane that has shown the relay's `[relay k]` line has it recorded
// by SetRelayMarkerSeen: a capture without the line is then a scrolled-out one.
func (s *Slot) SetRelayPending(k int, at string) {
	s.o.Set("relayPending", k)
	s.o.Set("relaySentAt", at)
	s.o.Set("relayEventSeq", s.eventSeq())
	s.o.Delete("relayMarkerSeen")
}

// RelayEventSeq is the slot's event id when the pending relay went out: an
// event with a higher Seq was sent after it. -1 when the relay predates the
// field.
func (s *Slot) RelayEventSeq() int {
	v, ok := s.o.Get("relayEventSeq")
	if !ok {
		return -1
	}
	n, _ := jsonx.Int(v)
	return n
}

func (s *Slot) eventSeq() int {
	n, _ := jsonx.Int(mustGet(s.o, "eventSeq"))
	return n
}

// RelaySentAt is when the pending relay went out; the zero time for none.
func (s *Slot) RelaySentAt() time.Time {
	v, _ := s.o.Get("relaySentAt")
	str, _ := v.(string)
	return parseStamp(str)
}

// RelayMarkerSeen: a capture has shown the pending relay's `[relay k]` line.
func (s *Slot) RelayMarkerSeen() bool { v, _ := s.o.Get("relayMarkerSeen"); b, _ := v.(bool); return b }

// SetRelayMarkerSeen records that the pending relay's line has been seen.
func (s *Slot) SetRelayMarkerSeen() { s.o.Set("relayMarkerSeen", true) }

// ClearRelayPending removes the relay-pending mark and what goes with it.
func (s *Slot) ClearRelayPending() {
	s.o.Delete("relayPending")
	s.o.Delete("relaySentAt")
	s.o.Delete("relayEventSeq")
	s.o.Delete("relayMarkerSeen")
}

// ClearSeen re-arms `round wait`: the slot's last seen row no longer counts.
func (s *Slot) ClearSeen() { s.o.Delete("seen") }

// SetSeen records the row `round wait` last reported.
func (s *Slot) SetSeen(key string) { s.o.Set("seen", key) }

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
	seq := s.eventSeq() + 1
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

// Outcome values: what a worker reports with `rota worker done --outcome`.
const (
	OutcomeDone    = "done"
	OutcomePartial = "partial"
	OutcomeFailed  = "failed"
)

// Outcomes are the values `worker done --outcome` accepts.
var Outcomes = []string{OutcomeDone, OutcomePartial, OutcomeFailed}

// Outcome is the outcome the worker last reported and its three summary
// lines; "" for none. Unlike the event it survives a pane going busy: only a
// new brief (Dispatch) or the worker's next report replaces it.
func (s *Slot) Outcome() (kind string, summary []string) {
	v, _ := s.o.Get("outcome")
	o, ok := v.(*jsonx.Object)
	if !ok {
		return "", nil
	}
	l, _ := o.Get("summary")
	list, _ := l.([]any)
	for _, e := range list {
		if line, ok := e.(string); ok {
			summary = append(summary, line)
		}
	}
	return jsonx.Str(o, "kind"), summary
}

// SetOutcome records the worker's reported outcome with its summary lines.
func (s *Slot) SetOutcome(kind string, summary []string, now string) {
	o := jsonx.NewObject()
	o.Set("kind", kind)
	lines := make([]any, len(summary))
	for i, l := range summary {
		lines[i] = l
	}
	o.Set("summary", lines)
	o.Set("ts", now)
	s.o.Set("outcome", o)
}

// ClearEvent drops the standing event; eventSeq stays.
func (s *Slot) ClearEvent() { s.o.Delete("event") }

// parseStamp is the registry's own activeAt as a time. The registry writes it
// through stamp.Format, so a value that does not parse is no stamp at all and
// reads as the zero time: no last change, so a standing event still counts.
// An event's ts is parsed by decide, which reports the error.
func parseStamp(v string) time.Time {
	t, _ := stamp.Parse(v)
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

// FenceAttempt refuses (exit 4) a completion carrying an attempt id that is not
// the slot's current one: the worker's assignment was reclaimed or transferred.
// "" is a caller that carries no id, which no fence applies to. The message is
// one line and does not name the current id, so a fenced worker cannot copy it.
func FenceAttempt(s *Slot, given string) error {
	if given == "" || given == s.Attempt() {
		return nil
	}
	e := &exitcode.Error{Exit: exitcode.ExitRefused,
		Message: fmt.Sprintf("slot %s: attempt %s is stale, the assignment was reclaimed or transferred; stop and do not retry", s.Name(), given)}
	e.Hint = "a fenced worker stops: its result is refused, not recorded"
	return e
}

// OutcomeOpen reports whether kind is an open outcome (partial or failed):
// the worker delivered less than the ticket asked.
func OutcomeOpen(kind string) bool { return kind == OutcomePartial || kind == OutcomeFailed }
