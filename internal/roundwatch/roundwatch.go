// Package roundwatch is the logic behind `rota round watch` (#81): a
// background-friendly watcher that exits, with what changed, whenever the
// orchestrator has something to look at, and at least every heartbeat. The
// orchestrator keeps exactly one running, so an orchestrator that is busy
// talking to the maintainer is still woken when a worker finishes.
//
// The watch process leaves a marker next to the round lease. The Stop hook
// reads it to refuse an idle orchestrator without one, and the prompt hook
// shows whether it is armed. Everything here takes its clock, process table
// and slot source from the caller, so tests need no host, forge or sleep.
package roundwatch

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/escalation"
	"github.com/l4ci/rota/internal/landfreeze"
	"github.com/l4ci/rota/internal/pidlive"
	"github.com/l4ci/rota/internal/rotastate"
	"github.com/l4ci/rota/internal/roundlease"
	"github.com/l4ci/rota/internal/stamp"
	"github.com/l4ci/rota/internal/strutil"
	"github.com/l4ci/rota/internal/worker"
)

// MarkerFile is the watch marker under <git-common-dir>/rota/.
const MarkerFile = "round-watch.json"

// Marker is the marker document: who is watching, since when, how often it
// reports back regardless.
type Marker struct {
	PID       int    `json:"pid"`
	Start     uint64 `json:"start,omitempty"`
	Host      string `json:"host"`
	StartedAt string `json:"startedAt"`
	Heartbeat int    `json:"heartbeatSeconds"`
}

// MarkerPath is the marker file under a common dir.
func MarkerPath(commonDir string) string { return rotastate.File(commonDir, MarkerFile) }

// Armed reports the live watch of the repo, if any. A marker whose process is
// gone, or on another host, is not armed: nothing can be checked about it.
func Armed(env roundlease.Env, commonDir string) (Marker, bool) {
	return markerFile(commonDir).ReadLive(func(m Marker) bool { return env.Running(m.Host, m.PID, m.Start) })
}

// OwnerPID implements pidlive.Record.
func (m Marker) OwnerPID() int { return m.PID }

func markerFile(commonDir string) pidlive.Marker[Marker] {
	return pidlive.Marker[Marker]{Path: MarkerPath(commonDir), Codec: pidlive.JSON[Marker]()}
}

// ArmedError is returned by Arm when another watch is live.
type ArmedError struct{ Marker Marker }

func (e *ArmedError) Error() string {
	return fmt.Sprintf("a watch is already armed for this repo (pid %d, since %s)", e.Marker.PID, e.Marker.StartedAt)
}

// Arm writes the marker for the calling process and returns the function that
// removes it. Another live watch is an *ArmedError and nothing is written: one
// watch at a time keeps every wake-up a single event.
func Arm(env roundlease.Env, commonDir string, pid int, heartbeat time.Duration) (release func(), err error) {
	file := markerFile(commonDir)
	m := Marker{PID: pid, StartedAt: stamp.Format(env.Now()), Heartbeat: int(heartbeat.Seconds())}
	m.Host, m.Start = env.Identity(pid)
	live := func(m Marker) bool { return env.Running(m.Host, m.PID, m.Start) }
	cur, held, err := file.Claim(m, live)
	if err != nil {
		return nil, err
	}
	if held {
		return nil, &ArmedError{Marker: cur}
	}
	// Only our own marker: a successor may have replaced a dead one.
	return func() { _ = file.Remove(pid) }, nil
}

// Reasons a watch returns.
const (
	ReasonSlot      = "slot"      // a slot needs attention (the `round wait` verdict)
	ReasonChange    = "change"    // a PR or an escalation changed
	ReasonHeartbeat = "heartbeat" // nothing happened for the heartbeat interval
	ReasonInterrupt = "interrupt" // the context ended
)

// EntryKind says what a snapshot entry is about.
type EntryKind string

// The snapshot entry kinds. The forge-owned ones are those carry keeps.
const (
	KindSlotPR           EntryKind = "pr"                // a slot's PR url (registry)
	KindPRState          EntryKind = "pr-state"          // a slot's PR state (forge)
	KindEscalation       EntryKind = "escalation"        // an escalation's stored status (registry)
	KindEscalationStatus EntryKind = "escalation-status" // an escalation's forge-checked status
	KindReview           EntryKind = "review"            // review input waiting on a done slot's PR
	KindGate             EntryKind = "gate"              // the land lock, present while held
	KindSupport          EntryKind = "support"           // a support entry, Sub is its Support* kind
)

