package escalation

import (
	"strings"
	"unicode"

	"github.com/l4ci/rota/internal/marker"
	"github.com/l4ci/rota/internal/tracker"
)

// IsAnswer is the answer rule for one comment body: any comment without a rota
// marker. The author decides nothing, because the orchestrator and the human
// usually post as the same account; rota marks every comment it posts instead.
// To change what counts as an answer, change this function; everything else
// goes through it.
func IsAnswer(body string) bool {
	return !marker.Has(body)
}

// FindAnswer applies the rule to a thread's comments, oldest first: the answer
// is the first comment after the escalation comment for which IsAnswer holds.
// escFound is false when the escalation comment is not in the list (deleted),
// in which case nothing can be ordered against it and found is false too.
func FindAnswer(comments []tracker.Comment, escalationCommentID string) (answer tracker.Comment, found, escFound bool) {
	for _, c := range comments {
		if !escFound {
			escFound = c.ID == escalationCommentID
			continue
		}
		if IsAnswer(c.Body) {
			return c, true, true
		}
	}
	return tracker.Comment{}, false, escFound
}

// ApprovalWords are the single words that approve a merge; ApprovalPhrase is
// the two-word phrase that does. Approves is the only reader of either.
var (
	ApprovalWords  = []string{"approve", "approved", "yes", "lgtm"}
	ApprovalPhrase = "ship it"
)

// Approves is the C5 allowlist: an answer approves a merge when the first
// word of its first non-blank line, lowercased with trailing punctuation
// removed, is in ApprovalWords, or the line's first two words are ApprovalPhrase.
// Internal punctuation stays, so "yes-ish" does not approve.
func Approves(answer string) bool {
	var line string
	for _, l := range strings.Split(answer, "\n") {
		if strings.TrimSpace(l) != "" {
			line = l
			break
		}
	}
	words := strings.Fields(line)
	for i, w := range words {
		words[i] = strings.ToLower(strings.TrimRightFunc(w, unicode.IsPunct))
	}
	if len(words) == 0 {
		return false
	}
	for _, a := range ApprovalWords {
		if words[0] == a {
			return true
		}
	}
	return len(words) > 1 && words[0]+" "+words[1] == ApprovalPhrase
}
