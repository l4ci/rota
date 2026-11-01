package host

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/l4ci/rota/internal/tui"
)

// ErrDraftOnPrompt: the slot's prompt line holds text rota did not type (a
// human mid-draft); nothing was sent. Retry once the draft is sent or cleared.
var ErrDraftOnPrompt = errors.New("a human draft is on the prompt line, so nothing was typed")

// Draft retries: a human may be about to press Enter, so Send waits a little
// before it refuses. Three settles of two seconds is inside the time a brief
// already gets (4 Enter attempts, 3s each).
const (
	draftRetries = 3
	draftWait    = 2 * time.Second
)

// promptMarkers start the input line: Claude Code's and Codex's.
const promptMarkers = "❯›"

// dialogOption: a dialog marks its selected choice with the same glyph.
var dialogOption = regexp.MustCompile(`^\d+[.)]\s`)

// dialogFooter: a dialog's key hint below its options. Marks an unnumbered
// choice (Claude Code's folder-trust dialog) that dialogOption cannot.
var dialogFooter = regexp.MustCompile(`(?i)enter (to )?(confirm|select)|esc (to cancel|not now)`)

// promptPlaceholders are what an empty prompt line shows. They read as text but
// nobody typed them.
var promptPlaceholders = []string{`Try "`, "Ask Codex to do anything"}

// promptLine returns the text after the pane's last prompt marker, up to the
// end of that line, "" when the pane has no marker or the line is empty or a
// placeholder.
func promptLine(pane string) string {
	i := strings.LastIndexAny(pane, promptMarkers)
	if i < 0 {
		return ""
	}
	_, size := utf8.DecodeRuneInString(pane[i:])
	rest := pane[i+size:]
	line, _, _ := strings.Cut(rest, "\n")
	line = strings.TrimFunc(line, func(r rune) bool { return unicode.IsSpace(r) || r == '│' })
	if isDialog(line, rest) {
		return "" // a dialog's selected option, not a prompt
	}
	for _, p := range promptPlaceholders {
		if strings.HasPrefix(line, p) {
			return ""
		}
	}
	return line
}

// themeOption is a row of Claude Code's first-run theme picker, whose
// highlighted choice sits after the prompt glyph like input would.
var themeOption = regexp.MustCompile(`^(?:Dark|Light) mode\b|^Auto \(match terminal\)`)

func isDialog(line, rest string) bool {
	return themeOption.MatchString(line) || dialogOption.MatchString(line) || dialogFooter.MatchString(rest)
}

// DialogError is ErrDialogOpen with the dialog's first line, for the refusal message.
type DialogError struct{ First string }

func (e *DialogError) Error() string { return ErrDialogOpen.Error() + ": " + e.First }

func (e *DialogError) Is(target error) bool { return target == ErrDialogOpen }

// Dialog is a harness dialog standing on the prompt line.
type Dialog struct {
	First   string   // the dialog's title line
	Dismiss []string // keys that dismiss it without choosing anything; nil when unknown
}

var paneRule = regexp.MustCompile(`^[─━]{10,}$`)

// ReadDialog reports whether the pane's prompt line is a harness dialog's
// option row rather than input, with its first line and, for the dialogs whose
// dismissal is known to be safe, the keys. Claude Code's autocompact picker
// says "esc not now", so Escape declines it; any other dialog stays unknown.
func ReadDialog(pane string) (Dialog, bool) {
	i := strings.LastIndexAny(pane, promptMarkers)
	if i < 0 {
		return Dialog{}, false
	}
	_, size := utf8.DecodeRuneInString(pane[i:])
	rest := pane[i+size:]
	line, _, _ := strings.Cut(rest, "\n")
	line = strings.TrimFunc(line, func(r rune) bool { return unicode.IsSpace(r) || r == '│' })
	if !isDialog(line, rest) {
		return Dialog{}, false
	}
	// The title is the first text line below the last rule above the marker.
	var first string
	for _, l := range strings.Split(pane[:i], "\n") {
		t := strings.TrimFunc(l, func(r rune) bool { return unicode.IsSpace(r) || r == '│' })
		switch {
		case paneRule.MatchString(t):
			first = ""
		case t != "" && first == "":
			first = t
		}
	}
	if first == "" {
		first = line
	}
	d := Dialog{First: first}
	if strings.Contains(pane[:i], "Want to autocompact at") && strings.Contains(strings.ToLower(rest), "esc not now") {
		d.Dismiss = []string{"esc"}
	}
	return d, true
}

