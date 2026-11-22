package worker

import (
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/pystr"
	"github.com/l4ci/rota/internal/strutil"
	"github.com/l4ci/rota/internal/usagelimit"
)

// Worker slot classification uses pane text, movement and host status.
//
// States: BUSY | IDLE | BLOCKED | DONE | DEAD | LIMITED | NEEDS-PERMISSION |
// UNKNOWN (herdr only).
//
// Liveness is decided by MOVEMENT, not by pattern-matching a spinner: the pane
// is captured twice, settle seconds apart, and a pane that changed is BUSY. A
// static pane is then classified by content. On herdr, a slot whose agent
// reports a turn finished since its dispatch (completion_seq past the
// recorded turnSeq, #511) is captured once and taken as static.
//
// DONE and BLOCKED come from SENTINELS the worker contract requires it to
// print, not from inference:
//
//	ROTA-DONE <slot> <pr-url-or-branch>
//	ROTA-DONE <slot> issues:#a,#b      (a review item: filed issues, no PR)
//	ROTA-DONE <slot> issues:none       (a completed review with no findings)
//	ROTA-DONE <slot> finding:<id>      (a research item: the finding comment, no PR)
//	ROTA-BLOCKED <slot>: <one question in plain language>
//
// LIMITED means the session hit its usage window; it routes to reassigning
// the slot to another account rather than re-dispatching onto the same one. If
// the pane offers "Add funds", that spends money and is never the
// orchestrator's to answer.
//
// DEAD is the one state with no sentinel, so it stays heuristic: a bare `API
// Error … Overloaded` on a static pane is a HEADSTONE, not a pulse (only
// `Retrying in` means a retry is in flight), and `Resume this session with`
// on a static pane is a crashed session.
//
// herdr adds a native agent state, read before the pane text: working is
// BUSY; blocked is NEEDS-PERMISSION (a dialog is up) unless the pane carries
// a ROTA-BLOCKED sentinel or the harness's internal tool-wait status;
// idle/done fall to the text rules; unknown falls to
// the text rules and ends UNKNOWN (with a short `herdr agent explain` excerpt in its evidence), which is surfaced and never treated as
// finished; gone (no agent) is DEAD. Sentinels, `Retrying in` and LIMITED
// still outrank the native state, the same as they outrank movement.

// Slot states.
const (
	StateBusy            = "BUSY"
	StateIdle            = "IDLE"
	StateBlocked         = "BLOCKED"
	StateDone            = "DONE"
	StateDead            = "DEAD"
	StateLimited         = "LIMITED"
	StateNeedsPermission = "NEEDS-PERMISSION"
	StateUnknown         = "UNKNOWN"
	// StateUnverifiable: the host could not be asked or the pane could not be
	// read, so the slot's liveness is not known. Contact loss is not process
	// death (#820): only positive proof of exit (`gone`) is DEAD. The evidence
	// is the reason (host.Reason*).
	StateUnverifiable = "UNVERIFIABLE"
)

var (
	// A sentinel may follow a reply marker: Claude Code v2.1.288 starts the
	// first line of every reply with "● " (older versions "⏺ "), Codex
	// 0.159.x with "• ".
	//
	// A kind whose TUI guttered its text (PaneVocab.Gutter) reads the guttered
	// variants instead; see lastSentinel.
	reBlocked  = regexp.MustCompile(`(?m)^\s*(?:[●⏺•]\s*)?ROTA-BLOCKED\s+(\S+)\s*:\s*(.+)$`)
	reDone     = regexp.MustCompile(`(?m)^\s*(?:[●⏺•]\s*)?ROTA-DONE\s+(\S+)\s*(.*)$`)
	reBlockedG = regexp.MustCompile(`(?m)^\s*(?:[●⏺•┃]\s*)?ROTA-BLOCKED\s+(\S+)\s*:\s*(.+)$`)
	reDoneG    = regexp.MustCompile(`(?m)^\s*(?:[●⏺•┃]\s*)?ROTA-DONE\s+(\S+)\s*(.*)$`)
	reRetry    = regexp.MustCompile(`Retrying in`)
	reFunds    = regexp.MustCompile(`Add funds`)
	reAPIErr   = regexp.MustCompile(`(API Error[^\n]*)`)
	reResume   = regexp.MustCompile(`Resume this session with`)
	// A worker stopped at a permission prompt is STALLED, but looks exactly
	// like an idle one. Distinct from BLOCKED: the worker is not asking a
	// design question, it needs an approval.
	permissionPatterns = usagelimit.CompileAll(`(?i)`, []string{
		`Do you want to (?:proceed|allow|make this edit)`,
		`and don'?t ask again`,
		`No, and tell Claude what to do differently`,
		`Allow .{0,40}\bto run\b`,
	})
)

