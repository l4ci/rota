package worker

import (
	"context"
	"errors"
	"fmt"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/stamp"
	"sync"

	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/ledger"
	"github.com/l4ci/rota/internal/strutil"
	"github.com/l4ci/rota/internal/tracker"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Worker slot classification: the port of bin/hv-worker-poll.
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
// a ROTA-BLOCKED sentinel; idle/done fall to the text rules; unknown falls to
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

// limitPhrases are the usage-limit messages a session prints about itself, as
// case-insensitive regex sources (no flag prefix). FindLimit is the one reader
// of them: `worker poll` and the usage-limit watcher (D3) both ask it.
var limitPhrases = harness.AllLimitPhrases()

// LimitRegex is the alternation of the limit phrases with the case-insensitive
// flag written in the regex itself, for engines that take one pattern string
// (herdr's pane.output_matched). It is a prefilter: it also matches an
// "Approaching" warning, which FindLimit then rejects, and every kind's own
// phrases, which FindLimit reads only for a slot of that kind.
func LimitRegex() string {
	return "(?i)(?:" + strings.Join(append(append([]string(nil), limitPhrases...), harness.AllPaneLimits()...), "|") + ")"
}

// FindLimit is the one answer to "does this pane text show a hit limit, and
// where": the byte span of the first match of the shared phrases and the
// kind's own (PaneVocab.Limits; "" has none). An "Approaching" warning matches
// the phrases but is not a limit, so it is skipped here and nowhere else.
func FindLimit(kind, text string) (start, end int, ok bool) {
	for _, re := range append(append([]*regexp.Regexp(nil), limitPatterns...), paneRulesFor(kind).limits...) {
		for _, loc := range re.FindAllStringIndex(text, -1) {
			if strings.Contains(strings.ToLower(text[loc[0]:loc[1]]), "approaching") {
				continue
			}
			return loc[0], loc[1], true
		}
	}
	return 0, 0, false
}

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
	// Heuristic, like DEAD: a limited session cannot print a sentinel. The
	// patterns are anchored to phrasing a session emits about ITSELF, not bare
	// words like "limit" that a worker reading source code would echo.
	limitPatterns = compileAll(`(?i)`, limitPhrases)
	// A worker stopped at a permission prompt is STALLED, but looks exactly
	// like an idle one. Distinct from BLOCKED: the worker is not asking a
	// design question, it needs an approval.
	permissionPatterns = compileAll(`(?i)`, []string{
		`Do you want to (?:proceed|allow|make this edit)`,
		`and don'?t ask again`,
		`No, and tell Claude what to do differently`,
		`Allow .{0,40}\bto run\b`,
	})
	// An architecture-review item produces issues, not code: its done line
	// names them in place of a PR, `issues:#139,#140`.
	// An entry is a number (`#139`) or a key-based tracker's key (`PROJ-123`).
	reIssuesDone = regexp.MustCompile(`^issues:\s*(?:#?\d+|[A-Z][A-Z0-9_]*-\d+)(?:\s*,\s*(?:#?\d+|[A-Z][A-Z0-9_]*-\d+))*$`)
	reIssueRef   = regexp.MustCompile(`#?\d+|[A-Z][A-Z0-9_]*-\d+`)
)

// ParseIssuesDone reads the evidence of `ROTA-DONE <slot> issues:#a,#b` into
// the issue refs (`#a`, `PROJ-123`). ok is false for any other evidence.
func ParseIssuesDone(evidence string) (refs []string, ok bool) {
	ev := strings.TrimSpace(evidence)
	if !reIssuesDone.MatchString(ev) {
		return nil, false
	}
	for _, r := range reIssueRef.FindAllString(strings.TrimPrefix(ev, "issues:"), -1) {
		refs = append(refs, "#"+strings.TrimPrefix(r, "#"))
		if strings.Contains(r, "-") {
			refs[len(refs)-1] = r
		}
	}
	return refs, true
}

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

func compileAll(prefix string, pats []string) []*regexp.Regexp {
	out := make([]*regexp.Regexp, len(pats))
	for i, p := range pats {
		out[i] = regexp.MustCompile(prefix + p)
	}
	return out
}