// kindPrefix is the text form of each kind, longest first where one is a
// prefix of another. It is the only place the old prefix grammar survives.
var kindPrefix = []struct {
	kind   EntryKind
	prefix string
}{
	{KindPRState, "pr/state/"},
	{KindSlotPR, "pr/"},
	{KindEscalationStatus, "escalation/status/"},
	{KindEscalation, "escalation/"},
	{KindReview, "review/"},
	{KindSupport, "support/"},
}

// Key identifies one snapshot entry: its kind, the slot, escalation or issue
// it concerns, and for a support entry the support kind. It is comparable, so
// a Snapshot keys on it directly.
type Key struct {
	Kind EntryKind
	ID   string
	Sub  string
}

// GateKey is the snapshot key of the land lock: present while a gate or train
// holds it, its value the holder. Its disappearing is the "gate cleared" change
// that tells an orchestrator whose assign was refused to assign now.
var GateKey = Key{Kind: KindGate}

// String is the display and wire form (`pr/ben`, `support/7/comment`).
func (k Key) String() string {
	if k.Kind == KindGate {
		return "gate/land"
	}
	for _, p := range kindPrefix {
		if p.kind != k.Kind {
			continue
		}
		if k.Kind == KindSupport {
			return p.prefix + k.ID + "/" + k.Sub
		}
		return p.prefix + k.ID
	}
	return string(k.Kind) + "/" + k.ID
}

// forgeOwned is a kind only the forge can speak to: a tick that skips the
// forge carries these over instead of reading their absence as a change.
func (k Key) forgeOwned() bool {
	switch k.Kind {
	case KindPRState, KindEscalationStatus, KindReview, KindSupport:
		return true
	}
	return false
}

// MarshalText and UnmarshalText keep the seen file readable under its old
// string keys.
func (k Key) MarshalText() ([]byte, error) { return []byte(k.String()), nil }

func (k *Key) UnmarshalText(b []byte) error {
	s := string(b)
	if s == "gate/land" {
		*k = GateKey
		return nil
	}
	for _, p := range kindPrefix {
		rest, ok := strings.CutPrefix(s, p.prefix)
		if !ok {
			continue
		}
		if p.kind == KindSupport {
			id, sub, found := strings.Cut(rest, "/")
			if !found {
				return fmt.Errorf("support key %q has no kind", s)
			}
			*k = Key{Kind: p.kind, ID: id, Sub: sub}
			return nil
		}
		*k = Key{Kind: p.kind, ID: rest}
		return nil
	}
	return fmt.Errorf("unknown snapshot key %q", s)
}

// Snapshot is what the watch compares between polls.
type Snapshot map[Key]string

// SlotNews is what the slot wait found.
type SlotNews struct {
	Slot, State, Evidence, Source string
}

// Change is one entry of the snapshot that moved.
type Change struct {
	Key      Key
	From, To string
}

// Result is what a watch returns.
type Result struct {
	Reason  string
	Slot    *SlotNews
	Changes []Change
	Waited  time.Duration
}

// ErrNothingToWatch is what Env.Wait returns when no slot has a session to
// watch (every slot idle, or none registered). The watch then waits out the
// interval on the registry and the forge alone.
var ErrNothingToWatch = errors.New("no slot with a session to watch")

// Env is what Run touches; the CLI wires the real ones.
type Env struct {
	Now   func() time.Time
	Sleep func(ctx context.Context, d time.Duration)
	// Wait blocks up to d for a slot that needs attention. nil news with a nil
	// error means d passed quietly.
	Wait func(ctx context.Context, d time.Duration) (*SlotNews, error)
	// Local is the cheap snapshot (the registry), taken every tick.
	Local func() Snapshot
	// Forge refreshes what only the forge can say (escalation answers, PR
	// states) and returns its snapshot entries. It runs every ForgeEvery.
	Forge func(ctx context.Context) Snapshot
	// Seen is what the orchestrator last saw of the support entries, and Mark
	// records the ones a returned watch just reported. The support entries'
	// baseline is Seen, not the first poll: a support PR or comment that is
	// already there when the watch starts and was never reported is news (the
	// 2026-10 report: a start-time baseline swallowed two open support PRs).
	// Both nil: the first poll is the baseline, as for every other entry.
	Seen func() Snapshot
	Mark func(Snapshot)
	// OnHeartbeat runs when the watch returns for ReasonHeartbeat. The CLI
	// uses it to forget returned permission prompts, so the next watch
	// returns one that is still unanswered (#3088). May be nil.
	OnHeartbeat func()
}