// reFindingDone is a research item's done line: the id of the finding comment
// the worker posted, in place of a PR.
var reFindingDone = regexp.MustCompile(`^finding:\s*([A-Za-z0-9_-]+)$`)

// ParseFindingDone reads the evidence of `ROTA-DONE <slot> finding:<id>` into
// the comment id. ok is false for any other evidence.
func ParseFindingDone(evidence string) (id string, ok bool) {
	m := reFindingDone.FindStringSubmatch(strings.TrimSpace(evidence))
	if m == nil {
		return "", false
	}
	return m[1], true
}

// rePlaceholder is evidence that is only the brief's own placeholder (`<pr-url>`).
var rePlaceholder = regexp.MustCompile(`^<[^<>]+>`)

// isPlaceholder: evidence that is only the brief's own `<pr-url>`. A real URL
// written in angle brackets, `<https://...>`, is evidence.
func isPlaceholder(ev string) bool {
	return rePlaceholder.MatchString(ev) && !strings.HasPrefix(ev, "<http")
}

// staleDoneEvidence is the evidence of a static pane whose only ROTA-DONE
// predates the last relay sent to the slot.
const staleDoneEvidence = "static pane, ROTA-DONE predates the last relay"

// lastSentinel returns the state of the last ROTA-BLOCKED/ROTA-DONE line the
// worker itself printed. Text from a `<rota-relay …>` opening line through its
// closing line (see relayMask), or from a legacy `--- ORCHESTRATOR (round N) ---`
// header up to the worker's next reply marker, is a relay: sentinel text quoted
// there is the orchestrator's, so only a marker-prefixed line counts inside a
// legacy one. A relay echoed under OpenCode's "┃" gutter ends where the gutter
// does.
//
// A ROTA-DONE followed by a relay header is superseded: the relay (a bounce, a
// review) reopened the work, so the old line is no longer the worker's state
// (#873). A ROTA-BLOCKED followed by a relay header is superseded the same way:
// the relay answered the question, so only a blocked line the worker prints
// after it counts, the same question re-asked included (#1096). stale reports that such a line was dropped and no later sentinel
// replaced it.
func lastSentinel(lines []string, gutter bool) (state, evidence string, ok, stale bool) {
	blockedRe, doneRe := reBlocked, reDone
	if gutter {
		blockedRe, doneRe = reBlockedG, reDoneG
	}
	inRelay := usagelimit.RelayMask(lines, gutter, false)
	for i, l := range lines {
		if head, _ := usagelimit.IsRelayHead(l, gutter); head {
			if ok && state == StateDone {
				state, evidence, ok, stale = "", "", false, true
			} else if ok && state == StateBlocked {
				state, evidence, ok = "", "", false
			}
			continue
		}
		if inRelay[i] {
			continue
		}
		// Match against the text from this line on, as the classifier always
		// has: a bare `ROTA-DONE <slot>` takes the next line as its evidence.
		rest := strings.Join(lines[i:], "\n")
		if m := blockedRe.FindStringSubmatchIndex(rest); m != nil && m[0] == 0 {
			if ev := strings.TrimSpace(rest[m[4]:m[5]]); !isPlaceholder(ev) {
				state, evidence, ok, stale = StateBlocked, ev, true, false
			}
		} else if m := doneRe.FindStringSubmatchIndex(rest); m != nil && m[0] == 0 {
			ev := strings.TrimSpace(rest[m[4]:m[5]])
			if !isPlaceholder(ev) {
				ev = joinWrapped(lines, inRelay, i+strings.Count(rest[:m[5]], "\n"), ev)
				state, evidence, ok, stale = StateDone, ev, true, false
			}
		}
	}
	return
}

// reWrapReply starts a line of the worker's own output.
var reWrapReply = regexp.MustCompile(`^\s*[●⏺•]`)

const noSentinelBase = "static pane, no sentinel"

// noSentinelEvidence is the evidence of an idle static pane. When the text
// holds a ROTA-DONE substring that was still not accepted, it says why, so
// the orchestrator is not told "no sentinel" about a pane that shows one
// (#4093): masked inside a relay echo, outside the read window, or a line the
// sentinel pattern rejects (an unmatched prefix, or the brief's placeholder).
func noSentinelEvidence(full, window []string, gutter bool, lines int) string {
	has := func(ls []string) bool {
		for _, l := range ls {
			if strings.Contains(l, "ROTA-DONE") {
				return true
			}
		}
		return false
	}
	if !has(full) {
		return noSentinelBase
	}
	if !has(window) {
		return noSentinelBase + ": ROTA-DONE is outside the last " + strconv.Itoa(lines) + " lines read"
	}
	inRelay := usagelimit.RelayMask(window, gutter, false)
	for i, l := range window {
		if strings.Contains(l, "ROTA-DONE") && !inRelay[i] {
			return noSentinelBase + ": ROTA-DONE seen but not accepted (unmatched prefix or placeholder evidence)"
		}
	}
	return noSentinelBase + ": ROTA-DONE seen only inside a relay echo (masked)"
}