// reRelayHead opens an orchestrator relay or brief echoed into the pane, bare
// or after Claude Code's "❯" prompt glyph.
var reRelayHead = regexp.MustCompile(`^\s*(?:❯\s*)?--- ORCHESTRATOR \(round \d+\) ---\s*$`)

// reRelayHeadG is reRelayHead under the gutter.
var reRelayHeadG = regexp.MustCompile(`^\s*(?:[┃❯]\s*)?--- ORCHESTRATOR \(round \d+\) ---\s*$`)

// reGutter starts a line of OpenCode's user-message gutter, and rePlaceholder
// is evidence that is only the brief's own placeholder (`<pr-url>`).
var (
	reGutter      = regexp.MustCompile(`^\s*┃`)
	rePlaceholder = regexp.MustCompile(`^<[^<>]+>`)
)

// isPlaceholder: evidence that is only the brief's own `<pr-url>`. A real URL
// written in angle brackets, `<https://...>`, is evidence.
func isPlaceholder(ev string) bool {
	return rePlaceholder.MatchString(ev) && !strings.HasPrefix(ev, "<http")
}

// reEchoIndent is the indent Claude Code gives every line of an echoed prompt
// after the first; reEchoHead is that first line, behind the "❯" glyph.
var (
	reEchoIndent = regexp.MustCompile(`^  `)
	reEchoHead   = regexp.MustCompile(`^\s*❯`)
)

// reReplyMarker starts a line of the worker's own output (see reBlocked).
var reReplyMarker = regexp.MustCompile(`^\s*[●⏺•]`)

// reSigLine is the signature trailer `rota worker dispatch` ends a brief or
// relay with (codex and opencode slots; a claude payload carries none).
var reSigLine = regexp.MustCompile(`^\s*(?:┃\s*)?--- ROTA-SIG [0-9a-f]+ ---\s*$`)

// staleDoneEvidence is the evidence of a static pane whose only ROTA-DONE
// predates the last relay sent to the slot.
const staleDoneEvidence = "static pane, ROTA-DONE predates the last relay"

// lastSentinel returns the state of the last ROTA-BLOCKED/ROTA-DONE line the
// worker itself printed. Text from a `--- ORCHESTRATOR (round N) ---` header up
// to the worker's next reply marker is a relay: sentinel text quoted there is
// the orchestrator's, so only a marker-prefixed line counts inside it. A relay
// echoed under OpenCode's "┃" gutter ends where the gutter does.
//
// A ROTA-DONE followed by a relay header is superseded: the relay (a bounce, a
// review) reopened the work, so the old line is no longer the worker's state
// (#873). A ROTA-BLOCKED followed by a relay header is superseded the same way:
// the relay answered the question, so only a blocked line the worker prints
// after it counts, the same question re-asked included (#1096). stale reports that such a line was dropped and no later sentinel
// replaced it.
func lastSentinel(lines []string, gutter bool) (state, evidence string, ok, stale bool) {
	relayHead, blockedRe, doneRe := reRelayHead, reBlocked, reDone
	if gutter {
		relayHead, blockedRe, doneRe = reRelayHeadG, reBlockedG, reDoneG
	}
	inRelay := relayMask(lines, gutter, false)
	for i, l := range lines {
		if relayHead.MatchString(l) {
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
			if ev := strings.TrimSpace(rest[m[4]:m[5]]); !isPlaceholder(ev) {
				state, evidence, ok, stale = StateDone, ev, true, false
			}
		}
	}
	return
}

