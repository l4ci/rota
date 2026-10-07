package worker

import (
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/jsonx"
)

// BestOfAttempt is one slot's attempt at a best-of:2 issue.
type BestOfAttempt struct{ Slot, Branch, ClaimID string }

// BestOf is a best-of:2 issue's attempts and the PR the orchestrator picked ("" until then).
type BestOf struct {
	Issue    string
	Attempts []BestOfAttempt
	Pick     string // PR ref as given to SetBestOfPick, e.g. "#123" or a URL
	Round    int
}

// bestOfKey is the registry key of an issue: upper case, no `#`, no spaces,
// the spelling QueuedIssue compares by.
func bestOfKey(issue string) string {
	return strings.ToUpper(strings.TrimPrefix(strings.TrimSpace(issue), "#"))
}

func bestOfsOf(doc *jsonx.Object) *jsonx.Object {
	if v, _ := doc.Get("bestOf"); v != nil {
		if m, ok := v.(*jsonx.Object); ok {
			return m
		}
	}
	return jsonx.NewObject()
}

// BestOf is the best-of:2 record of an issue, nil when it has none.
func (r Registry) BestOf(issue string) *BestOf {
	key := bestOfKey(issue)
	v, _ := bestOfsOf(r.doc).Get(key)
	o, ok := v.(*jsonx.Object)
	if !ok {
		return nil
	}
	rv, _ := o.Get("round")
	round, _ := jsonx.Int(rv)
	b := &BestOf{Issue: key, Pick: jsonx.Str(o, "pick"), Round: round}
	raw, _ := o.Get("attempts")
	list, _ := raw.([]any)
	for _, e := range list {
		if a, ok := e.(*jsonx.Object); ok {
			b.Attempts = append(b.Attempts, BestOfAttempt{Slot: jsonx.Str(a, "slot"), Branch: jsonx.Str(a, "branch"), ClaimID: jsonx.Str(a, "claimId")})
		}
	}
	return b
}

// Attempt is the attempt of a slot, nil when the slot made none.
func (b BestOf) Attempt(slot string) *BestOfAttempt {
	for i := range b.Attempts {
		if b.Attempts[i].Slot == slot {
			return &b.Attempts[i]
		}
	}
	return nil
}

// Sibling is the attempt of another slot, nil when there is none.
func (b BestOf) Sibling(slot string) *BestOfAttempt {
	for i := range b.Attempts {
		if b.Attempts[i].Slot != slot {
			return &b.Attempts[i]
		}
	}
	return nil
}

// Picked reports whether the pick names pr, compared by PR number.
func (b BestOf) Picked(pr string) bool {
	want, ok := PRRefNumber(pr)
	if !ok {
		return false
	}
	got, ok := PRRefNumber(b.Pick)
	return ok && got == want
}

func (b BestOf) object() *jsonx.Object {
	attempts := make([]any, 0, len(b.Attempts))
	for _, a := range b.Attempts {
		o := jsonx.NewObject()
		o.Set("slot", a.Slot)
		o.Set("branch", a.Branch)
		o.Set("claimId", a.ClaimID)
		attempts = append(attempts, o)
	}
	o := jsonx.NewObject()
	o.Set("attempts", attempts)
	o.Set("pick", b.Pick)
	o.Set("round", b.Round)
	return o
}

// SetBestOf records b, replacing the record of the same issue.
func (d *Doc) SetBestOf(b BestOf) {
	m := bestOfsOf(d.doc)
	m.Set(bestOfKey(b.Issue), b.object())
	d.doc.Set("bestOf", m)
}

// DropBestOf forgets the record of an issue.
func (d *Doc) DropBestOf(issue string) {
	m := bestOfsOf(d.doc)
	if _, ok := m.Get(bestOfKey(issue)); !ok {
		return
	}
	m.Delete(bestOfKey(issue))
	d.doc.Set("bestOf", m)
}

// SetBestOfPick records the PR the orchestrator picked for an issue; an error
// when the issue has no best-of record.
func SetBestOfPick(root, issue, pr string) error {
	if LoadRegistry(root).BestOf(issue) == nil {
		return fmt.Errorf("%s has no best-of record", issue)
	}
	return Update(root, func(d *Doc) {
		if b := d.BestOf(issue); b != nil {
			b.Pick = pr
			d.SetBestOf(*b)
		}
	})
}

// ClearBestOf forgets an issue's record: its PR landed or the issue was handed
// over. No record means no write.
func ClearBestOf(root, issue string) error {
	if LoadRegistry(root).BestOf(issue) == nil {
		return nil
	}
	return Update(root, func(d *Doc) { d.DropBestOf(issue) })
}