// maxWrapJoins bounds how many continuation lines joinWrapped appends.
const maxWrapJoins = 3

// joinWrapped repairs a DONE line the terminal soft-wrapped (Core hands over
// the screen as rendered, with no unwrapped read). A line is taken as wrapped
// when it fills the pane width, seen as another line at least as long, and
// the next line is one unindented token outside any relay. Only a one-token
// evidence is extended, so prose after the sentinel is never glued on.
func joinWrapped(lines []string, inRelay []bool, at int, ev string) string {
	if ev == "" || strings.ContainsAny(ev, " \t") || at >= len(lines) {
		return ev
	}
	full := utf8.RuneCountInString(lines[at])
	wide := false
	for j, l := range lines {
		if j != at && utf8.RuneCountInString(l) >= full-1 {
			wide = true
			break
		}
	}
	for n := 0; wide && n < maxWrapJoins && at+1 < len(lines); n++ {
		next := lines[at+1]
		if next == "" || strings.ContainsAny(next, " \t") || inRelay[at+1] || reWrapReply.MatchString(next) {
			break
		}
		ev += next
		at++
		if utf8.RuneCountInString(next) < full-1 {
			break
		}
	}
	return ev
}

func firstMatch(res []*regexp.Regexp, s string) string {
	for _, re := range res {
		if m := re.FindString(s); m != "" {
			return m
		}
	}
	return ""
}

// Classify decides one slot's state from its pane text using the default
// harness's vocabulary. native is herdr's agent_status
// (or `gone`), empty under tmux, which has none. moved says the pane changed
// between the two captures.
func Classify(text string, moved bool, lines int, native string) (state, evidence string) {
	return ClassifyFor("", text, moved, lines, native)
}

// ClassifyFor is Classify for a slot of the given kind: its harness's own pane
// wording (PaneVocab) is read on top of the shared rules, on that kind's panes
// only.
func ClassifyFor(kind, text string, moved bool, lines int, native string) (state, evidence string) {
	p, _ := harness.LookupPane(kind)
	return ClassifyPane(p, text, moved, lines, native)
}