// clearDialog dismisses a dialog on the prompt line once when its dismissal is
// known and re-reads; a dialog it cannot dismiss, or one that survives, is
// refused as a *DialogError.
func clearDialog(d *Deps, read func() string, press func(key string)) error {
	dlg, ok := ReadDialog(read())
	if !ok {
		return nil
	}
	if dlg.Dismiss == nil {
		return &DialogError{First: dlg.First}
	}
	for _, k := range dlg.Dismiss {
		press(k)
	}
	d.settle(time.Second)
	if again, ok := ReadDialog(read()); ok {
		return &DialogError{First: again.First}
	}
	return nil
}

// stripDim turns a styled capture into plain text with the dim runs removed.
// Claude Code renders its ghost suggestion faint (SGR 2) or gray on an idle
// prompt, and nobody typed it. Prompt glyphs are kept even when dimmed, since
// promptLine anchors on them. Every escape sequence is dropped.
func stripDim(s string) string {
	var b strings.Builder
	dim := false
	for i := 0; i < len(s); {
		if end := tui.CSIEnd(s, i); end >= 0 {
			if end > i+2 && s[end-1] == 'm' {
				dim = sgrDim(s[i+2:end-1], dim)
			}
			i = end
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if !dim || r == '\n' || strings.ContainsRune(promptMarkers, r) {
			b.WriteString(s[i : i+size])
		}
		i += size
	}
	return b.String()
}

// midGray: equal channels (within a few units) in the middle of the range.
// White, black and near-extremes are ordinary text colours, and missing a
// real draft is worse than a false refusal.
func midGray(r, g, b int) bool {
	lo, hi := min(r, g, b), max(r, g, b)
	return hi-lo <= 8 && lo >= 90 && hi <= 200
}

// sgrDim applies one SGR parameter list to the dim state: faint (2) and a gray
// foreground (90, 256-colour 241-250, or a mid-range equal-channel truecolour) start it;
// reset (0, empty), normal intensity (22) and any other foreground end it.
func sgrDim(params string, dim bool) bool {
	p := strings.Split(params, ";")
	if params == "" {
		return false
	}
	for i := 0; i < len(p); i++ {
		n, err := strconv.Atoi(p[i])
		if err != nil {
			continue
		}
		switch {
		case n == 0, n == 22, n == 39:
			dim = false
		case n == 2, n == 90:
			dim = true
		case (n >= 30 && n <= 37) || (n >= 91 && n <= 97):
			dim = false
		case n == 38 && i+1 < len(p):
			mode := p[i+1]
			if mode == "5" && i+2 < len(p) {
				c, _ := strconv.Atoi(p[i+2])
				// The gray ramp 232-255 runs 8..238 in steps of 10; only its
				// middle (about 90-200) reads as a suggestion.
				dim = c >= 241 && c <= 250
				i += 2
			} else if mode == "2" && i+4 < len(p) {
				r, _ := strconv.Atoi(p[i+2])
				g, _ := strconv.Atoi(p[i+3])
				bl, _ := strconv.Atoi(p[i+4])
				dim = midGray(r, g, bl)
				i += 4
			}
		}
	}
	return dim
}

// humanDraft returns the prompt line's text when it is neither empty nor the
// brief rota itself left there (its tail, or a paste placeholder), else "".
func humanDraft(pane, brief string) string {
	line := promptLine(pane)
	if line == "" {
		return ""
	}
	// A multi-line brief runs past the first prompt line, so look for its tail
	// in everything after the marker.
	i := strings.LastIndexAny(pane, promptMarkers)
	_, size := utf8.DecodeRuneInString(pane[i:])
	if briefInPrompt(line, brief) || briefInPrompt(pane[i+size:], brief) {
		return ""
	}
	return line
}

// briefInPrompt: prompt holds a paste placeholder or the brief's tail. The tail
// is the brief's last line: it is typed last, so it is there only when the whole
// brief is. Whitespace is dropped on both sides so soft wraps do not matter.
func briefInPrompt(prompt, brief string) bool {
	if pastePlaceholder.MatchString(prompt) {
		return true
	}
	tail := briefTail(brief)
	return tail != "" && strings.Contains(squeeze(prompt), tail)
}

func briefTail(brief string) string {
	tail := ""
	for _, l := range strings.Split(brief, "\n") {
		if t := squeeze(l); t != "" {
			tail = t
		}
	}
	if len(tail) > 80 {
		tail = tail[len(tail)-80:]
	}
	return tail
}

// waitNoDraft polls read until the prompt line holds no human draft, settling
// between polls. It returns the draft still there after the last poll, "" when
// the line cleared.
func waitNoDraft(d *Deps, read func() string, brief string) string {
	draft := ""
	for i := 0; i < draftRetries; i++ {
		if draft = humanDraft(read(), brief); draft == "" {
			return ""
		}
		if i+1 < draftRetries {
			d.settle(draftWait)
		}
	}
	return draft
}