// Opts are the intervals of a watch.
type Opts struct {
	Heartbeat  time.Duration // return after this long with nothing to report; > 0
	Poll       time.Duration // longest single wait, and the local snapshot cadence
	ForgeEvery time.Duration // forge cadence; <= 0 never asks the forge
}

// Run watches until a slot needs attention, a snapshot entry changes, the
// heartbeat passes or ctx ends. The baseline is taken before the first wait,
// so a change that happened while the previous watch was being re-armed
// shows up as the difference between the registry and what the orchestrator
// last read, not here: a watch reports what moves while it runs.
func Run(ctx context.Context, env Env, o Opts) (Result, error) {
	start := env.Now()
	base := env.Local()
	if o.ForgeEvery > 0 {
		base = merge(base, env.Forge(ctx))
	}
	lastForge := start
	if env.Seen != nil {
		for k := range supportOnly(base) {
			delete(base, k)
		}
		base = merge(base, env.Seen())
	}
	for {
		elapsed := env.Now().Sub(start)
		if elapsed >= o.Heartbeat {
			if env.OnHeartbeat != nil {
				env.OnHeartbeat()
			}
			return Result{Reason: ReasonHeartbeat, Waited: elapsed}, nil
		}
		d := minDur(o.Poll, o.Heartbeat-elapsed)
		news, err := env.Wait(ctx, d)
		switch {
		case ctx.Err() != nil:
			return Result{Reason: ReasonInterrupt, Waited: env.Now().Sub(start)}, nil
		case errors.Is(err, ErrNothingToWatch):
			env.Sleep(ctx, d)
		case err != nil:
			return Result{}, err
		case news != nil:
			return Result{Reason: ReasonSlot, Slot: news, Waited: env.Now().Sub(start)}, nil
		}
		cur := env.Local()
		if o.ForgeEvery > 0 && env.Now().Sub(lastForge) >= o.ForgeEvery {
			lastForge = env.Now()
			cur = merge(cur, env.Forge(ctx))
		} else {
			cur = merge(cur, carry(base))
		}
		// A lock taken or handed on mid-watch is no news: fold it into the
		// baseline so only its release wakes the watch.
		if v, ok := cur[GateKey]; ok {
			base[GateKey] = v
		}
		if ch := supportNews(Diff(base, cur), env.Seen != nil); len(ch) > 0 {
			if env.Mark != nil {
				env.Mark(supportOnly(cur))
			}
			return Result{Reason: ReasonChange, Changes: ch, Waited: env.Now().Sub(start)}, nil
		}
	}
}

// supportNews drops the support entries that vanished: a forge read that
// failed or an issue handed back is nothing new. It applies only when the
// baseline is the seen record, whose entries outlive the poll that made them.
func supportNews(ch []Change, seen bool) []Change {
	if !seen {
		return ch
	}
	out := ch[:0:0]
	for _, c := range ch {
		if c.Key.Kind == KindSupport && c.To == "" {
			continue
		}
		out = append(out, c)
	}
	return out
}

// carry keeps the forge-owned entries of the baseline, so a tick that did not
// ask the forge does not read their absence as a change.
func carry(base Snapshot) Snapshot {
	out := Snapshot{}
	for k, v := range base {
		if k.forgeOwned() {
			out[k] = v
		}
	}
	return out
}

