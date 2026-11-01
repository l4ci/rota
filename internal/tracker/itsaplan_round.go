package tracker

import (
	"context"
)

// ---- claims (plain comments) --------------------------------------------------

// claimStore is the shared claim-comment rule over the issue's comment feed.
func (t *Itsaplan) claimStore() claimComments {
	return claimComments{
		list: t.Comments,
		add: func(ctx context.Context, key IssueKey, body string) error {
			_, err := t.AddComment(ctx, key, body)
			return err
		},
		delete: t.DeleteComment,
	}
}

// Claims lists the claim ids on key, earliest first, each once. The feed is
// read to its last page, so a claim past the first 100 comments still counts.
func (t *Itsaplan) Claims(ctx context.Context, key IssueKey) ([]string, error) {
	return t.claimStore().Claims(ctx, key)
}

// PutClaim posts id's plain claim comment; the earliest one of an id holds its place.
func (t *Itsaplan) PutClaim(ctx context.Context, key IssueKey, id string) error {
	return t.claimStore().PutClaim(ctx, key, id)
}

// DropClaim deletes every claim comment of id; none is a no-op.
func (t *Itsaplan) DropClaim(ctx context.Context, key IssueKey, id string) error {
	return t.claimStore().DropClaim(ctx, key, id)
}

// ---- assign and close on merge -------------------------------------------------

// AssignSelf makes the key's user the assignee; It's a Plan has one.
func (t *Itsaplan) AssignSelf(ctx context.Context, key IssueKey) error {
	me, err := t.viewer(ctx)
	if err != nil {
		return err
	}
	id, err := t.issueID(ctx, key)
	if err != nil {
		return err
	}
	return t.patch(ctx, id, map[string]any{"assigneeUserId": me})
}

// CloseAfterMerge closes the issue a merged PR names. rota does not rely on
// It's a Plan's own forge webhook, which is optional: an issue already in a
// completed or canceled column, by that webhook or by hand, is left alone.
func (t *Itsaplan) CloseAfterMerge(ctx context.Context, key IssueKey, pr string) error {
	return closeAfterMerge(ctx, key, pr, t.Close, func() (bool, error) {
		m, err := t.projectMeta(ctx)
		if err != nil {
			return false, err
		}
		d, err := t.issue(ctx, key)
		if err != nil {
			return false, err
		}
		c := m.column(d.ColumnID)
		return c != nil && (c.StateType == "completed" || c.StateType == "canceled"), nil
	})
}
