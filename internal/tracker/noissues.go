package tracker

import "context"

// NoIssues is the issue tracker of issues.source file: the issues live in
// BACKLOG.md, so reads find nothing and writes are refused with
// ErrNoIssueTracker. A caller that works with or without a tracker holds it
// instead of a nil.
type NoIssues struct{}

var _ IssueTracker = NoIssues{}

func (NoIssues) Provider() string                { return "file" }
func (NoIssues) CheckAuth(context.Context) error { return ErrNoIssueTracker }
func (NoIssues) Create(context.Context, string, string, []string, string) (IssueKey, error) {
	return "", ErrNoIssueTracker
}
func (NoIssues) Get(context.Context, IssueKey, bool) (Issue, error) {
	return Issue{}, &Error{Kind: KindNotFound, Code: 3, Message: "issues.source is file: there is no tracker issue to read"}
}
func (NoIssues) List(context.Context, ListFilter) ([]Issue, error)         { return nil, nil }
func (NoIssues) Edit(context.Context, IssueKey, IssueEdit) error           { return ErrNoIssueTracker }
func (NoIssues) EnsureLabels(context.Context, []string, bool) error        { return ErrNoIssueTracker }
func (NoIssues) AddLabels(context.Context, IssueKey, []string, bool) error { return ErrNoIssueTracker }
func (NoIssues) RemoveLabels(context.Context, IssueKey, []string) error    { return ErrNoIssueTracker }
func (NoIssues) Close(context.Context, IssueKey, string, string) error     { return ErrNoIssueTracker }
func (NoIssues) Reopen(context.Context, IssueKey) error                    { return ErrNoIssueTracker }
func (NoIssues) AssignSelf(context.Context, IssueKey) error                { return ErrNoIssueTracker }
func (NoIssues) Comments(context.Context, IssueKey) ([]Comment, error)     { return nil, nil }
func (NoIssues) AddComment(context.Context, IssueKey, string) (string, error) {
	return "", ErrNoIssueTracker
}
func (NoIssues) EditComment(context.Context, IssueKey, string, string) error {
	return ErrNoIssueTracker
}
func (NoIssues) DeleteComment(context.Context, IssueKey, string) error { return ErrNoIssueTracker }
func (NoIssues) CommentURL(context.Context, IssueKey, string) (string, error) {
	return "", ErrNoIssueTracker
}
func (NoIssues) FindMilestone(context.Context, string) (string, bool, error) { return "", false, nil }
func (NoIssues) ClosesIssue(IssueKey, ClosingSource) bool                    { return false }
func (NoIssues) Milestones(context.Context, string) ([]Milestone, error)     { return nil, nil }
func (NoIssues) CreateMilestone(context.Context, string, string) (int, error) {
	return 0, ErrNoIssueTracker
}
func (NoIssues) EditMilestone(context.Context, int, MilestoneEdit) error { return ErrNoIssueTracker }
func (NoIssues) IssuesInMilestone(context.Context, string, string) ([]Issue, error) {
	return nil, nil
}
