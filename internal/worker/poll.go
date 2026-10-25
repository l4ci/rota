package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/ledger"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/stamp"
	"github.com/l4ci/rota/internal/strutil"
	"github.com/l4ci/rota/internal/tracker"
)

// Worker slot polling: capture each slot's pane, classify it (pane.go), record
// the verdict and promote an idle slot whose PR is open.

// relayEchoEvidence is the evidence of a done line read while the pending
// relay's echo is not in the pane.
const relayEchoEvidence = "relay sent, its echo is not in the pane yet; the ROTA-DONE shown predates it"

// gateRelayEcho holds a done row busy while the pane cannot show it answers
// the pending relay (#1017). Before the relay's `[relay K]` line has ever been
// seen, a done in the capture is an earlier turn's. Once it has been seen, a
// capture without it means it scrolled out, so a done in the capture came
// after it. A done event the worker sent after the relay passes: the event, not
// the pane, decides that row.
func gateRelayEcho(t pollTarget, st, ev, text string, lines int) (string, string) {
	if t.relayPending == 0 || st != StateDone || doneEventAfterRelay(t) {
		return st, ev
	}
	if t.relayMarker {
		if doneAfterRelay(t.kind, text, lines, t.relayPending) {
			return st, ev
		}
	} else if t.relaySeen {
		return st, ev
	}
	return StateBusy, relayEchoEvidence
}

// doneEventAfterRelay: the slot's standing event is a `worker done` sent after
// the pending relay went out. The event's seq decides it, since stamps have
// second precision and a stale done can share the relay's second; a relay
// recorded before the seq existed falls back to the stamp.
func doneEventAfterRelay(t pollTarget) bool {
	if t.ev == nil || t.ev.Kind != EventDone {
		return false
	}
	if t.relayEvSeq >= 0 {
		return t.ev.Seq > t.relayEvSeq
	}
	ts, err := stamp.Parse(t.ev.TS)
	return err == nil && !ts.Before(t.relaySentAt)
}

// relayFallbackWindow is how long an unseen relay holds a slot busy when
// round.stallMinutes is 0 (stall detection off), so a collapsed paste cannot
// hold it until the next dispatch.
const relayFallbackWindow = 30 * time.Minute

// noteRelayEcho records what the captures showed about each pending relay:
// its `[relay K]` line seen, which is what tells a scrolled-out marker from an
// unrendered one, or the relay given up on after round.stallMinutes without
// the line (a collapsed paste shows none), which would hold the slot busy for
// good. With round.stallMinutes at 0 the window is relayFallbackWindow. It updates the targets it was given, which a wait keeps across passes.
func (e Env) noteRelayEcho(root string, targets []pollTarget, now time.Time) {
	var stall time.Duration
	loaded := false
	for i, t := range targets {
		if t.relayPending == 0 {
			continue
		}
		k := t.relayPending
		switch {
		case t.relayMarker && !t.relaySeen:
			if _, err := UpdateSlot(root, t.name, func(s *Slot) {
				if s.RelayPending() == k {
					s.SetRelayMarkerSeen()
				}
			}); err == nil {
				targets[i].relaySeen = true
			}
		case !t.relaySeen:
			if !loaded {
				loaded = true
				if set, err := roundcfg.FromConfig(e.WithDefaults().Config(root)); err == nil {
					stall = time.Duration(set.StallMinutes) * time.Minute
				}
			}
			if stall <= 0 {
				stall = relayFallbackWindow
			}
			if !t.relaySentAt.IsZero() && now.Sub(t.relaySentAt) >= stall {
				if _, err := UpdateSlot(root, t.name, func(s *Slot) {
					if s.RelayPending() == k {
						s.ClearRelayPending()
					}
				}); err == nil {
					targets[i].relayPending = 0
				}
			}
		}
	}
}

// PollRow is one slot's verdict.
type PollRow struct {
	Name, State, Evidence string
}

// missingSentinelNote marks a row promoted to DONE from the forge.
const missingSentinelNote = "no ROTA-DONE sentinel; PR found by branch head"

