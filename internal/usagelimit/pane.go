// Package usagelimit is the leaf layer both worker pane classification and the
// usage-limit watcher read: the pane text rules that decide whether a session
// hit its usage window, and the account selection over the meters that follow.
// It imports neither worker nor limits.
package usagelimit

import (
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/pystr"
)

// limitPhrases are the usage-limit messages a session prints about itself, as
// case-insensitive regex sources (no flag prefix). FindLimit is the one reader
// of them: `worker poll` and the usage-limit watcher (D3) both ask it.
var limitPhrases = harness.SharedLimitPhrases()

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
	for _, re := range append(append([]*regexp.Regexp(nil), limitPatterns...), RulesFor(kind).Limits...) {
		for _, loc := range re.FindAllStringIndex(text, -1) {
			if strings.Contains(strings.ToLower(text[loc[0]:loc[1]]), "approaching") {
				continue
			}
			return loc[0], loc[1], true
		}
	}
	return 0, 0, false
}

// limitPatterns are the limitPhrases compiled. Heuristic, like DEAD: a limited
// session cannot print a sentinel. The patterns are anchored to phrasing a
// session emits about ITSELF, not bare words like "limit" that a worker
// reading source code would echo.
var limitPatterns = CompileAll(`(?i)`, limitPhrases)

// CompileAll compiles each pattern with prefix prepended.
func CompileAll(prefix string, pats []string) []*regexp.Regexp {
	out := make([]*regexp.Regexp, len(pats))
	for i, p := range pats {
		out[i] = regexp.MustCompile(prefix + p)
	}
	return out
}

// reRelayHead opens an orchestrator relay or brief echoed into the pane in the
// format that preceded the rota-relay element, bare or after Claude Code's "❯"
// prompt glyph. IsRelayHead reads both formats.
var reRelayHead = regexp.MustCompile(`^\s*(?:❯\s*)?--- ORCHESTRATOR \(round \d+\) ---\s*$`)

// reRelayHeadG is reRelayHead under the gutter.
var reRelayHeadG = regexp.MustCompile(`^\s*(?:[┃❯]\s*)?--- ORCHESTRATOR \(round \d+\) ---\s*$`)

