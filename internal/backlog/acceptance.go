package backlog

import (
	"context"
	"strings"

	"github.com/l4ci/rota/internal/acceptance"
	"github.com/l4ci/rota/internal/blockedby"
	"github.com/l4ci/rota/internal/marker"
	"github.com/l4ci/rota/internal/tracker"
)

// DecisionTrust says whether a comment author has write (or higher) access to
// the repository. Nil means the tracker cannot say: no decision counts.
type DecisionTrust func(author string) (bool, error)

// TrustOf takes the author check from src when it implements tracker.PushTrust
// (GitHub), else nil. Answers are cached per login for the life of the check.
func TrustOf(ctx context.Context, src any) DecisionTrust {
	pt, ok := src.(tracker.PushTrust)
	if !ok {
		return nil
	}
	seen := map[string]bool{}
	return func(author string) (bool, error) {
		if v, ok := seen[author]; ok {
			return v, nil
		}
		v, err := pt.CanPush(ctx, author)
		if err == nil {
			seen[author] = v
		}
		return v, err
	}
}

// TrustedDecisionIDs are the ids of the comments carrying the decision marker
// whose author trust vouches for. A nil trust vouches for none.
func TrustedDecisionIDs(comments []tracker.Comment, trust DecisionTrust) ([]string, error) {
	var ids []string
	for _, c := range comments {
		if k, _, ok := marker.ParseComment(c.Body); !ok || k != "decision" || trust == nil {
			continue
		}
		if ok, err := trust(c.Author); err != nil {
			return nil, err
		} else if ok {
			ids = append(ids, c.ID)
		}
	}
	return ids, nil
}

// DecisionIDs is the item's trusted decision comments, for `plan pass --decision`.
func (b *Issues) DecisionIDs(ref string) ([]string, error) {
	is, _, err := b.require(ref)
	if err != nil {
		return nil, err
	}
	comments, err := b.Tracker.Comments(b.ctx(), is.Key)
	if err != nil {
		return nil, err
	}
	return TrustedDecisionIDs(comments, TrustOf(b.ctx(), b.Tracker))
}

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
// A decision mark counts only when its comment carries the decision marker and
// trust says the comment's author has write access; a nil trust counts none.
func UnmetAcceptance(body string, comments []tracker.Comment, lookup func(text, sha, check string) (string, bool), trust DecisionTrust) ([]acceptance.Status, error) {
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
	decisions, err := TrustedDecisionIDs(comments, trust)
	if err != nil {
		return nil, err
	}
	return unmetAcceptance(body, notes["acceptance"], notes["proof"], decisions, lookup), nil
}

func unmetAcceptance(body, note, proof string, decisions []string, lookup func(text, sha, check string) (string, bool)) []acceptance.Status {
	crits, _ := acceptance.Parse(body)
	explicit := map[string]bool{}
	for _, c := range crits {
		if c.ID != "" {
			explicit[c.ID] = true
		}
	}
	var unmet []acceptance.Status
	for _, st := range acceptance.Coverage(body, acceptance.ParseMarks(note), acceptance.WithDecisions(func(sha, check string) (string, bool) {
		if lookup == nil {
			return "", false
		}
		return lookup(proof, sha, check)
	}, decisions)) {
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
	return UnmetAcceptance(is.Body, comments, b.LookupProof, TrustOf(b.ctx(), b.Tracker))
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