// promoteIdleWithPR turns an IDLE row with no sentinel into DONE when the open
// PR headed by the slot's own branch exists: the worker opened its PR and
// stopped on a prose summary. A slot with no such PR, or a forge that cannot
// be asked, stays IDLE (stuck), as before. notes names each promoted slot.
func (e Env) promoteIdleWithPR(ctx context.Context, root string, rows []PollRow) (out []PollRow, notes map[string]string) {
	idle := map[string]PollRow{}
	for _, r := range rows {
		// A done event is recorded before the PR is opened: its row has no PR
		// yet, the same hole as an idle pane that printed no sentinel.
		if (r.State == StateIdle && r.Evidence != staleDoneEvidence) || (r.State == StateDone && r.Evidence == noPREvidence) {
			idle[r.Name] = r
		}
	}
	if len(idle) == 0 {
		return rows, nil
	}
	reg := LoadRegistryTolerant(root) // Wait already read the registry strictly; this only adds notes
	openPRs := e.openPRsByHead(ctx, root, reg, idle)
	for i, r := range rows {
		s := reg.Slot(r.Name)
		if _, ok := idle[r.Name]; !ok || s == nil || s.Branch() == "" || IsOwnPark(s.Branch(), s.Name()) {
			continue
		}
		if url := openPRs[s.Branch()]; url != "" {
			rows[i] = PollRow{r.Name, StateDone, url}
			if notes == nil {
				notes = map[string]string{}
			}
			notes[r.Name] = missingSentinelNote
		}
	}
	return rows, notes
}

// PollOpts are the flags of `rota worker poll`.
type PollOpts struct {
	Slot   string
	Settle time.Duration // default 3s
	Lines  int           // default 60
}

// PollResult is the verdict list and whether the registry changed.
type PollResult struct {
	Slots   []PollRow
	Notes   map[string]string // slot -> why its state was not read from a sentinel
	Changed bool
}

// PollFixture classifies a pane text file instead of talking to a host. It
// exists so the classifier is testable without a live session. It writes
// nothing.
func PollFixture(path, slot, kind, status string, lines int) (PollResult, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return PollResult{}, fail(exitcode.ExitUsage, "fixture not found: "+path)
	}
	if slot == "" {
		slot = "fixture"
	}
	if lines <= 0 {
		lines = 60
	}
	st, ev := ClassifyFor(kind, string(b), false, lines, status)
	return PollResult{Slots: []PollRow{{slot, st, ev}}}, nil
}

// Poll classifies the worker slots through the host and records each slot's
// state in .rota/workers.json, plus the PR URL from `ROTA-DONE <slot> <pr-url>` in
// slot.pr, so the gate merges through the PR instead of falling back to a
// local merge. A slot that newly turns BLOCKED or NEEDS-PERMISSION raises a
// host notification (herdr only).
func (e Env) Poll(ctx context.Context, root string, o PollOpts) (PollResult, error) {
	e = e.WithDefaults()
	if o.Settle < 0 {
		o.Settle = 0
	}
	if o.Lines <= 0 {
		o.Lines = 60
	}
	if err := SoloRefusal(root, "rota round report records a subagent's state"); err != nil {
		return PollResult{}, err
	}
	h := e.NewHost(e.hostKind(root))
	if err := h.Require(); err != nil {
		return PollResult{}, fail(exitcode.ExitUnavailable, err.Error())
	}
	reg, err := LoadRegistry(root)
	if err != nil {
		return PollResult{}, err
	}
	if o.Slot != "" && reg.Slot(o.Slot) == nil {
		return PollResult{}, fail(exitcode.ExitResolution, fmt.Sprintf("slot '%s' is not in the pool", o.Slot))
	}
	var targets []pollTarget
	for _, s := range reg.Slots() {
		if (o.Slot != "" && s.Name() != o.Slot) || s.IsExternal() { // an adopted slot has no pane
			continue
		}
		targets = append(targets, slotTarget(s))
	}
	if len(targets) == 0 {
		return PollResult{}, nil
	}
	before := ChangeCount(root)

	rows, _, _ := e.classify(ctx, h, targets, o.Settle, o.Lines)
	e.noteRelayEcho(root, targets, e.Now())
	rows, notes := e.promoteIdleWithPR(ctx, root, rows)
	notified := map[string]int{} // slot -> seq of the event whose notification went out
	for i, r := range rows {
		// Notify on the transition only: a poll loop re-reading a stuck slot
		// must not ring every few seconds. A blocked event moves the slot to
		// blocked when it is sent, so its transition is the first poll to read
		// it, once per event.
		t := targets[i]
		fresh := t.evSeq > 0 && r.State == StateBlocked && !t.ev.Notified
		if ((r.State == StateBlocked || r.State == StateNeedsPermission) && t.prev != strings.ToLower(r.State)) || fresh {
			h.Notify(ctx, fmt.Sprintf("rota worker %s: %s", r.Name, r.State), r.Evidence)
			if fresh {
				notified[r.Name] = t.evSeq
			}
		}
	}

	byName := map[string]PollRow{}
	for _, r := range rows {
		byName[r.Name] = r
	}
	openPRs := e.openPRsByHead(ctx, root, reg, byName)
	var rowErr error
	var dones []ledger.Entry
	if err := UpdateSlots(root, func(s *Slot) {
		r, ok := byName[s.Name()]
		if !ok {
			return
		}
		// A dispatch since the snapshot replaced the session this row was
		// read from: its verdict (a killed pane reads dead) is not the slot's.
		if s.PaneHandle() != handleOf(targets, s.Name()) {
			return
		}
		prev := s.State()
		skipped, err := recordRow(s, r, targetOf(targets, r.Name), e.Now(), reg)
		if err != nil && rowErr == nil {
			rowErr = err
		}
		if skipped { // a newer event arrived; the next poll reads against it
			return
		}
		if ev := s.Event(); ev != nil && ev.Seq == notified[s.Name()] {
			s.MarkEventNotified()
		}
		if d, ok := paneDone(prev, s, r); ok {
			dones = append(dones, d)
		}
		// A worker can open its PR and never print the sentinel (or print it
		// without a URL): the slot would stay busy and the gate would not find
		// the PR. The open PR headed by the slot's own branch is the record.
		if s.PR() == "" && s.Branch() != "" && !IsOwnPark(s.Branch(), s.Name()) {
			if url := openPRs[s.Branch()]; url != "" {
				s.SetPR(url)
			}
		}
	}); err != nil {
		return PollResult{}, err
	}
	memo := &RoundMemo{}
	for _, d := range dones {
		LedgerDoneIn(e, memo, ctx, e.Accounts, root, d)
	}
	if rowErr != nil {
		return PollResult{}, rowErr
	}
	syncChanged(ctx, h, root, rows, targets)
	return PollResult{Slots: rows, Notes: notes, Changed: ChangeCount(root) != before}, nil
}