// relayMask marks the lines of echoed orchestrator text: each
// `--- ORCHESTRATOR (round N) ---` header and what follows up to the worker's
// next reply marker. A relay echoed under OpenCode's "┃" gutter ends where the
// gutter does, since OpenCode marks the worker's reply with nothing. A brief
// Claude Code echoes behind "❯" ends at the first non-blank line without the
// two-space echo indent: a harness banner at column 0. With
// sigEnds, a `--- ROTA-SIG <hex> ---` line (the trailer of a signed brief) also
// closes the span, inclusive: what the harness prints after it, a limit banner
// carrying no reply marker, is the session's own.
func relayMask(lines []string, gutter, sigEnds bool) []bool {
	relayHead := reRelayHead
	if gutter {
		relayHead = reRelayHeadG
	}
	mask := make([]bool, len(lines))
	inRelay, gutterRelay, echoRelay := false, false, false
	for i, l := range lines {
		if echoRelay && strings.TrimSpace(l) != "" && !reEchoIndent.MatchString(l) && !relayHead.MatchString(l) {
			inRelay, echoRelay = false, false
		}
		if gutter && gutterRelay && strings.TrimSpace(l) != "" && !reGutter.MatchString(l) {
			inRelay, gutterRelay = false, false
		}
		switch {
		case relayHead.MatchString(l):
			inRelay, gutterRelay, echoRelay = true, gutter && reGutter.MatchString(l), reEchoHead.MatchString(l)
		case reReplyMarker.MatchString(l):
			inRelay, gutterRelay, echoRelay = false, false, false
		}
		mask[i] = inRelay
		if sigEnds && inRelay && reSigLine.MatchString(l) {
			inRelay, gutterRelay, echoRelay = false, false, false
		}
	}
	return mask
}

// reChrome is a line of the harness's own frame below its dialog: the input
// prompt, a box border, the permission-mode footer or the shortcut hint.
var reChrome = regexp.MustCompile(`^\s*(?:[❯>]|[╭╰│─┌└├]|⏵|\? for shortcuts)`)

// reFrameEdge is the input box's bottom edge, in column 0 where reply text
// never starts; reFooter is the permission-mode footer or shortcut hint. Below
// the lowest of them sits the status area: a custom statusline, the footer.
var (
	reFrameEdge = regexp.MustCompile(`^[╰─]`)
	reFooter    = regexp.MustCompile(`^\s*(?:⏵|\? for shortcuts)`)
)

// statusDepth is how many lines up from the bottom the frame edge is looked
// for: a statusline is a few lines, and a deeper match would be the reply's own.
const statusDepth = 8

// DialogRegion is the part of a pane the harness itself wrote: the status area
// under its input box and the blocks directly above the box, below the last
// reply. The worker's prose, diffs, code and tool output sit inside a reply, so
// a limit phrase quoted there is not a hit limit (#1212). The walk goes up
// blank-separated blocks to the last block that opens with a reply marker. The
// run of blocks straight below it that open indented, other than with "⎿", is
// that reply's later paragraphs, lists and code; the blocks under that run are
// dialog. From the marker block, when no such run follows it, its unindented
// lines count (a banner the harness prints straight after a reply) and, while
// no spinner or working footer shows, its "⎿" lines (the way Claude Code
// renders a dialog, and why idle tool output quoting the phrase reads as
// limited). busy are the kind's working-footer patterns; a spinner is frame,
// not dialog. A pane without a reply marker is dialog throughout.
func DialogRegion(text string, busy []*regexp.Regexp) string {
	lines := splitLines(text)
	isWork := func(l string) bool { return anyMatch(busy, l) || reSpinner.MatchString(l) }
	end := len(lines)
	status := end
	for _, re := range []*regexp.Regexp{reFrameEdge, reFooter} {
		for k := end - 1; k >= 0 && k >= end-statusDepth && status == end; k-- {
			if re.MatchString(lines[k]) {
				status = k
			}
		}
	}
	working := slices.ContainsFunc(lines[status:], isWork)
	end = status
	for end > 0 {
		l := lines[end-1]
		w := isWork(l)
		if !(strings.TrimSpace(l) == "" || reChrome.MatchString(l) || w) {
			break
		}
		working = working || w
		end--
	}
	// Walk up block by block (blank-line separated) to the last reply marker.
	// cont is the top of the indented run above start, -1 for none.
	start, from, cont := end, end, -1
walk:
	for i := end; i > 0; {
		j := i
		for j > 0 && strings.TrimSpace(lines[j-1]) != "" {
			j--
		}
		if j < i {
			head := lines[j]
			switch {
			case reReplyMarker.MatchString(head):
				from = j + 1
				if cont >= 0 {
					from = start
				}
				break walk
			case reIndent.MatchString(head) && !reGutterLine.MatchString(head):
				cont = j
			default:
				start, cont = j, -1
			}
		}
		if j == 0 {
			if cont >= 0 { // no reply above: the indented run is dialog too
				start = cont
			}
			break
		}
		i = j - 1
	}
	var kept []string
	if from < start {
		for _, l := range lines[from:start] {
			if strings.TrimSpace(l) != "" && (!reIndent.MatchString(l) || !working && reGutterLine.MatchString(l)) {
				kept = append(kept, l)
			}
		}
	}
	kept = append(kept, lines[start:end]...)
	return strings.Join(append(kept, lines[status:]...), "\n")
}

