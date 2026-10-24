package worker

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/strutil"
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
		return StateNeedsPermission, "herdr: agent blocked on a dialog — answer it in the slot's tab, or widen round.commands.claude"
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
			return StateNeedsPermission, "stopped at a permission prompt — approve in the pane, or widen round.commands.claude"
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

// reRelaySeq is the `[relay K]` line every relay carries under its header.
var reRelaySeq = regexp.MustCompile(`^\s*(?:[┃❯]\s*)?\[relay (\d+)\]\s*$`)

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
