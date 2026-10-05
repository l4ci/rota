// Package trackertest is an in-memory backlog.Tracker for tests: it keeps
// issues, comments and milestones, applies writes the way the forges do, and
// records every call so a test can compare the sequence with what the Python
// IssueBackend sends its adapter.
package trackertest

import (
	"context"
	"slices"
	"sort"
	"strconv"

	"github.com/l4ci/rota/internal/tracker"
)

// Call is one recorded tracker call. Method and Args use the Python adapter's
// names (get, list, add_comment, ...) and argument order, so a Go run and a
// Python run compare directly. Edit carries one map of its non-empty fields.
type Call struct {
	Method string `json:"method"`
	Args   []any  `json:"args"`
}

// Fake is the tracker. Fill Issues (and Milestones, the native milestone
// titles that exist) before use; Comments holds each issue's comments.
type Fake struct {
	Issues     []tracker.Issue
	Milestones []string
	Calls      []Call
	// Fail maps a Python-style method name to the error it returns.
	Fail map[string]error

	nextComment int
}

func (f *Fake) rec(method string, args ...any) error {
	if args == nil {
		args = []any{}
	}
	f.Calls = append(f.Calls, Call{method, args})
	return f.Fail[method]
}

func notFound(n int) error {
	return &tracker.Error{Kind: tracker.KindNotFound, Code: 1, Message: "issue #" + strconv.Itoa(n) + " not found"}
}

func (f *Fake) find(n int) (*tracker.Issue, error) {
	for i := range f.Issues {
		if f.Issues[i].Number == n {
			return &f.Issues[i], nil
		}
	}
	return nil, notFound(n)
}

func strs(s []string) []any {
	out := make([]any, len(s))
	for i, x := range s {
		out[i] = x
	}
	return out
}

// Get returns a copy of the issue; withComments is not recorded as a flag by
// the Python side (it never asks), so only the number is.
func (f *Fake) Get(_ context.Context, n int, withComments bool) (tracker.Issue, error) {
	if err := f.rec("get", n); err != nil {
		return tracker.Issue{}, err
	}
	is, err := f.find(n)
	if err != nil {
		return tracker.Issue{}, err
	}
	out := *is
	out.Labels = slices.Clone(is.Labels)
	out.Assignees = slices.Clone(is.Assignees)
	if !withComments {
		out.Comments = nil
	}
	return out, nil
}

// List returns the issues in the filter's state, newest first, as gh does.
func (f *Fake) List(_ context.Context, fl tracker.ListFilter) ([]tracker.Issue, error) {
	state := fl.State
	if state == "" {
		state = "open"
	}
	if err := f.rec("list", state); err != nil {
		return nil, err
	}
	var out []tracker.Issue
	for _, is := range f.Issues {
		if state == "all" || is.State == state {
			is.Comments = nil
			out = append(out, is)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Number > out[j].Number })
	return out, nil
}

// Create adds an open issue.
func (f *Fake) Create(_ context.Context, title, body string, labels []string, milestone string) (int, error) {
	if err := f.rec("create", title, body, strs(labels), milestone); err != nil {
		return 0, err
	}
	n := 1
	for _, is := range f.Issues {
		n = max(n, is.Number+1)
	}
	f.Issues = append(f.Issues, tracker.Issue{Number: n, Title: title, Body: body, Labels: slices.Clone(labels),
		Milestone: milestone, State: "open", URL: "https://example.test/issues/" + strconv.Itoa(n)})
	return n, nil
}

// Edit applies the non-empty fields of e.
func (f *Fake) Edit(_ context.Context, n int, e tracker.IssueEdit) error {
	m := map[string]any{}
	if e.Title != nil {
		m["title"] = *e.Title
	}
	if e.Body != nil {
		m["body"] = *e.Body
	}
	if len(e.AddLabels) > 0 {
		m["add_labels"] = strs(e.AddLabels)
	}
	if len(e.RemoveLabels) > 0 {
		m["remove_labels"] = strs(e.RemoveLabels)
	}
	if e.Milestone != "" {
		m["milestone"] = e.Milestone
	}
	if e.RemoveMilestone {
		m["remove_milestone"] = true
	}
	if err := f.rec("edit", n, m); err != nil {
		return err
	}
	is, err := f.find(n)
	if err != nil {
		return err
	}
	if e.Title != nil {
		is.Title = *e.Title
	}
	if e.Body != nil {
		is.Body = *e.Body
	}
	for _, l := range e.AddLabels {
		if !slices.Contains(is.Labels, l) {
			is.Labels = append(is.Labels, l)
		}
	}
	is.Labels = slices.DeleteFunc(is.Labels, func(l string) bool { return slices.Contains(e.RemoveLabels, l) })
	if e.Milestone != "" {
		is.Milestone = e.Milestone
	}
	if e.RemoveMilestone {
		is.Milestone = ""
	}
	return nil
}

// EnsureLabels records the call; labels are not modelled.
func (f *Fake) EnsureLabels(_ context.Context, names []string, autoCreate bool) error {
	return f.rec("ensure_labels", strs(names), autoCreate)
}