// reIndent starts an indented line; reGutterLine is tool output under "⎿".
var (
	reIndent     = regexp.MustCompile(`^\s`)
	reGutterLine = regexp.MustCompile(`^\s*⎿`)
)

// reSpinner is a working line: an activity glyph, a verb and an ellipsis
// ("✻ Running tests…").
var reSpinner = regexp.MustCompile(`^\s*[✻✶✽✢✳·*]\s+\S.*…`)

// StripRelays is text without the echoed orchestrator blocks, the way
// lastSentinel skips them: a limit phrase a brief or relay quotes is the
// orchestrator's, not the session's. kind picks the gutter reading.
func StripRelays(kind, text string) string {
	lines := splitLines(text)
	mask := relayMask(lines, paneRulesFor(kind).gutter, true)
	kept := lines[:0:0]
	for i, l := range lines {
		if !mask[i] {
			kept = append(kept, l)
		}
	}
	return strings.Join(kept, "\n")
}

// paneRules are a kind's PaneVocab compiled.
type paneRules struct {
	retry, busy, background, limits []*regexp.Regexp
	authDead, badModel              *regexp.Regexp
	loggedOut                       *regexp.Regexp
	gutter                          bool
}

var (
	paneRulesMu    sync.Mutex
	paneRulesCache = map[string]*paneRules{}
)

// paneRulesFor is the compiled vocabulary of a kind; "" and a kind that is not
// a worker harness have none, so they read as they always did.
func paneRulesFor(kind string) *paneRules {
	paneRulesMu.Lock()
	defer paneRulesMu.Unlock()
	if r, ok := paneRulesCache[kind]; ok {
		return r
	}
	r := &paneRules{}
	if h, ok := harness.Lookup(kind); ok && kind != "" {
		v := h.PaneVocab()
		r.retry, r.busy = compileAll(`(?i)`, v.Retry), compileAll(`(?i)`, v.Busy)
		r.background = compileAll(`(?i)`, v.Background)
		r.limits = compileAll(`(?i)`, v.Limits)
		if v.AuthDead != "" {
			r.authDead = regexp.MustCompile(`(?i)` + v.AuthDead)
		}
		if v.LoggedOut != "" {
			r.loggedOut = regexp.MustCompile(`(?i)` + v.LoggedOut)
		}
		if v.BadModel != "" {
			r.badModel = regexp.MustCompile(v.BadModel)
		}
		r.gutter = v.Gutter
	}
	paneRulesCache[kind] = r
	return r
}

func firstMatch(res []*regexp.Regexp, s string) string {
	for _, re := range res {
		if m := re.FindString(s); m != "" {
			return m
		}
	}
	return ""
}

