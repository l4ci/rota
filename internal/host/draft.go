package host

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// ErrDraftOnPrompt: the slot's prompt line holds text rota did not type (a
// human mid-draft); nothing was sent. Retry once the draft is sent or cleared.
var ErrDraftOnPrompt = errors.New("a human draft is on the prompt line, so nothing was typed")

// Drafter is implemented by a host that can tell whether a slot's prompt line
// holds a human draft. Send already refuses on one; this is the same check on
// its own.
type Drafter interface {
	// Draft returns the text on the slot's prompt line that is not the file's
	// own brief, "" when the line is empty or holds only rota's brief.
	Draft(ctx context.Context, slot, handle, file string) string
}

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
	line = strings.Trim(line, " \t\r│")
	if dialogOption.MatchString(line) {
		return "" // a dialog's selected option, not a prompt
	}
	for _, p := range promptPlaceholders {
		if strings.HasPrefix(line, p) {
			return ""
		}
	}
	return line
}

// stripDim turns a styled capture into plain text with the dim runs removed.
// Claude Code renders its ghost suggestion faint (SGR 2) or gray on an idle
// prompt, and nobody typed it. Prompt glyphs are kept even when dimmed, since
// promptLine anchors on them. Every escape sequence is dropped.
func stripDim(s string) string {
	var b strings.Builder
	dim := false
	for i := 0; i < len(s); {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
			if j < len(s) && s[j] == 'm' {
				dim = sgrDim(s[i+2:j], dim)
			}
			i = j + 1
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
	if line == "" || briefInPrompt(line, brief) {
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