// ClassifyPane is ClassifyFor for the Pane that wrote the text; nil reads with
// no harness vocabulary.
func ClassifyPane(p harness.Pane, text string, moved bool, lines int, native string) (state, evidence string) {
	rules := harness.RulesOf(p)
	all := pystr.Splitlines(text)
	full := all
	if lines > 0 && len(all) > lines { // [-0:] is the whole list in Python
		all = all[len(all)-lines:]
	}
	tail := strings.Join(all, "\n")
	// What the session says about its own limit is read outside echoed
	// orchestrator blocks (#883), and only the harness's own dialog counts,
	// not text the worker quotes (#1212): usagelimit.Detect decides. tail is
	// already scoped to lines, so no further scope is passed.
	hit, limited := usagelimit.DetectPane(p, tail, usagelimit.Scope{})
	own, dialog := hit.Own, hit.Dialog

	// Sentinels win over everything, including movement: a worker that printed
	// ROTA-DONE and is still rendering its own output is finished, not busy.
	// The LAST sentinel decides: a ROTA-DONE printed after an answered
	// ROTA-BLOCKED means the worker moved on.
	st, ev, ok, staleDone := lastSentinel(all, rules.Gutter)
	if ok {
		return st, ev
	}
	// `Retrying in` is the only positive proof a retry is in flight. Check it
	// before the DEAD patterns so a live retry is never read as a corpse.
	if reRetry.MatchString(tail) || usagelimit.AnyMatch(rules.Retry, tail) {
		return StateBusy, "retry in flight"
	}
	// LIMITED is checked BEFORE movement: a session that hit its usage limit may
	// still animate a prompt, and reading that as BUSY strands the orchestrator
	// waiting for work that cannot resume until the window resets.
	if limited {
		// The "Add funds" prompt spends real money and is NEVER the
		// orchestrator's to answer: flag it so the caller escalates.
		if reFunds.MatchString(tail) { // fail closed: a real limit plus any "Add funds" escalates
			return StateLimited, "usage limit reached; pane offers 'Add funds' — needs a human, do NOT answer"
		}
		if rules.AuthDead != nil && rules.AuthDead.MatchString(own) {
			return StateLimited, "API key expired — needs a human to renew it; no reset clock"
		}
		return StateLimited, strutil.Clip(strings.TrimSpace(dialog[hit.Start:hit.End]), 120)
	}
	nativeLive := JudgeLiveness(LivenessEvidence{Native: native})
	if nativeLive.State == LivenessUnverifiable {
		return StateUnverifiable, nativeLive.Reason
	}
	if rules.LoggedOut != nil {
		if m := rules.LoggedOut.FindString(dialog); m != "" {
			return StateBlocked, "slot is logged out (\"" + strings.TrimSpace(m) + "\"): " + rules.LoggedOutAdvice
		}
	}
	// A dialog for a tier model the provider does not list: the TUI falls back
	// to its last-used model once it closes, so nothing else shows the worker
	// is on the wrong one.
	if rules.BadModel != nil {
		if m := rules.BadModel.FindStringSubmatch(tail); m != nil {
			return StateBlocked, "model " + m[1] + " was refused — fix round.tiers (provider/model ids)"
		}
	}
	if usagelimit.AnyMatch(rules.Busy, own) {
		return StateBusy, "pane shows: " + firstMatch(rules.Busy, own)
	}
	if native == "working" {
		return StateBusy, "herdr: agent working"
	}
	if moved {
		return StateBusy, "pane changed between captures"
	}
	waiting := usagelimit.AnyMatch(rules.Waiting, own)
	if native == "blocked" && !waiting {
		return StateNeedsPermission, "herdr: agent blocked on a dialog — answer it in the slot's tab, or widen round.commands.claude"
	}
	if nativeLive.State == LivenessDead {
		return StateDead, "herdr: no agent in the slot's tab — the session exited"
	}
	// Static pane from here down.
	if m := reAPIErr.FindStringSubmatch(tail); m != nil {
		return StateDead, strutil.Clip(strings.TrimSpace(m[1]), 120)
	}
	if reResume.MatchString(tail) {
		return StateDead, "crashed session offering resume"
	}
	// Only the harness's own dialog counts, not permission wording the worker
	// quotes inside a reply (#3161).
	for _, re := range permissionPatterns {
		if re.MatchString(dialog) {
			return StateNeedsPermission, "stopped at a permission prompt — approve in the pane, or widen round.commands.claude"
		}
	}
	if waiting {
		return StateBusy, "pane shows an internal tool wait"
	}
	if native == "unknown" {
		return StateUnknown, "herdr cannot classify the agent — inspect the tab; not proof it finished"
	}
	// The footer outlives a dialog or a crash, so it only turns the final idle
	// outcome into busy. The count moves as tasks start and end; a fixed
	// evidence keeps `round wait` from reporting the slot as changed.
	if usagelimit.AnyMatch(rules.Background, own) {
		return StateBusy, "pane shows background shells or monitors running"
	}
	if staleDone {
		return StateIdle, staleDoneEvidence
	}
	return StateIdle, noSentinelEvidence(full, all, rules.Gutter, lines)
}

// relayMarkerVisible: the pane capture holds pending relay k's opening line (or the legacy `[relay k]` line).
func relayMarkerVisible(text string, lines, k int) bool {
	all := pystr.Splitlines(text)
	if lines > 0 && len(all) > lines {
		all = all[len(all)-lines:]
	}
	for _, l := range all {
		if harness.RelaySequence(l) == strconv.Itoa(k) {
			return true
		}
	}
	return false
}

// doneAfterRelay reports a done line printed after relay k's echo. The relay is
// identified by its marker line, not by the shape of the pane, so a second
// relay not yet rendered is never mistaken for the first.
func doneAfterRelay(kind, text string, lines, k int) bool {
	all := pystr.Splitlines(text)
	if lines > 0 && len(all) > lines {
		all = all[len(all)-lines:]
	}
	gutter := usagelimit.RulesFor(kind).Gutter
	mark := -1
	for i, l := range all {
		if harness.RelaySequence(l) == strconv.Itoa(k) {
			mark = i
		}
	}
	if mark < 0 {
		return false
	}
	start := mark
	for start > 0 {
		if head, _ := usagelimit.IsRelayHead(all[start], gutter); head {
			break
		}
		start--
	}
	st, _, ok, _ := lastSentinel(all[start:], gutter)
	return ok && st == StateDone
}

// ReadsWaitPane reports whether a kind's pane can show an internal tool wait
// that its host snapshot reads as blocked, so a caller must read the pane
// (ClassifyFor) before trusting that snapshot. A kind with no wait wording
// (or no harness) never does.
func ReadsWaitPane(kind string) bool {
	return len(usagelimit.RulesFor(kind).Waiting) > 0
}