// openPRs lists the forge's open PRs. Best effort: ok is false with no origin,
// a local forge, no forge or a failing listing, where nothing is known.
func (e Env) openPRs(ctx context.Context, root string) (prs []tracker.PR, ok bool) {
	if _, code := e.runGit(ctx, root, "remote", "get-url", "origin"); code != 0 {
		return nil, false
	}
	cfg := e.GateConfig(root)
	provider, _ := e.detectProvider(ctx, root, "", cfg.Tracker.Provider, tracker.ForgeReader) // a reader never fails
	if provider == tracker.ProviderLocal {
		return nil, false
	}
	f, err := e.Forge(provider, root, cfg.Tracker)
	if err != nil {
		return nil, false
	}
	prs, err = f.OpenPRs(ctx)
	return prs, err == nil
}

// OpenPRNumbers is the set of open PR numbers, ok false when the forge cannot
// say. A caller that drops PRs absent from it must keep them all when !ok.
func (e Env) OpenPRNumbers(ctx context.Context, root string) (open map[int]bool, ok bool) {
	prs, ok := e.openPRs(ctx, root)
	if !ok {
		return nil, false
	}
	open = map[int]bool{}
	for _, p := range prs {
		open[p.Number] = true
	}
	return open, true
}

// openPRsByHead maps head branch to PR URL for the open PRs, read only when a
// polled slot records no PR. Best effort: with no forge, no origin or a failing
// listing the poll records what the panes say and nothing more.
func (e Env) openPRsByHead(ctx context.Context, root string, reg Registry, polled map[string]PollRow) map[string]string {
	need := false
	for _, s := range reg.Slots() {
		if _, ok := polled[s.Name()]; ok && s.PR() == "" && s.Branch() != "" && !IsOwnPark(s.Branch(), s.Name()) {
			need = true
		}
	}
	if !need {
		return nil
	}
	prs, ok := e.openPRs(ctx, root)
	if !ok {
		return nil
	}
	out := map[string]string{}
	for _, p := range prs {
		out[p.Branch] = p.URL
	}
	return out
}

// prMerged says whether the forge shows the slot's recorded PR merged. A slot
// with no PR, a project with no origin or a forge that cannot answer is not.
func (e Env) prMerged(ctx context.Context, root string, s *Slot) bool {
	merged, _ := e.prMergedKnown(ctx, root, s)
	return merged
}

