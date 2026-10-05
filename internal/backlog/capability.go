package backlog

import ms "github.com/l4ci/rota/internal/milestone"

// IssueBackend is what the issue-only verbs need of a tracker-backed backend.
// The single-repo *Issues and the *Umbrella both implement it, so callers ask
// for a capability by method instead of switching on the concrete type.
type IssueBackend interface {
	Backend
	ReviewQueue() ([]QueueEntry, error)
	MergePRGated(pr int, items []string, approve MergeApprover) (MergeResult, error)
	ReleaseGate(mid string) ([]Blocker, []Issue, error)
	ReleaseNotes(mid string) ([]NoteSection, error)
	ReleaseClose(mid, tag string) (int, bool, error)
	// MilestoneStore is the backend's milestone store.
	MilestoneStore() ms.Store
	// SetWarn routes notices (duplicate tracking issues) to warn.
	SetWarn(warn func(string))
	// HomeIssues is the Issues backend that holds slice plans and milestone
	// plans: the backend itself, or an umbrella's home sub-repo.
	HomeIssues() (*Issues, error)
}

var (
	_ IssueBackend = (*Issues)(nil)
	_ IssueBackend = (*Umbrella)(nil)
)

// SetWarn sets the notice sink.
func (b *Issues) SetWarn(warn func(string)) { b.Warn = warn }

// HomeIssues is b itself.
func (b *Issues) HomeIssues() (*Issues, error) { return b, nil }

// SetWarn sets the notice sink on the home sub-repo, where the milestone
// notices arise. A home that cannot be resolved is left for the verb to hit.
func (u *Umbrella) SetWarn(warn func(string)) {
	if home, err := u.homeSub(); err == nil {
		home.Warn = warn
	}
}

// HomeIssues is the home sub-repo.
func (u *Umbrella) HomeIssues() (*Issues, error) { return u.homeSub() }
