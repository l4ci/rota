package tracker

import (
	"context"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/connections"
)

// ownKind is one tracker of its own (not a forge): the issues.source value
// that names it, the connection kind and `connect add` flags it reaches its
// site through, its constructor and its connection test read. A kind is
// declared here and nowhere else: adding one is one entry in ownKinds.
type ownKind struct {
	source string
	spec   restSpec
	// build never returns a nil pointer inside a non-nil IssueTracker.
	build  func(context.Context, *CLI, Settings) (IssueTracker, error)
	tester connections.Tester
}

// ownKinds is the registry of trackers that name themselves in issues.source.
var ownKinds = []ownKind{
	{config.SourceJira, jiraSpec, func(ctx context.Context, c *CLI, s Settings) (IssueTracker, error) {
		t, err := newJiraTracker(ctx, c, s)
		if err != nil {
			return nil, err
		}
		return t, nil
	}, JiraTester{}},
	{config.SourceLinear, linearSpec, func(ctx context.Context, c *CLI, s Settings) (IssueTracker, error) {
		t, err := newLinearTracker(ctx, c, s)
		if err != nil {
			return nil, err
		}
		return t, nil
	}, LinearTester{}},
	{config.SourceItsaplan, itsaplanSpec, func(ctx context.Context, c *CLI, s Settings) (IssueTracker, error) {
		t, err := newItsaplanTracker(ctx, c, s)
		if err != nil {
			return nil, err
		}
		return t, nil
	}, ItsaplanTester{}},
}

// ownKindFor returns the registered kind whose issues.source is source.
func ownKindFor(source string) (ownKind, bool) {
	for _, k := range ownKinds {
		if k.source == source {
			return k, true
		}
	}
	return ownKind{}, false
}

// ConnectionTesters is the connection test read of every registered kind,
// keyed by connection kind: what `rota connect test` and doctor run.
func ConnectionTesters() map[string]connections.Tester {
	m := make(map[string]connections.Tester, len(ownKinds))
	for _, k := range ownKinds {
		m[k.spec.kind] = k.tester
	}
	return m
}