// prMergedKnown is prMerged plus whether the forge answered. A lookup that ctx
// cut short (the Wait deadline, an interrupt) or that failed is unknown, not
// "open": a caller that must not act on a guess tells the two apart.
func (e Env) prMergedKnown(ctx context.Context, root string, s *Slot) (merged, known bool) {
	info, known := e.prInfoKnown(ctx, root, s)
	return strings.EqualFold(info.State, "MERGED"), known
}

// prInfoKnown is the forge's view of the slot's recorded PR plus whether the
// forge answered. A slot with no PR or no origin answers with an empty view.
func (e Env) prInfoKnown(ctx context.Context, root string, s *Slot) (info tracker.PRInfo, known bool) {
	n, ok := PRRefNumber(s.PR())
	if !ok {
		return info, true
	}
	if _, code := e.git(root, "remote", "get-url", "origin"); code != 0 {
		return info, true
	}
	cfg := e.GateConfig(root)
	provider, _ := e.detectProvider(ctx, root, "", cfg.Tracker.Provider, tracker.ForgeReader) // a reader never fails
	if provider == tracker.ProviderLocal {
		return info, ctx.Err() == nil
	}
	f, err := e.Forge(provider, root, cfg.Tracker)
	if err != nil {
		return info, false
	}
	info, err = f.PRView(ctx, n)
	if err != nil || ctx.Err() != nil {
		return tracker.PRInfo{}, false
	}
	return info, true
}

// seenKey is what `round wait` remembers of a slot it returned: the state and
// its evidence, so a new ROTA-BLOCKED question or limit line is news again.
func seenKey(state, evidence string) string {
	return strings.ToLower(state) + "\t" + evidence
}

// alwaysNews are the states `round wait` returns every time it sees them. The
// orchestrator answers them in the pane at once (a permission prompt) or
// reclaims the slot (dead); an answer typed in the pane re-arms nothing, and
// the generic evidence of a permission prompt cannot tell two prompts apart.
func alwaysNews(state string) bool {
	return state == StateNeedsPermission || state == StateDead
}

// recordRow writes one classified row into its slot, the way every writer of
// a pane's state does (Poll, Wait). A row different from `seen` drops it: the
// slot moved on, so its next arrival is news again.
func recordRow(s *Slot, r PollRow, t pollTarget, now time.Time, queued Registry) (skipped bool, err error) {
	// The row was read against t's event. One that arrived, went or was replaced
	// since (a `worker send` between the snapshot and this write) makes the row
	// stale: recording it would stamp activeAt past the new event and kill it.
	// The caller reads again.
	if cur := s.Event(); (cur == nil) != (t.ev == nil) || (cur != nil && cur.Seq != t.ev.Seq) {
		return true, nil
	}
	// A state change is the registry's only record of activity that is not a
	// commit or an edit: `round reconcile` reads it as the stall clock. A row an
	// event decided is stamped with the event's own time, so the event is not
	// older than the change it caused.
	at := stamp.Format(now)
	if t.evSeq > 0 {
		// An event whose ts does not parse (decide surfaced it) must not
		// reach activeAt: keep the poll time.
		if _, err := stamp.Parse(t.ev.TS); err == nil {
			at = t.ev.TS
		}
	}
	key := rowKey(t, r)
	prev := s.State()
	if err := s.MarkState(r.State, at); err != nil {
		return false, err
	}
	if r.State == StateDone && !strings.EqualFold(prev, "done") {
		s.BaselineReview(now)
	}
	if s.Seen() != key {
		s.ClearSeen()
	}
	if r.State == StateDone && r.Evidence != relayEchoEvidence {
		if t.relayPending > 0 {
			// A reply past the pending relay's echo proves that even a send
			// which timed out was accepted. Do not leave its unsent mark to
			// block the next reply once relayPending is cleared.
			s.SetUnsent(false)
		}
		s.ClearRelayPending()
	}
	if r.State == StateUnverifiable {
		s.SetReason(r.Evidence)
	}
	// A pane that moved after the worker's event answers it: the question was
	// relayed or typed to, the work resumed. Only the event this row was read
	// against goes (the seq check above), never a newer one.
	if r.State == StateBusy && t.ev != nil {
		s.ClearEvent()
	}
	// Only a URL-shaped ROTA-DONE argument becomes slot.pr. The contract
	// allows a bare branch name there, and handing a branch to `gh pr
	// merge` fails where the gate's local merge would have worked.
	// A PR a queued record already holds is the previous issue's: the pane's
	// last ROTA-DONE outlives it, and recording it would pair it with this
	// slot's next issue and branch.
	if r.State == StateDone && IsPRURL(r.Evidence) && queued.QueuedPR(r.Evidence) == nil {
		s.SetPR(r.Evidence)
	}
	// A review item reports the issues it filed instead; `round assign` frees
	// the slot on them and closes the item.
	if r.State == StateDone {
		if refs, ok := ParseIssuesDone(r.Evidence); ok {
			s.SetIssues(refs)
		}
		if id, ok := ParseFindingDone(r.Evidence); ok {
			s.SetFinding(id)
		}
	}
	return false, nil
}

