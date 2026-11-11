package tracker

import "context"

// NoIssues is the issue tracker of issues.source file: the issues live in
// BACKLOG.md, so reads find nothing and writes are refused with
// ErrNoIssueTracker. A caller that works with or without a tracker holds it
// instead of a nil. Its milestone half is the shared noMilestones one, so
// "this tracker has none" is the same IsNoMilestones refusal as on Linear.
type NoIssues struct{ noMilestones }

// NewNoIssues is the absent-tracker adapter of issues.source file.
func NewNoIssues() NoIssues {
	return NoIssues{noMilestones{provider: "file", detail: ": the backlog lives in BACKLOG.md"}}
}

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
func (NoIssues) ClosesIssue(IssueKey, ClosingSource) bool { return false }