func merge(a, b Snapshot) Snapshot {
	out := Snapshot{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

func minDur(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

// Diff lists the keys whose value differs between two snapshots, sorted.
func Diff(prev, cur Snapshot) []Change {
	var out []Change
	for k, v := range cur {
		if p, ok := prev[k]; !ok || p != v {
			out = append(out, Change{Key: k, From: prev[k], To: v})
		}
	}
	for k, p := range prev {
		if _, ok := cur[k]; !ok {
			out = append(out, Change{Key: k, From: p})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key.String() < out[j].Key.String() })
	return out
}

// LocalSnapshot is the registry's part of the snapshot: each slot's PR and the
// stored status of each escalation. A slot's own state is not here: `round
// wait` reports it, and the orchestrator's own dispatch would otherwise wake
// the watch it just armed.
func LocalSnapshot(we worker.Env, root string) Snapshot {
	we = we.WithDefaults()
	out := Snapshot{}
	// Tolerant: a hook digest cannot refuse; a corrupt registry reads as no slots.
	reg := we.Registry(root)
	for _, s := range reg.Slots() {
		if pr := s.PR(); pr != "" {
			out[Key{Kind: KindSlotPR, ID: s.Name()}] = pr
		}
	}
	for _, e := range escalation.FromRegistry(reg) {
		out[Key{Kind: KindEscalation, ID: e.ID}] = e.Status
	}
	if held, err := we.LandLockHeld(context.Background(), root); err == nil && held {
		out[GateKey] = strutil.FirstNonEmpty(we.LandLockHolder(context.Background(), root), "held")
	}
	return out
}

// PRStateKey is the snapshot key of a slot's PR state, which only the forge knows.
func PRStateKey(slot string) Key { return Key{Kind: KindPRState, ID: slot} }

// ReviewKey is the snapshot key of the review input waiting on a done slot's
// PR; its value says how much and from whom. It moves when a new batch arrives
// and when the batch is consumed.
func ReviewKey(slot string) Key { return Key{Kind: KindReview, ID: slot} }

// Support entry kinds (#1827): the PR head of an offered or adopted issue, a
// PR that exists for a support issue, and the newest comment the other
// machine left on an offered or held issue.
const (
	SupportPRHead   = "pr-head"
	SupportPROpened = "pr-opened"
	SupportComment  = "comment"
	// SupportBranchHead is the head of a support branch with no open PR (#1918).
	SupportBranchHead = "branch-head"
	// SupportConflict is the head sha of a support PR the forge reports as
	// conflicting with its base (#1911); it wakes once per head.
	SupportConflict = "conflict"
)

// SupportBranchID is the id slot of a branch-keyed support entry: the branch
// with "/" as "~", which a ref name cannot contain, so the key still splits
// into id and kind at its first "/".
func SupportBranchID(branch string) string { return strings.ReplaceAll(branch, "/", "~") }

// SupportKey is the snapshot key of one support entry of issue id.
func SupportKey(id, kind string) Key { return Key{Kind: KindSupport, ID: id, Sub: kind} }

// SupportChange reads a support entry back out of a change: the issue, the
// kind and the text for the change line. A comment's value is
// `<author>#<comment id>` and reads as the author. ok is false for any other
// key.
func SupportChange(ch Change) (id, kind, to string, ok bool) {
	if ch.Key.Kind != KindSupport {
		return "", "", "", false
	}
	id, kind = ch.Key.ID, ch.Key.Sub
	to = ch.To
	if kind == SupportComment {
		to, _, _ = strings.Cut(to, "#")
	}
	if kind == SupportBranchHead {
		id = strings.ReplaceAll(id, "~", "/")
	}
	return id, kind, to, true
}

func supportOnly(m Snapshot) Snapshot {
	out := Snapshot{}
	for k, v := range m {
		if k.Kind == KindSupport {
			out[k] = v
		}
	}
	return out
}

// EscalationStatusKey is the snapshot key of the forge-checked escalation status.
func EscalationStatusKey(id string) Key { return Key{Kind: KindEscalationStatus, ID: id} }

// Digest is the one-line round state the prompt hook shows: each active
// slot with its issue and PR, the open escalations and whether a watch is armed.
func Digest(we worker.Env, root string, round int, armed bool) string {
	line := roundState(we, root, round)
	if armed {
		return line + ". Watch armed."
	}
	return line + ". NO WATCH ARMED: run `rota round watch` in the background now."
}

// ExitDigest describes the round as its watch returns and must be re-armed.
func ExitDigest(we worker.Env, root string, round int) string {
	return roundState(we, root, round) + ". Watch exiting; re-arm `rota round watch`."
}

// activeSlot is a slot the round is still waiting on.
type activeSlot struct {
	slot  *worker.Slot
	state string
	// wake: the slot needs the orchestrator (a done slot whose PR is not merged).
	wake bool
}

// roundView is the one walk over the registry that the digest and the Stop
// hook both read: the slots that are not idle and the escalations still open.
type roundView struct {
	active []activeSlot
	open   int
}

func viewOf(reg worker.Registry) roundView {
	var v roundView
	for _, s := range reg.Slots() {
		st := strings.ToLower(s.State())
		if worker.IsIdle(st) {
			continue
		}
		v.active = append(v.active, activeSlot{slot: s, state: st, wake: worker.NeedsWake(st) && !s.PRMerged()})
	}
	v.open = len(escalation.Open(escalation.FromRegistry(reg)))
	return v
}

// needsWatch: anything a watch would wake the orchestrator for, and the slots
// already waiting on it.
func (v roundView) needsWatch() (bool, []string) {
	var attn []string
	for _, a := range v.active {
		if a.wake {
			attn = append(attn, a.slot.Name()+" "+a.state)
		}
	}
	return len(v.active) > 0 || v.open > 0, attn
}

func roundState(we worker.Env, root string, round int) string {
	we = we.WithDefaults()
	ctx, run := we.Ctx, we.Git
	var parts []string
	if frozen, err := landfreeze.Load(ctx, run, root); err != nil {
		if !errors.Is(err, rotastate.ErrNotRepo) {
			parts = append(parts, landfreeze.UnreadableLine(err))
		}
	} else if frozen != nil && frozen.Active {
		parts = append(parts, frozen.Line())
	}
	// Tolerant: a hook digest cannot refuse; a corrupt registry reads as no slots.
	v := viewOf(we.Registry(root))
	for _, a := range v.active {
		s, st := a.slot, a.state
		p := s.Name() + " " + st
		if lv := worker.JudgeLiveness(worker.LivenessEvidence{State: st, Reason: s.Reason()}); lv.State == worker.LivenessUnverifiable && s.Reason() != "" {
			p += ":" + s.Reason()
		}
		is := s.Issue()
		if is == "" {
			is = s.HeldID()
		}
		if is != "" {
			p += " #" + strings.TrimPrefix(is, "#")
		}
		if pr := s.PR(); pr != "" {
			p += " " + pr
		}
		if a.wake {
			p += " (needs you)"
		}
		parts = append(parts, p)
	}
	if v.open > 0 {
		parts = append(parts, fmt.Sprintf("%d escalation(s) pending", v.open))
	}
	line := "rota round"
	if round > 0 {
		line += fmt.Sprintf(" %d", round)
	}
	if len(parts) == 0 {
		line += ": no active slots"
	} else {
		line += ": " + strings.Join(parts, "; ")
	}
	return line
}

// NeedsWatch is the Stop hook's question: is there anything a watch would
// wake the orchestrator for? A slot that is not idle, or an escalation still
// pending. An all-idle round has nothing to wait on, so the orchestrator may
// stop (it has candidates to assign, not events to watch).
func NeedsWatch(we worker.Env, root string) (bool, []string) {
	return viewOf(we.WithDefaults().Registry(root)).needsWatch()
}

// StopBlock refuses an idle orchestrator with active workers and no live
// watch: nothing would wake it when a worker finishes. Solo rounds have no
// panes to watch. The caller has established that this is the orchestrator
// and that this is the first stop in the turn, so a refused watch cannot loop.
func StopBlock(we worker.Env, env roundlease.Env, root, commonDir string) (reason string, block bool) {
	reg := we.WithDefaults().Registry(root)
	if worker.IsSoloHost(reg.Host()) {
		return "", false
	}
	need, attn := viewOf(reg).needsWatch()
	if !need {
		return "", false
	}
	if _, armed := Armed(env, commonDir); armed {
		return "", false
	}
	reason = "No `rota round watch` is running, and workers are active: nothing would wake you when one finishes. " +
		"Start `rota round watch` as a background command now (one at a time; it exits with JSON when a slot, PR or escalation changes, or at its heartbeat), then stop."
	if len(attn) > 0 {
		reason += " Waiting on you already: " + strings.Join(attn, ", ") + "."
	}
	return reason, true
}