// pollTarget is one slot to classify: its name, host handle, the state the
// registry last recorded for it, the state `round wait` last returned for it
// and the turn baseline its last dispatch recorded.
type pollTarget struct {
	name, handle, prev, seen string
	kind                     string // the slot's harness kind, "" for the default
	// relayPending is the number of the relay sent to the slot whose echo no
	// done line has followed yet (0 for none). relays is the length of the
	// slot's relay log.
	relayPending int
	relaySeen    bool      // its `[relay K]` line has been seen in a capture
	relaySentAt  time.Time // when it went out
	relayEvSeq   int       // the slot's event id when it went out; -1 when unrecorded
	relayMarker  bool      // set by classify: the last capture shows the line
	relays       int
	turnSeq      int
	unsent       bool // a task whose brief the host never acknowledged
	// ev is the slot's standing worker event with the stamps Decide weighs it
	// against. evSeq is set by classify to the event's seq when the event, not
	// the pane, decided the row.
	ev         *Event
	lastChange time.Time
	evSeq      int
}

func slotTarget(s *Slot) pollTarget {
	return pollTarget{name: s.Name(), kind: s.HarnessKind(), handle: s.PaneHandle(), prev: s.State(), seen: s.Seen(), turnSeq: s.TurnSeq(), unsent: s.Unsent(),
		relayPending: s.RelayPending(), relaySeen: s.RelayMarkerSeen(), relaySentAt: s.RelaySentAt(), relayEvSeq: s.RelayEventSeq(), relays: len(s.Relays()),
		ev: s.Event(), lastChange: parseStamp(s.ActiveAt())}
}

// rowKey is the `seen` key of a row. A row an event decided carries the
// event's seq, so the next event is news even when it reads the same.
func rowKey(t pollTarget, r PollRow) string {
	k := seenKey(r.State, r.Evidence)
	// A done after a relay is news even with the evidence the last one had.
	if r.State == StateDone && t.relays > 0 {
		k += "\tr" + strconv.Itoa(t.relays)
	}
	if t.evSeq > 0 {
		k += "\t#" + strconv.Itoa(t.evSeq)
	}
	return k
}

// refreshEvents re-reads each target's event and last-change stamp (and its
// recorded state) from reg, for a loop that classifies the same targets again.
func refreshEvents(reg Registry, targets []pollTarget) {
	for i := range targets {
		if s := reg.Slot(targets[i].name); s != nil {
			targets[i].ev, targets[i].lastChange, targets[i].prev = s.Event(), parseStamp(s.ActiveAt()), s.State()
			targets[i].unsent = s.Unsent()
		}
		targets[i].evSeq = 0
	}
}

// targetOf is the polled target named name, the zero target when none is.
func targetOf(targets []pollTarget, name string) pollTarget {
	for _, t := range targets {
		if t.name == name {
			return t
		}
	}
	return pollTarget{}
}

