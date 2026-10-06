package host

import (
	"context"
	"errors"
	"regexp"
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
