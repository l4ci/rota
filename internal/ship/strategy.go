package ship

import (
	"errors"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/tracker"
)

// Landing is how a finished branch lands. Strategy is direct, pr, or unset
// (nothing decided it); Reason says what did.
type Landing struct {
	Strategy string
	Reason   string
}

// ErrTrackerNeedsLocalForge is the one case Strategy cannot serve: issues live
// on a tracker, which closes them through a PR, but the forge is local.
var ErrTrackerNeedsLocalForge = errors.New("issues are on a tracker, which needs a PR, but the forge is local (no origin remote, or forge.provider local): set forge.provider or an origin remote")

// Strategy owns the landing rule; the rota ship strategy verb only renders it.
//
//   - issues on a tracker (Capabilities.IssueIDs): pr, always;
//   - a local forge: direct, whatever configured says, since there is no PR to open;
//   - otherwise configured (work.mergeStrategy): direct, pr, or unset.
//
// caps are the backlog backend's; forge a tracker.Provider* value.
func Strategy(caps backlog.Capabilities, forge, configured string) (Landing, error) {
	switch {
	case caps.IssueIDs && forge == tracker.ProviderLocal:
		return Landing{}, ErrTrackerNeedsLocalForge
	case caps.IssueIDs:
		return Landing{"pr", "issues.source is a tracker: the PR closes the issues"}, nil
	case forge == tracker.ProviderLocal:
		return Landing{"direct", "the forge is local: there are no pull requests"}, nil
	case configured == "":
		return Landing{"unset", "work.mergeStrategy"}, nil
	}
	return Landing{configured, "work.mergeStrategy"}, nil
}
