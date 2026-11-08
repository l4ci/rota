package tracker

import (
	"context"

	"github.com/l4ci/rota/internal/marker"
)

// markedComments is the rule Jira and Linear share for a comment that carries
// a rota marker: people see marker.Visible(body), the full body is kept out of
// band, a store that fails takes the half-made comment back down, and an edit
// that drops the marker drops the stored body too. The adapter binds the issue
// into each primitive and supplies only the storage calls.
type markedComments struct {
	// post creates a comment showing shown and returns its id; edit replaces
	// the shown text; remove deletes the comment (the rollback).
	post   func(ctx context.Context, shown string) (string, error)
	edit   func(ctx context.Context, id, shown string) error
	remove func(ctx context.Context, id string) error
	// put keeps body whole for comment id; replace is true when the comment
	// already existed, so an older stored body may need clearing. drop clears
	// the stored body; none stored is not an error.
	put  func(ctx context.Context, id, body string, replace bool) error
	drop func(ctx context.Context, id string) error
	// fits, when set, refuses a body the store cannot hold before anything is
	// written; name says which comment ("a comment" or "comment <id>").
	fits func(name, body string) error
	// rollbackFailed, when set, turns a failed rollback into the error to
	// report; unset, the store error is reported and the rollback's dropped.
	rollbackFailed func(id string, putErr, removeErr error) error
	// stored is the full body kept for comment id, if any.
	stored func(id string) (string, bool)
}

// shownText is the text a comment displays for body.
func shownText(body string) string {
	if marker.Has(body) {
		return marker.Visible(body)
	}
	return body
}

// Add posts body, then stores it whole when it carries a marker. A store that
// fails takes the comment back down.
func (m markedComments) Add(ctx context.Context, body string) (string, error) {
	marked := marker.Has(body)
	if marked && m.fits != nil {
		if err := m.fits("a comment", body); err != nil {
			return "", err
		}
	}
	id, err := m.post(ctx, shownText(body))
	if err != nil {
		return "", err
	}
	if marked {
		if err := m.put(ctx, id, body, false); err != nil {
			if rerr := m.remove(ctx, id); rerr != nil && m.rollbackFailed != nil {
				return "", m.rollbackFailed(id, err, rerr)
			}
			return "", err
		}
	}
	return id, nil
}

// Edit replaces the comment's text, then stores body whole when it carries a
// marker or drops the stored body when it does not.
func (m markedComments) Edit(ctx context.Context, id, body string) error {
	marked := marker.Has(body)
	if marked && m.fits != nil {
		if err := m.fits("comment "+id, body); err != nil {
			return err
		}
	}
	if err := m.edit(ctx, id, shownText(body)); err != nil {
		return err
	}
	if !marked {
		return m.drop(ctx, id)
	}
	return m.put(ctx, id, body, true)
}

// Body is the full body for comment id: the stored one when there is one, else
// the text the tracker lists.
func (m markedComments) Body(id, listed string) string {
	if full, ok := m.stored(id); ok {
		return full
	}
	return listed
}
