package tracker

import (
	"context"

	"github.com/l4ci/rota/internal/config"
)

// issueUse is who asks which provider an issues.source names. It decides what
// the source rules do where they differ: whether a source naming its own
// tracker is built (actor, tracker) or only its forge asked (reader), whether
// an origin naming no forge is refused (actor) or assumed github (reader,
// tracker), and whether file, the legacy issues source and an unknown source
// are refused or read as dir's forge (tracker refuses or detects, the others
// ask the forge).
type issueUse int

const (
	useActor   issueUse = iota // upstream writes: ResolveIssueActor
	useReader                  // the forge of the issues: ResolveIssueForge
	useTracker                 // the tracker adapter: NewIssueTracker
)

// issueSource is the decision for one issues.source: the tracker kind it
// names itself (own), or the forge provider that serves it, which can be the
// local forge.
type issueSource struct {
	own *ownKind
	Resolution
}

// resolveIssueSource is the one place that maps an issues.source to a
// provider. A source in ownKinds names its own tracker, github and gitlab name
// the forge outright, forge is dir's forge as use resolves it, and the rest
// follow use (see issueUse).
func (c *CLI) resolveIssueSource(ctx context.Context, s Settings, source string, use issueUse) (issueSource, error) {
	if use != useReader {
		if k, ok := ownKindFor(source); ok {
			return issueSource{own: &k}, nil
		}
	}
	if source == config.SourceGitHub || source == config.SourceGitLab {
		return issueSource{Resolution: Resolution{Provider: source, How: HowConfig}}, nil
	}
	kind := ForgeActor
	if use != useActor {
		kind = ForgeReader
	}
	if use == useTracker {
		switch source {
		case config.SourceFile, "":
			return issueSource{}, ErrNoIssueTracker
		case config.SourceIssues:
			// An unmigrated "issues backend" with no named provider: the
			// origin names it, as the old issues.provider auto did.
			p, err := c.resolveIssues(ctx, config.ForgeAuto, s.Provider)
			return issueSource{Resolution: Resolution{Provider: p}}, err
		case config.SourceForge:
		default:
			return issueSource{}, Unavailable("unknown issues.source %q", source)
		}
	}
	r, err := c.resolveForge(ctx, kind, config.ForgeAuto, s.Provider)
	return issueSource{Resolution: r}, err
}

// ResolveIssueForge is the forge provider of the issues in dir as a reader
// resolves it: an origin naming no forge reads as github, and a local forge is
// the answer ProviderLocal, not an error.
func ResolveIssueForge(ctx context.Context, s Settings, dir string, opts ...Option) (Resolution, error) {
	r, err := newCLI(s, dir, opts...).resolveIssueSource(ctx, s, s.Source, useReader)
	return r.Resolution, err
}