// IsRelayHead reports the line that opens echoed orchestrator text: a
// `<rota-relay …>` opening line, or the legacy `--- ORCHESTRATOR (round N) ---`
// header (under the gutter when the kind has one). tag tells the two apart:
// text opened by a tag ends at its closing line.
func IsRelayHead(l string, gutter bool) (head, tag bool) {
	if harness.IsRelayOpen(l) {
		return true, true
	}
	if gutter {
		return reRelayHeadG.MatchString(l), false
	}
	return reRelayHead.MatchString(l), false
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

// RelayMask marks the lines of echoed orchestrator text: each
// `<rota-relay …>` opening line and what follows through its closing line, the
// exact end of the orchestrator's text. Text echoed in the format that
// preceded the element opens with a `--- ORCHESTRATOR (round N) ---` header and
// has no end marker, so the span runs to the worker's next reply marker. A
// relay echoed under OpenCode's "┃" gutter ends where the gutter does, since
// OpenCode marks the worker's reply with nothing. A brief Claude Code echoes
// behind "❯" ends at the first non-blank line without the two-space echo
// indent: a harness banner at column 0. A reply marker inside a tag-opened span
// is the orchestrator's own text (a "•" bullet), not the worker's reply. With
// sigEnds, a `--- ROTA-SIG <hex> ---` line (the trailer of a signed brief) also
// closes a legacy span, inclusive (a tag-opened span ends only at its closing
// line, so a trailer shape quoted in the body cannot end it): what the harness prints after it, a limit banner
// carrying no reply marker, is the session's own. The trailer straight below a
// closing line is masked with the span either way.
func RelayMask(lines []string, gutter, sigEnds bool) []bool {
	mask := make([]bool, len(lines))
	inRelay, gutterRelay, echoRelay, tagRelay, closed := false, false, false, false, false
	for i, l := range lines {
		head, tag := IsRelayHead(l, gutter)
		if head && !tag && inRelay && tagRelay {
			head = false // a legacy header quoted inside an element is its text
		}
		if echoRelay && strings.TrimSpace(l) != "" && !reEchoIndent.MatchString(l) && !head {
			inRelay, echoRelay, tagRelay = false, false, false
		}
		if gutter && gutterRelay && strings.TrimSpace(l) != "" && !reGutter.MatchString(l) {
			inRelay, gutterRelay, tagRelay = false, false, false
		}
		afterClose := closed
		closed = false
		switch {
		case head:
			inRelay, gutterRelay, echoRelay, tagRelay = true, gutter && reGutter.MatchString(l), reEchoHead.MatchString(l), tag
		case reReplyMarker.MatchString(l) && !tagRelay:
			inRelay, gutterRelay, echoRelay = false, false, false
		}
		mask[i] = inRelay || (afterClose && reSigLine.MatchString(l))
		switch {
		case inRelay && tagRelay && harness.IsRelayClose(l):
			inRelay, gutterRelay, echoRelay, tagRelay, closed = false, false, false, false, true
		case sigEnds && inRelay && !tagRelay && reSigLine.MatchString(l):
			inRelay, gutterRelay, echoRelay, tagRelay = false, false, false, false
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
	lines := pystr.Splitlines(text)
	isWork := func(l string) bool { return AnyMatch(busy, l) || reSpinner.MatchString(l) }
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
	lines := pystr.Splitlines(text)
	mask := RelayMask(lines, RulesFor(kind).Gutter, true)
	kept := lines[:0:0]
	for i, l := range lines {
		if !mask[i] {
			kept = append(kept, l)
		}
	}
	return strings.Join(kept, "\n")
}

// Rules are a kind's PaneVocab compiled.
type Rules struct {
	Retry, Busy, Waiting, Background, Limits []*regexp.Regexp
	AuthDead, BadModel                       *regexp.Regexp
	LoggedOut                                *regexp.Regexp
	LoggedOutAdvice                          string
	Gutter                                   bool
}

var (
	paneRulesMu    sync.Mutex
	paneRulesCache = map[string]*Rules{}
)

// RulesFor is the compiled vocabulary of a kind. The harness resolves an
// unset kind to its default; a kind that is not a worker harness has no rules.
func RulesFor(kind string) *Rules {
	h, known := harness.LookupPane(kind)
	if known {
		kind = h.Kind()
	}
	paneRulesMu.Lock()
	defer paneRulesMu.Unlock()
	if r, ok := paneRulesCache[kind]; ok {
		return r
	}
	r := &Rules{}
	if known {
		v := h.PaneVocab()
		r.Retry, r.Busy = CompileAll(`(?i)`, v.Retry), CompileAll(`(?i)`, v.Busy)
		r.Waiting = CompileAll(`(?i)`, v.Waiting)
		r.Background = CompileAll(`(?i)`, v.Background)
		r.Limits = CompileAll(`(?i)`, v.Limits)
		if v.AuthDead != "" {
			r.AuthDead = regexp.MustCompile(`(?i)` + v.AuthDead)
		}
		if v.LoggedOut != "" {
			r.LoggedOut = regexp.MustCompile(`(?i)` + v.LoggedOut)
			r.LoggedOutAdvice = v.LoggedOutAdvice
		}
		if v.BadModel != "" {
			r.BadModel = regexp.MustCompile(v.BadModel)
		}
		r.Gutter = v.Gutter
	}
	paneRulesCache[kind] = r
	return r
}

// AnyMatch reports whether any pattern matches s.
func AnyMatch(res []*regexp.Regexp, s string) bool {
	for _, re := range res {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}

// DialogFor is DialogRegion with the busy patterns of the given kind's harness,
// for callers outside poll (the usage-limit watcher) that read a pane's text
// as the harness wrote it.
func DialogFor(kind, text string) string {
	return DialogRegion(text, RulesFor(kind).Busy)
}

// reGutter starts a line of OpenCode's user-message gutter.
var reGutter = regexp.MustCompile(`^\s*┃`)

// Scope narrows which part of a pane Detect reads.
type Scope struct {
	// Prompt, when set, keeps only what follows its last occurrence (matched
	// across terminal wrapping), so a message a resume already answered does
	// not count again.
	Prompt string
	// TailLines, when positive, keeps only that many lines from the end.
	TailLines int
}

// Hit is Detect's answer.
type Hit struct {
	// Own is the scoped pane without echoed relays: what the session said
	// about itself. Dialog is its harness-written region. Both are set whether
	// or not a limit was found.
	Own, Dialog string
	// Start and End are the byte span of the limit phrase inside Dialog.
	Start, End int
	// Line is the dialog line holding the phrase, trimmed.
	Line string
	// Rest is Dialog from the start of that line, at most 400 bytes.
	Rest string
}

// Detect is the one chain that decides what a usage limit is: scope the pane,
// strip echoed relays, keep the harness's dialog region, find the limit
// phrase. The worker classifier and the limits watcher both call it.
func Detect(kind, text string, sc Scope) (Hit, bool) {
	if re := promptRe(sc.Prompt); re != nil {
		if locs := re.FindAllStringIndex(text, -1); len(locs) > 0 {
			text = text[locs[len(locs)-1][1]:]
		}
	}
	if sc.TailLines > 0 {
		lines := strings.Split(strings.TrimRight(text, "\n \t"), "\n")
		if len(lines) > sc.TailLines {
			lines = lines[len(lines)-sc.TailLines:]
		}
		text = strings.Join(lines, "\n")
	}
	var h Hit
	h.Own = StripRelays(kind, text)
	h.Dialog = DialogFor(kind, h.Own)
	var ok bool
	if h.Start, h.End, ok = FindLimit(kind, h.Dialog); !ok {
		return h, false
	}
	d := h.Dialog
	from := strings.LastIndex(d[:h.Start], "\n") + 1
	to := strings.Index(d[h.Start:], "\n")
	if to < 0 {
		to = len(d)
	} else {
		to += h.Start
	}
	h.Rest = d[from:]
	if len(h.Rest) > 400 {
		h.Rest = h.Rest[:400]
	}
	h.Line = strings.TrimSpace(d[from:to])
	return h, true
}

// promptRe matches the prompt as a terminal may have wrapped it; nil for none.
func promptRe(prompt string) *regexp.Regexp {
	words := strings.Fields(prompt)
	if len(words) == 0 {
		return nil
	}
	for i, w := range words {
		words[i] = regexp.QuoteMeta(w)
	}
	re, err := regexp.Compile(strings.Join(words, `\s+`))
	if err != nil {
		return nil
	}
	return re
}
