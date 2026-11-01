package harness

import "github.com/l4ci/rota/internal/config"

// AuthorSources holds known authorship and the inputs used to infer it. Slot
// is the slot holding this issue; Assigned is its latest recorded assignment
// when that slot has moved on. PriorSlot belongs to a different issue and is
// only a fallback for a new assignment, after the project default.
// Callers validate explicit picks and read the ledger/config at their seams.
type AuthorSources struct {
	Flag, Slot, Assigned, ResumeSlot, Project, PriorSlot string
	Labels                                               []string
}

// Sources reported by ResolveAuthor. The strings are also CLI output.
const (
	AuthorFromFlag       = "--kind"
	AuthorFromSlot       = "slot"
	AuthorFromResume     = "slot (resume)"
	AuthorFromAssignment = "assignment"
	AuthorFromLabel      = "harness label"
	AuthorFromProject    = config.RoundHarnessesKey
	AuthorFromDefault    = "default"
)

// ResolveAuthor resolves a worker's author harness: explicit flag, known slot,
// recorded assignment, first valid harness label, resumed slot, project default,
// prior slot, then Default. Project is the first round.harnesses entry.
// A resumed assignment supplies ResumeSlot; a new assignment supplies PriorSlot so
// an earlier quota fallback cannot override the next issue's project default.
func ResolveAuthor(s AuthorSources) (kind, source string) {
	for _, c := range []struct{ kind, source string }{
		{s.Flag, AuthorFromFlag},
		{s.Slot, AuthorFromSlot},
		{s.Assigned, AuthorFromAssignment},
		{FromLabels(s.Labels), AuthorFromLabel},
		{s.ResumeSlot, AuthorFromResume},
		{s.Project, AuthorFromProject},
		{s.PriorSlot, AuthorFromSlot},
	} {
		if c.kind != "" {
			return c.kind, c.source
		}
	}
	return Default, AuthorFromDefault
}
