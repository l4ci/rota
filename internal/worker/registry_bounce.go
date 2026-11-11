package worker

import (
	"encoding/json"
	"strconv"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/ledger"
)

// recordBounce counts one bounce per (issue, review head). That is a semantic
// dedupe across callers (review-relay and bounce both reach it), so it is not a
// request-id receipt: a retry mints a fresh id, yet must still count once. It
// stays here, and receiptedVerbs in internal/cli leaves round bounce out.
func recordBounce(env Env, m *RoundMemo, root, issue, slot, head string, limit int) (n int, counted, capped bool, err error) {
	defer func() {
		if err == nil && counted {
			LedgerNoteIn(env, m, root, ledger.Entry{Kind: ledger.KindBounce, Issue: issue, Slot: slot, Detail: ledger.Detail("count", n)})
		}
	}()
	err = Update(root, func(d *Doc) {
		doc := d.doc
		b := bouncesOf(doc)
		heads := bounceHeadsOf(doc)
		n = bounceCount(b, issue)
		if AtCap(n, limit) {
			capped = true
			return
		}
		if last, _ := heads.Get(issue); head != "" && last == any(head) && n > 0 {
			return
		}
		n++
		counted = true
		b.Set(issue, json.Number(strconv.Itoa(n)))
		doc.Set("bounces", b)
		if head != "" {
			heads.Set(issue, head)
			doc.Set("bounceHeads", heads)
		}
	})
	return n, counted, capped, err
}

// UnrecordBounce undoes one recordBounce(root, issue, head) whose relay did not
// go out; prev is the registry from before it. The count drops by one from what
// the registry holds now, never to prev's, so a bounce another writer counted
// meanwhile stays. The head goes back to prev's only while it is still the one
// this call recorded: a newer head from another writer stays. A call that did
// not count (recordBounce deduped on the same head) has nothing to undo, so
// the caller does not call this for it.
func UnrecordBounce(root, issue, head string, prev Registry) error {
	return Update(root, func(d *Doc) {
		doc := d.doc
		b, heads := bouncesOf(doc), bounceHeadsOf(doc)
		if n := bounceCount(b, issue) - 1; n > 0 {
			b.Set(issue, json.Number(strconv.Itoa(n)))
		} else {
			b.Delete(issue)
		}
		if cur, _ := heads.Get(issue); head != "" && cur == any(head) {
			if h, ok := bounceHeadsOf(prev.doc).Get(issue); ok {
				heads.Set(issue, h)
			} else {
				heads.Delete(issue)
			}
		}
		doc.Set("bounces", b)
		doc.Set("bounceHeads", heads)
	})
}

// Bounces is how often an item's PR has been sent back so far (0 when never).
func (r Registry) Bounces(issue string) int {
	return bounceCount(bouncesOf(r.doc), issue)
}

// BounceHead is the PR head the gate last sent back for an item, "" when it
// never did. A branch still at that head has not answered the bounce.
func (r Registry) BounceHead(issue string) string {
	v, _ := bounceHeadsOf(r.doc).Get(issue)
	s, _ := v.(string)
	return s
}

// ClearBounces forgets an item's count: its PR merged or it was handed over.
func ClearBounces(root, issue string) error {
	return Update(root, func(d *Doc) {
		doc := d.doc
		if b := bouncesOf(doc); bounceCount(b, issue) > 0 {
			b.Delete(issue)
			doc.Set("bounces", b)
			heads := bounceHeadsOf(doc)
			heads.Delete(issue)
			doc.Set("bounceHeads", heads)
		}
	})
}

// bounceHeadsOf is the issue -> PR head SHA of the last counted bounce.
func bounceHeadsOf(doc *jsonx.Object) *jsonx.Object {
	if v, _ := doc.Get("bounceHeads"); v != nil {
		if b, ok := v.(*jsonx.Object); ok {
			return b
		}
	}
	return jsonx.NewObject()
}

func bouncesOf(doc *jsonx.Object) *jsonx.Object {
	if v, _ := doc.Get("bounces"); v != nil {
		if b, ok := v.(*jsonx.Object); ok {
			return b
		}
	}
	return jsonx.NewObject()
}

func bounceCount(b *jsonx.Object, issue string) int {
	v, _ := b.Get(issue)
	if n, ok := v.(json.Number); ok {
		if i, err := strconv.Atoi(n.String()); err == nil {
			return i
		}
	}
	return 0
}
