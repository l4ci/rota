package tracker

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
)

// ---- project metadata kept in step with writes ------------------------------

// labelIDs is the ids of the named labels, creating the missing ones when
// create is set; without it a missing label is KindNotFound.
func (t *Itsaplan) labelIDs(ctx context.Context, names []string, create bool) ([]int, error) {
	if _, err := t.projectMeta(ctx); err != nil {
		return nil, err
	}
	var ids []int
	for _, name := range uniq(names) {
		id, err := t.labelID(ctx, name, create)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func (t *Itsaplan) labelID(ctx context.Context, name string, create bool) (int, error) {
	t.mu.Lock()
	for _, l := range t.meta.Labels {
		if strings.EqualFold(l.Name, name) {
			t.mu.Unlock()
			return l.ID, nil
		}
	}
	t.mu.Unlock()
	if !create {
		return 0, &Error{Kind: KindNotFound, Code: 1, Message: fmt.Sprintf("It's a Plan project %s has no label %q", t.project, name)}
	}
	var l itsaplanLabel
	if err := t.rest.Do(ctx, http.MethodPost, t.projectPath("/labels"), map[string]any{"name": name}, &l); err != nil {
		return 0, err
	}
	t.mu.Lock()
	t.meta.Labels = append(t.meta.Labels, l)
	t.mu.Unlock()
	return l.ID, nil
}

// fieldID is the rota custom field's id, creating the field when the project
// has none; a key that may not create it fails naming the field.
func (t *Itsaplan) fieldID(ctx context.Context) (int, error) {
	m, err := t.projectMeta(ctx)
	if err != nil {
		return 0, err
	}
	t.mu.Lock()
	f := m.field()
	t.mu.Unlock()
	if f != nil {
		return f.ID, nil
	}
	if !m.permits("custom_fields", "create") {
		return 0, unavailable("It's a Plan project %s has no custom field %q and this API key may not create one (ask a project admin to add a text field named %q)", t.project, itsaplanFieldName, itsaplanFieldName)
	}
	var nf itsaplanField
	if err := t.rest.Do(ctx, http.MethodPost, t.projectPath("/custom-fields"), map[string]any{"name": itsaplanFieldName, "fieldType": "text"}, &nf); err != nil {
		return 0, err
	}
	t.mu.Lock()
	m.CustomFields = append(m.CustomFields, nf)
	t.mu.Unlock()
	return nf.ID, nil
}

// putFields stores block as the rota field's value on issue id; "" clears it.
func (t *Itsaplan) putFields(ctx context.Context, id int, block string) error {
	if block == "" {
		// nothing to clear on a project that never had the field
		m, err := t.projectMeta(ctx)
		if err != nil {
			return err
		}
		t.mu.Lock()
		f := m.field()
		t.mu.Unlock()
		if f == nil {
			return nil
		}
	}
	fid, err := t.fieldID(ctx)
	if err != nil {
		return err
	}
	return t.rest.Do(ctx, http.MethodPut, "/issues/"+strconv.Itoa(id)+"/fields/"+strconv.Itoa(fid), map[string]any{"value": block}, nil)
}

func (t *Itsaplan) patch(ctx context.Context, id int, in map[string]any) error {
	return t.rest.Do(ctx, http.MethodPatch, "/issues/"+strconv.Itoa(id), in, nil)
}

// ---- issues -----------------------------------------------------------------

func (t *Itsaplan) Create(ctx context.Context, title, body string, labels []string, milestone string) (IssueKey, error) {
	if milestone != "" {
		return "", errItsaplanNoMilestones()
	}
	m, err := t.projectMeta(ctx)
	if err != nil {
		return "", err
	}
	col := t.firstColumn(m, "unstarted", "backlog")
	if col == nil {
		return "", failed("It's a Plan project %s has no unstarted or backlog column to create an issue in", t.project)
	}
	ids, err := t.labelIDs(ctx, labels, true)
	if err != nil {
		return "", err
	}
	text, fields := splitBody(body)
	if fields.Block != "" {
		// fail before the issue exists when the field cannot be had
		if _, err := t.fieldID(ctx); err != nil {
			return "", err
		}
	}
	in := map[string]any{"title": title, "description": text, "columnId": col.ID}
	if len(ids) > 0 {
		in["labelIds"] = ids
	}
	var d itsaplanIssue
	if err := t.rest.Do(ctx, http.MethodPost, t.projectPath("/issues"), in, &d); err != nil {
		return "", err
	}
	key := IssueKey(d.Identifier)
	if key == "" {
		return "", failed("It's a Plan created an issue but named no key for it")
	}
	if fields.Block != "" {
		if err := t.putFields(ctx, d.ID, fields.Block); err != nil {
			if derr := t.rest.Do(ctx, http.MethodDelete, "/issues/"+strconv.Itoa(d.ID), nil, nil); derr != nil {
				return "", failed("%v; deleting the half-made issue %s also failed (%v): delete it in It's a Plan", err, key, derr)
			}
			return "", err
		}
	}
	t.mu.Lock()
	t.ids[key] = d.ID
	t.mu.Unlock()
	return key, nil
}

func (t *Itsaplan) Edit(ctx context.Context, key IssueKey, e IssueEdit) error {
	if e.Milestone != "" {
		return errItsaplanNoMilestones()
	}
	id, err := t.issueID(ctx, key)
	if err != nil {
		return err
	}
	in := map[string]any{}
	if e.Title != nil {
		in["title"] = *e.Title
	}
	var fields fieldsProp
	if e.Body != nil {
		var text string
		text, fields = splitBody(*e.Body)
		in["description"] = text
		if fields.Block != "" {
			// fail before any write when the field cannot be had, so a refused
			// key never leaves the new text behind without its block
			if _, err := t.fieldID(ctx); err != nil {
				return err
			}
		}
	}
	if len(in) > 0 {
		if err := t.patch(ctx, id, in); err != nil {
			return err
		}
	}
	if e.Body != nil {
		if err := t.putFields(ctx, id, fields.Block); err != nil {
			return err
		}
	}
	if len(uniq(e.AddLabels)) > 0 {
		if err := t.AddLabels(ctx, key, e.AddLabels, true); err != nil {
			return err
		}
	}
	if len(uniq(e.RemoveLabels)) > 0 {
		return t.RemoveLabels(ctx, key, e.RemoveLabels)
	}
	return nil
}

// ---- labels -----------------------------------------------------------------

// EnsureLabels creates the missing labels when autoCreate is set; without it a
// missing label is KindNotFound.
func (t *Itsaplan) EnsureLabels(ctx context.Context, names []string, autoCreate bool) error {
	_, err := t.labelIDs(ctx, names, autoCreate)
	return err
}

// AddLabels uses the bulk route, which only adds and ignores a label the issue
// already has, so two writers adding at once keep both labels.
func (t *Itsaplan) AddLabels(ctx context.Context, key IssueKey, labels []string, autoCreate bool) error {
	if len(uniq(labels)) == 0 {
		return nil
	}
	ids, err := t.labelIDs(ctx, labels, autoCreate)
	if err != nil {
		return err
	}
	id, err := t.issueID(ctx, key)
	if err != nil {
		return err
	}
	return t.rest.Do(ctx, http.MethodPost, t.projectPath("/issues/bulk/labels"), map[string]any{"ids": []int{id}, "add": ids}, nil)
}

// itsaplanLabelTries is how often RemoveLabels writes before it gives up on a
// concurrent writer undoing it.
const itsaplanLabelTries = 2

// RemoveLabels sends the issue's label set without the removed labels: the api
// replaces the whole set and offers no compare-and-swap. The issue is read
// just before the write and again after it, and the write is repeated once when
// another writer's PATCH overwrote it in between. A label another writer adds
// in the moment between the read and the write is still lost; AddLabels, which
// uses the bulk route, never loses one.
func (t *Itsaplan) RemoveLabels(ctx context.Context, key IssueKey, labels []string) error {
	if len(uniq(labels)) == 0 {
		return nil
	}
	// a label no one made is on no issue: only the names that exist are removed
	var drop []int
	for _, n := range uniq(labels) {
		ids, err := t.labelIDs(ctx, []string{n}, false)
		if IsKind(err, KindNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		drop = append(drop, ids...)
	}
	if len(drop) == 0 {
		return nil
	}
	id, err := t.issueID(ctx, key)
	if err != nil {
		return err
	}
	for try := 0; ; try++ {
		cur, err := t.issue(ctx, key)
		if err != nil {
			return err
		}
		if !slices.ContainsFunc(cur.LabelIDs, func(l int) bool { return slices.Contains(drop, l) }) {
			return nil
		}
		keep := []int{} // never nil: an empty set must be sent as [], which clears the labels
		for _, l := range cur.LabelIDs {
			if !slices.Contains(drop, l) {
				keep = append(keep, l)
			}
		}
		if err := t.patch(ctx, id, map[string]any{"labelIds": keep}); err != nil {
			return err
		}
		after, err := t.issue(ctx, key)
		if err != nil {
			return err
		}
		if sameLabels(after.LabelIDs, keep) {
			return nil
		}
		if try+1 >= itsaplanLabelTries {
			return failed("It's a Plan issue %s: another writer kept changing its labels while rota removed %v; run it again", key, labels)
		}
	}
}

func sameLabels(a, b []int) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

// ---- state ------------------------------------------------------------------

// firstColumn is the project's first column, by position, of the first type in
// want that it has; nil when it has none.
func (t *Itsaplan) firstColumn(m *itsaplanMeta, want ...string) *itsaplanColumn {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, typ := range want {
		var best *itsaplanColumn
		for i := range m.Columns {
			if c := &m.Columns[i]; c.StateType == typ && (best == nil || c.Position < best.Position) {
				best = c
			}
		}
		if best != nil {
			return best
		}
	}
	return nil
}

// moveTo puts the issue in the first column of the first type in want; an
// issue already in a column of any of them is left alone. Columns are told
// apart by stateType, never by name.
func (t *Itsaplan) moveTo(ctx context.Context, key IssueKey, want ...string) error {
	m, err := t.projectMeta(ctx)
	if err != nil {
		return err
	}
	d, err := t.issue(ctx, key)
	if err != nil {
		return err
	}
	if c := m.column(d.ColumnID); c != nil && slices.Contains(want, c.StateType) {
		return nil
	}
	col := t.firstColumn(m, want...)
	if col == nil {
		return failed("It's a Plan project %s has no %s column to move %s to", t.project, strings.Join(want, " or "), key)
	}
	return t.patch(ctx, d.ID, map[string]any{"columnId": col.ID})
}

// Close moves the issue to a completed column, or a canceled one for
// not_planned, then posts comment. The move comes first, so a retry after it
// failed posts no second comment.
func (t *Itsaplan) Close(ctx context.Context, key IssueKey, reason, comment string) error {
	want := "completed"
	if reason == CloseNotPlanned {
		want = "canceled"
	}
	if err := t.moveTo(ctx, key, want); err != nil {
		return err
	}
	if comment != "" {
		_, err := t.AddComment(ctx, key, comment)
		return err
	}
	return nil
}

// Reopen moves a closed issue to an unstarted column, else a started one.
func (t *Itsaplan) Reopen(ctx context.Context, key IssueKey) error {
	m, err := t.projectMeta(ctx)
	if err != nil {
		return err
	}
	d, err := t.issue(ctx, key)
	if err != nil {
		return err
	}
	if c := m.column(d.ColumnID); c == nil || (c.StateType != "completed" && c.StateType != "canceled") {
		return nil
	}
	return t.moveTo(ctx, key, "unstarted", "started")
}

// ---- comments ---------------------------------------------------------------

// A comment is stored as written, marker comments included: the api keeps an
// HTML comment in a comment body, which the feed returns as it was sent.

func (t *Itsaplan) AddComment(ctx context.Context, key IssueKey, body string) (string, error) {
	id, err := t.issueID(ctx, key)
	if err != nil {
		return "", err
	}
	var c struct {
		ID int `json:"id"`
	}
	if err := t.rest.Do(ctx, http.MethodPost, "/issues/"+strconv.Itoa(id)+"/comments", map[string]any{"body": body}, &c); err != nil {
		return "", err
	}
	if c.ID == 0 {
		return "", failed("It's a Plan created a comment on %s but named no id for it", key.Ref())
	}
	return strconv.Itoa(c.ID), nil
}

// commentPath is the route of comment id, once key is known to be an issue of
// this project and id a number.
func (t *Itsaplan) commentPath(key IssueKey, id string) (string, error) {
	if _, err := t.seq(key); err != nil {
		return "", err
	}
	if n, err := strconv.Atoi(id); err != nil || n < 1 {
		return "", &Error{Kind: KindNotFound, Code: 1, Message: fmt.Sprintf("not an It's a Plan comment id: %q", id)}
	}
	return "/comments/" + id, nil
}

func (t *Itsaplan) EditComment(ctx context.Context, key IssueKey, commentID, body string) error {
	path, err := t.commentPath(key, commentID)
	if err != nil {
		return err
	}
	return t.rest.Do(ctx, http.MethodPatch, path, map[string]any{"body": body}, nil)
}

func (t *Itsaplan) DeleteComment(ctx context.Context, key IssueKey, commentID string) error {
	path, err := t.commentPath(key, commentID)
	if err != nil {
		return err
	}
	return t.rest.Do(ctx, http.MethodDelete, path, nil, nil)
}

// CommentURL is empty: the api returns no web address for a comment, and its
// web origin is not the api's.
func (t *Itsaplan) CommentURL(ctx context.Context, key IssueKey, commentID string) (string, error) {
	_, err := t.commentPath(key, commentID)
	return "", err
}
