package tracker

import "context"

// The optional trust capabilities (#899). A tracker or forge that can say who
// wrote an issue body, who may push and who the authenticated user is
// implements these; callers take the interface instead of declaring their own
// and asserting per call. Adapters without a capability do not implement it,
// and the caller refuses with its own text.

// Authorship is who wrote an issue body and the body they wrote, read in one
// call so the trust check judges the text that is then used.
type Authorship struct {
	// Author and Editor are logins; "" for a deleted account, and for Editor an
	// issue never edited.
	Author, Editor string
	// Edited says the body was edited at all, so an edit by a deleted account
	// (no Editor) is not mistaken for none.
	Edited bool
	Body   string
}

// PushTrust says whether a login holds write access on the repository.
// Implemented by GitHub only.
type PushTrust interface {
	CanPush(ctx context.Context, login string) (bool, error)
}

// IssueTrust is the part of an issue tracker that says who wrote an issue body
// and whether they may push: the behaviour run only executes steps a
// collaborator wrote. IssueAuthorship returns the body with the authors, so the
// steps that run are the text that was vouched for. Implemented by GitHub only.
type IssueTrust interface {
	PushTrust
	IssueAuthorship(ctx context.Context, key IssueKey) (Authorship, error)
}

// Identity resolves the authenticated user. Implemented by GitHub and GitLab.
type Identity interface {
	AuthenticatedUser(ctx context.Context) (string, error)
}

var (
	_ IssueTrust = (*GitHub)(nil)
	_ Identity   = (*GitHub)(nil)
	_ Identity   = (*GitLab)(nil)
)