// classify reads each target's pane twice, settle apart, and classifies it.
// It touches no file: Poll and Wait record the result.
// First capture for every slot, then settle once, then the second capture, so
// N slots cost one settle interval, not N.
//
// A slot whose host numbers its turns (herdr's completion_seq, #511) and
// reports one finished past the slot's dispatch baseline is read once, with
// no movement check: the agent is at rest by the host's own count, so a pane
// that redraws is not a pulse. The text rules still decide its state. When
// every slot is such a slot, nothing settles and slept is false.
//
// settling reports a slot that is BUSY only because its pane moved while the
// host's own status was not `working` (herdr idle or done, or no native
// status). The host sends no event when such a pane comes to rest, so a
// caller woken by events must re-classify it on its own (#211: herdr's
// scrollback reads differ for a moment after a turn ends).
func (e Env) classify(ctx context.Context, h host.Host, targets []pollTarget, settle time.Duration, lines int) (rows []PollRow, settling, slept bool) {
	tr := h.Capabilities().TurnReader
	first := map[string]string{}
	finished := map[string]string{} // slot -> native status of a finished turn
	readErr := map[string]error{}   // slot -> why a pane read failed
	for _, t := range targets {
		// The turn is read before the capture: a capture taken after the
		// host counted the turn finished holds all of its output.
		if tr != nil && t.turnSeq > 0 {
			if tn, ok := tr.Turn(ctx, t.name, t.handle); ok && tn.Finished(t.turnSeq) {
				finished[t.name] = tn.Status
			}
		}
		first[t.name], readErr[t.name] = readPane(ctx, h, t, lines)
	}
	if len(finished) < len(targets) {
		e.Sleep(settle)
		slept = true
	}
	for i, t := range targets {
		if t.unsent && t.relayPending == 0 {
			rows = append(rows, PollRow{t.name, StateBlocked, "dispatch: brief was not submitted; retry the task dispatch"})
			continue
		}
		// push appends the row after the slot's own event had its say.
		push := func(row PollRow) {
			row, used := decide(t.ev, row, t.lastChange)
			if used {
				targets[i].evSeq = t.ev.Seq
			}
			rows = append(rows, row)
		}
		if native, ok := finished[t.name]; ok {
			st, ev := ClassifyFor(t.kind, first[t.name], false, lines, native)
			t.relayMarker = t.relayPending > 0 && relayMarkerVisible(first[t.name], lines, t.relayPending)
			targets[i].relayMarker = t.relayMarker
			st, ev = gateRelayEcho(t, st, ev, first[t.name], lines)
			push(PollRow{t.name, st, ev})
			continue
		}
		second, err := readPane(ctx, h, t, lines)
		native := h.Status(ctx, t.name, t.handle)
		if err == nil {
			err = readErr[t.name]
		}
		native = withReadFailure(native, err)
		moved := first[t.name] != second
		st, ev := ClassifyFor(t.kind, second, moved, lines, native)
		t.relayMarker = t.relayPending > 0 && relayMarkerVisible(second, lines, t.relayPending)
		targets[i].relayMarker = t.relayMarker
		st, ev = gateRelayEcho(t, st, ev, second, lines)
		if st == StateUnknown {
			// herdr says why it could not classify the agent; a failing or
			// absent explain leaves the generic evidence as it was.
			if x := h.Capabilities().Explainer; x != nil {
				if why := x.Explain(ctx, t.name, t.handle); why != "" {
					ev += ": herdr explain: " + strutil.Clip(why, 200)
				}
			}
		}
		if st == StateBusy && moved && native != "working" {
			settling = true
		}
		push(PollRow{t.name, st, ev})
	}
	return rows, settling, slept
}

// readPane captures a slot's pane. err says the read failed, which is not the
// same as a pane with no text. A host that cannot tell reads as readable.
func readPane(ctx context.Context, h host.Host, t pollTarget, lines int) (text string, err error) {
	if r := h.Capabilities().PaneReader; r != nil && t.handle != "" {
		return r.ReadPane(ctx, t.name, t.handle, lines)
	}
	return h.Capture(ctx, t.name, t.handle, lines), nil
}

// withReadFailure folds a failed pane read into the host's native status. A
// window the host says is gone is proof (`gone`). Any other failed read says
// nothing about the agent, so it yields to what the host itself reports
// (`working`, a `blocked` dialog, `gone`, its own unverifiable) and is only
// pane-unreadable when the host has nothing better.
func withReadFailure(native string, err error) string {
	if err == nil {
		return native
	}
	if errors.Is(err, host.ErrPaneGone) {
		return "gone"
	}
	switch {
	case native == "working", native == "blocked", native == "gone", strings.HasPrefix(native, host.UnverifiablePrefix):
		return native
	}
	return host.UnverifiablePrefix + host.ReasonPaneUnreadable
}

// IsPRURL reports whether s is a PR or MR URL, the shape `worker poll` stores
// in slot.pr.
func IsPRURL(s string) bool {
	_, ok := tracker.ParsePRURL(s)
	return ok
}