// AddLabels records the call and adds the labels.
func (f *Fake) AddLabels(_ context.Context, n int, labels []string, autoCreate bool) error {
	if err := f.rec("add_labels", n, strs(labels), autoCreate); err != nil {
		return err
	}
	is, err := f.find(n)
	if err != nil {
		return err
	}
	for _, l := range labels {
		if !slices.Contains(is.Labels, l) {
			is.Labels = append(is.Labels, l)
		}
	}
	return nil
}

// RemoveLabels records the call and removes the labels.
func (f *Fake) RemoveLabels(_ context.Context, n int, labels []string) error {
	if err := f.rec("remove_labels", n, strs(labels)); err != nil {
		return err
	}
	is, err := f.find(n)
	if err != nil {
		return err
	}
	is.Labels = slices.DeleteFunc(is.Labels, func(l string) bool { return slices.Contains(labels, l) })
	return nil
}

// Close closes the issue; an empty reason is "completed" and a comment is added.
func (f *Fake) Close(_ context.Context, n int, reason, comment string) error {
	if reason == "" {
		reason = "completed"
	}
	if err := f.rec("close", n, reason, comment); err != nil {
		return err
	}
	is, err := f.find(n)
	if err != nil {
		return err
	}
	is.State, is.StateReason, is.ClosedAt = "closed", reason, "2026-01-01T00:00:00Z"
	if comment != "" {
		f.add(is, comment)
	}
	return nil
}

// Reopen opens the issue again.
func (f *Fake) Reopen(_ context.Context, n int) error {
	if err := f.rec("reopen", n); err != nil {
		return err
	}
	is, err := f.find(n)
	if err != nil {
		return err
	}
	is.State, is.StateReason, is.ClosedAt = "open", "", ""
	return nil
}

// AssignSelf assigns the issue to "fake-user".
func (f *Fake) AssignSelf(_ context.Context, n int) error {
	if err := f.rec("assign_self", n); err != nil {
		return err
	}
	is, err := f.find(n)
	if err != nil {
		return err
	}
	if !slices.Contains(is.Assignees, "fake-user") {
		is.Assignees = append(is.Assignees, "fake-user")
	}
	return nil
}

func (f *Fake) add(is *tracker.Issue, body string) string {
	f.nextComment++
	id := strconv.Itoa(f.nextComment)
	is.Comments = append(is.Comments, tracker.Comment{ID: id, Body: body, Author: "fake-user"})
	return id
}

// Comments returns the issue's comments in order.
func (f *Fake) Comments(_ context.Context, n int) ([]tracker.Comment, error) {
	if err := f.rec("comments", n); err != nil {
		return nil, err
	}
	is, err := f.find(n)
	if err != nil {
		return nil, err
	}
	return slices.Clone(is.Comments), nil
}

// AddComment appends a comment and returns its id.
func (f *Fake) AddComment(_ context.Context, n int, body string) (string, error) {
	if err := f.rec("add_comment", n, body); err != nil {
		return "", err
	}
	is, err := f.find(n)
	if err != nil {
		return "", err
	}
	return f.add(is, body), nil
}

// EditComment replaces a comment's body.
func (f *Fake) EditComment(_ context.Context, n int, id, body string) error {
	if err := f.rec("edit_comment", n, id, body); err != nil {
		return err
	}
	is, err := f.find(n)
	if err != nil {
		return err
	}
	for i := range is.Comments {
		if is.Comments[i].ID == id {
			is.Comments[i].Body = body
			return nil
		}
	}
	return &tracker.Error{Kind: tracker.KindNotFound, Code: 1, Message: "comment " + id + " not found"}
}

// DeleteComment removes a comment.
func (f *Fake) DeleteComment(_ context.Context, n int, id string) error {
	if err := f.rec("delete_comment", n, id); err != nil {
		return err
	}
	is, err := f.find(n)
	if err != nil {
		return err
	}
	is.Comments = slices.DeleteFunc(is.Comments, func(c tracker.Comment) bool { return c.ID == id })
	return nil
}

// FindMilestone returns the first milestone title that starts with rotaID.
func (f *Fake) FindMilestone(_ context.Context, rotaID string) (string, bool, error) {
	if err := f.rec("find_milestone", rotaID); err != nil {
		return "", false, err
	}
	for _, t := range f.Milestones {
		if t == rotaID || (len(t) > len(rotaID) && t[:len(rotaID)] == rotaID && (t[len(rotaID)] == ' ' || t[len(rotaID)] == '\t')) {
			return t, true, nil
		}
	}
	return "", false, nil
}

// CreateMilestone adds a native milestone, as the forges do; the number is its
// position.
func (f *Fake) CreateMilestone(_ context.Context, title, description string) (int, error) {
	if err := f.rec("create_milestone", title, description); err != nil {
		return 0, err
	}
	f.Milestones = append(f.Milestones, title)
	return len(f.Milestones), nil
}