func anyMatch(res []*regexp.Regexp, s string) bool {
	for _, re := range res {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}

// Classify decides one slot's state from its pane text, reading it as a pane of
// no particular kind (Claude Code's wording). native is herdr's agent_status
// (or `gone`), empty under tmux, which has none. moved says the pane changed
// between the two captures.
func Classify(text string, moved bool, lines int, native string) (state, evidence string) {
	return ClassifyFor("", text, moved, lines, native)
}

// DialogFor is DialogRegion with the busy patterns of the given kind's harness,
// for callers outside poll (the usage-limit watcher) that read a pane's text
// as the harness wrote it.
func DialogFor(kind, text string) string {
	return DialogRegion(text, paneRulesFor(kind).busy)
}

// ClassifyFor is Classify for a slot of the given kind: its harness's own pane
// wording (PaneVocab) is read on top of the shared rules, on that kind's panes
// only.
func ClassifyFor(kind, text string, moved bool, lines int, native string) (state, evidence string) {
	rules := paneRulesFor(kind)
	all := splitLines(text)
	if lines > 0 && len(all) > lines { // [-0:] is the whole list in Python
		all = all[len(all)-lines:]
	}
	tail := strings.Join(all, "\n")
	// What the session says about its own limit is read outside echoed
	// orchestrator blocks (#883).
	own := StripRelays(kind, tail)

	// Sentinels win over everything, including movement: a worker that printed
	// ROTA-DONE and is still rendering its own output is finished, not busy.
	// The LAST sentinel decides: a ROTA-DONE printed after an answered
	// ROTA-BLOCKED means the worker moved on.
	st, ev, ok, staleDone := lastSentinel(all, rules.gutter)
	if ok {
		return st, ev
	}
	// `Retrying in` is the only positive proof a retry is in flight. Check it
	// before the DEAD patterns so a live retry is never read as a corpse.
	if reRetry.MatchString(tail) || anyMatch(rules.retry, tail) {
		return StateBusy, "retry in flight"
	}
	// LIMITED is checked BEFORE movement: a session that hit its usage limit may
	// still animate a prompt, and reading that as BUSY strands the orchestrator
	// waiting for work that cannot resume until the window resets.
	// Only the harness's own dialog counts, not text the worker quotes (#1212).
	dialog := DialogFor(kind, own)
	if s, e, ok := FindLimit(kind, dialog); ok {
		// The "Add funds" prompt spends real money and is NEVER the
		// orchestrator's to answer: flag it so the caller escalates.
		if reFunds.MatchString(tail) { // fail closed: a real limit plus any "Add funds" escalates
			return StateLimited, "usage limit reached; pane offers 'Add funds' — needs a human, do NOT answer"
		}
		if rules.authDead != nil && rules.authDead.MatchString(own) {
			return StateLimited, "API key expired — needs a human to renew it; no reset clock"
		}
		return StateLimited, strutil.Clip(strings.TrimSpace(dialog[s:e]), 120)
	}
	if reason, ok := strings.CutPrefix(native, host.UnverifiablePrefix); ok {
		return StateUnverifiable, reason
	}
	if rules.loggedOut != nil {
		if m := rules.loggedOut.FindString(dialog); m != "" {
			return StateBlocked, "slot is logged out (\"" + strings.TrimSpace(m) + "\"): its account has no login — set round.accounts with this configDir in .rota/config.local.json, or run rota under the logged-in CLAUDE_CONFIG_DIR"
		}
	}
	// A dialog for a tier model the provider does not list: the TUI falls back
	// to its last-used model once it closes, so nothing else shows the worker
	// is on the wrong one.
	if rules.badModel != nil {
		if m := rules.badModel.FindStringSubmatch(tail); m != nil {
			return StateBlocked, "model " + m[1] + " was refused — fix round.tiers (provider/model ids)"
		}
	}
	if anyMatch(rules.busy, own) {
		return StateBusy, "pane shows: " + firstMatch(rules.busy, own)
	}
	if native == "working" {
		return StateBusy, "herdr: agent working"
	}
	if moved {
		return StateBusy, "pane changed between captures"
	}
	if native == "blocked" {
		return StateNeedsPermission, "herdr: agent blocked on a dialog — answer it in the slot's tab, or widen round.workerCommand"
	}
	if native == "gone" {
		return StateDead, "herdr: no agent in the slot's tab — the session exited"
	}
	// Static pane from here down.
	if m := reAPIErr.FindStringSubmatch(tail); m != nil {
		return StateDead, strutil.Clip(strings.TrimSpace(m[1]), 120)
	}
	if reResume.MatchString(tail) {
		return StateDead, "crashed session offering resume"
	}
	for _, re := range permissionPatterns {
		if re.MatchString(tail) {
			return StateNeedsPermission, "stopped at a permission prompt — approve in the pane, or widen round.workerCommand"
		}
	}
	if native == "unknown" {
		return StateUnknown, "herdr cannot classify the agent — inspect the tab; not proof it finished"
	}
	// The footer outlives a dialog or a crash, so it only turns the final idle
	// outcome into busy. The count moves as tasks start and end; a fixed
	// evidence keeps `round wait` from reporting the slot as changed.
	if anyMatch(rules.background, own) {
		return StateBusy, "pane shows background shells or monitors running"
	}
	if staleDone {
		return StateIdle, staleDoneEvidence
	}
	return StateIdle, "static pane, no sentinel"
}

// relayEchoEvidence is the evidence of a done line read while the pending
// relay's echo is not in the pane.
const relayEchoEvidence = "relay sent, its echo is not in the pane yet; the ROTA-DONE shown predates it"

// reRelaySeq is the `[relay K]` line every relay carries under its header.
var reRelaySeq = regexp.MustCompile(`^\s*(?:[┃❯]\s*)?\[relay (\d+)\]\s*$`)

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

// relayMarkerVisible: the pane capture holds pending relay k's `[relay k]` line.
func relayMarkerVisible(text string, lines, k int) bool {
	all := splitLines(text)
	if lines > 0 && len(all) > lines {
		all = all[len(all)-lines:]
	}
	for _, l := range all {
		if m := reRelaySeq.FindStringSubmatch(l); m != nil && m[1] == strconv.Itoa(k) {
			return true
		}
	}
	return false
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

// doneAfterRelay reports a done line printed after relay k's echo. The relay is
// identified by its `[relay k]` line, not by the shape of the pane, so a second
// relay not yet rendered is never mistaken for the first.
func doneAfterRelay(kind, text string, lines, k int) bool {
	all := splitLines(text)
	if lines > 0 && len(all) > lines {
		all = all[len(all)-lines:]
	}
	gutter := paneRulesFor(kind).gutter
	head := reRelayHead
	if gutter {
		head = reRelayHeadG
	}
	mark := -1
	for i, l := range all {
		if m := reRelaySeq.FindStringSubmatch(l); m != nil && m[1] == strconv.Itoa(k) {
			mark = i
		}
	}
	if mark < 0 {
		return false
	}
	start := mark
	for start > 0 && !head.MatchString(all[start]) {
		start--
	}
	st, _, ok, _ := lastSentinel(all[start:], gutter)
	return ok && st == StateDone
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
	before, _ := os.ReadFile(RegistryPath(root))

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
	after, _ := os.ReadFile(RegistryPath(root))
	return PollResult{Slots: rows, Notes: notes, Changed: string(before) != string(after)}, nil
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
	if _, code := e.git(root, "remote", "get-url", "origin"); code != 0 {
		return nil
	}
	cfg := e.GateConfig(root)
	provider := e.detectProvider(ctx, root, "", cfg.Tracker.Provider)
	if provider == tracker.ProviderLocal {
		return nil
	}
	f, err := e.Forge(provider, root, cfg.Tracker)
	if err != nil {
		return nil
	}
	prs, err := f.OpenPRs(ctx)
	if err != nil {
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
	n, ok := PRRefNumber(s.PR())
	if !ok {
		return false
	}
	if _, code := e.git(root, "remote", "get-url", "origin"); code != 0 {
		return false
	}
	cfg := e.GateConfig(root)
	provider := e.detectProvider(ctx, root, "", cfg.Tracker.Provider)
	if provider == tracker.ProviderLocal {
		return false
	}
	f, err := e.Forge(provider, root, cfg.Tracker)
	if err != nil {
		return false
	}
	info, err := f.PRView(ctx, n)
	return err == nil && strings.EqualFold(info.State, "MERGED")
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
	// ev is the slot's standing worker event with the stamps Decide weighs it
	// against. evSeq is set by classify to the event's seq when the event, not
	// the pane, decided the row.
	ev         *Event
	lastChange time.Time
	evSeq      int
}

func slotTarget(s *Slot) pollTarget {
	return pollTarget{name: s.Name(), kind: s.HarnessKind(), handle: s.PaneHandle(), prev: s.State(), seen: s.Seen(), turnSeq: s.TurnSeq(),
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
