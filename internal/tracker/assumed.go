package tracker

import "context"

// assumedForge is the issue tracker of issues.source forge on an origin that
// names no forge: ResolveProvider's reader answer, github. Reads go through;
// every write is the actor's refusal, an *UnknownForgeError, before the CLI
// runs, so nothing lands on a forge rota only guessed.
type assumedForge struct {
	IssueTracker
	r Resolution
}

// AssumedNote is the line a verb prints when it reads it on an assumed github
// (Resolution.Note), "" for any other tracker.
func AssumedNote(it IssueTracker) string {
	if a, ok := it.(assumedForge); ok {
		return a.r.Note()
	}
	return ""
}

func (a assumedForge) refuse() error { return &UnknownForgeError{Origin: a.r.Origin} }

func (a assumedForge) Create(context.Context, string, string, []string, string) (IssueKey, error) {
	return "", a.refuse()
}
func (a assumedForge) Edit(context.Context, IssueKey, IssueEdit) error    { return a.refuse() }
func (a assumedForge) EnsureLabels(context.Context, []string, bool) error { return a.refuse() }
func (a assumedForge) AddLabels(context.Context, IssueKey, []string, bool) error {
	return a.refuse()
}
func (a assumedForge) RemoveLabels(context.Context, IssueKey, []string) error { return a.refuse() }
func (a assumedForge) Close(context.Context, IssueKey, string, string) error  { return a.refuse() }
func (a assumedForge) Reopen(context.Context, IssueKey) error                 { return a.refuse() }
func (a assumedForge) AssignSelf(context.Context, IssueKey) error             { return a.refuse() }
func (a assumedForge) AddComment(context.Context, IssueKey, string) (string, error) {
	return "", a.refuse()
}
func (a assumedForge) EditComment(context.Context, IssueKey, string, string) error {
	return a.refuse()
}
func (a assumedForge) DeleteComment(context.Context, IssueKey, string) error { return a.refuse() }
func (a assumedForge) CreateMilestone(context.Context, string, string) (int, error) {
	return 0, a.refuse()
}
func (a assumedForge) EditMilestone(context.Context, int, MilestoneEdit) error { return a.refuse() }
