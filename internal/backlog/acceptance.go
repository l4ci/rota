package backlog

import (
	"strings"

	"github.com/l4ci/rota/internal/acceptance"
	"github.com/l4ci/rota/internal/blockedby"
	"github.com/l4ci/rota/internal/tracker"
)

// HasAcceptanceIDs reports whether the item explicitly names AC-n criteria.
// Legacy unnumbered criteria keep their completion behavior.
func HasAcceptanceIDs(body string) bool {
	crits, _ := acceptance.Parse(body)
	for _, c := range crits {
		if c.ID != "" {
			return true
		}
	}
	return false
}

// UnmetAcceptance reads the same multipart notes and proof lookup as plan pass.
// Only explicit ids still in the body can block completion; removed marks and
// legacy unnumbered criteria do not acquire a new completion requirement.
func UnmetAcceptance(body string, comments []tracker.Comment, lookup func(text, sha, check string) (string, bool)) ([]acceptance.Status, error) {
	notes := map[string]string{}
	for _, kind := range []string{"acceptance", "proof"} {
		parts, err := noteParts(comments, kind)
		if err != nil {
			return nil, err
		}
		var text strings.Builder
		for _, p := range parts {
			text.WriteString(p.rest)
		}
		notes[kind] = text.String()
	}
	return unmetAcceptance(body, notes["acceptance"], notes["proof"], lookup), nil
}

func unmetAcceptance(body, note, proof string, lookup func(text, sha, check string) (string, bool)) []acceptance.Status {
	crits, _ := acceptance.Parse(body)
	explicit := map[string]bool{}
	for _, c := range crits {
		if c.ID != "" {
			explicit[c.ID] = true
		}
	}
	var unmet []acceptance.Status
	for _, st := range acceptance.Coverage(body, acceptance.ParseMarks(note), func(sha, check string) (string, bool) {
		if lookup == nil {
			return "", false
		}
		return lookup(proof, sha, check)
	}) {
		if explicit[st.ID] && !st.Met && st.Flag != "missing" {
			unmet = append(unmet, st)
		}
	}
	return unmet
}

// itemUnmetAcceptance reads notes only for items with explicit criteria.
func (b *Issues) itemUnmetAcceptance(is Issue) ([]acceptance.Status, error) {
	if !HasAcceptanceIDs(is.Body) {
		return nil, nil
	}
	comments, err := b.Tracker.Comments(b.ctx(), is.Key)
	if err != nil {
		return nil, err
	}
	return UnmetAcceptance(is.Body, comments, b.LookupProof)
}

// AcceptanceRefusal is shared by item completion and the pre-merge gate.
func AcceptanceRefusal(id string, unmet []acceptance.Status) *RefusedError {
	var lines []string
	for _, st := range unmet {
		lines = append(lines, st.ID+": "+st.Text)
	}
	return &RefusedError{BlockedBy: blockedby.UnmetAcceptance,
		Msg:  "[" + id + "] unmet acceptance criteria: " + strings.Join(lines, "; "),
		Hint: "mark criteria with `rota plan pass`, or pass --unmet-reason <text> to item complete"}
}

// AcceptanceRecord is the durable disclosure attached to an overridden close.
func AcceptanceRecord(unmet []acceptance.Status, reason string) string {
	var b strings.Builder
	b.WriteString("Unmet acceptance criteria:\n\n")
	for _, st := range unmet {
		b.WriteString("- " + st.ID + ": " + st.Text + "\n")
	}
	b.WriteString("\nReason: " + oneLine(reason))
	return b.String()
}
