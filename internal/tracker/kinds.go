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
	// keyName is the config key naming the project or team the kind reads and
	// writes (issues.<source>.<project|team>); field is where Settings keeps it.
	keyName string
	field   func(*Settings) *string
	// connectArgs is what doctor's "rota connect add" hint needs after --kind;
	// probeHint is what doctor tells when the key cannot be read through the
	// connection, "" when the kind has no such probe.
	connectArgs string
	probeHint   string
	// build never returns a nil pointer inside a non-nil IssueTracker.
	build  func(context.Context, *CLI, Settings) (IssueTracker, error)
	tester connections.Tester
}

// ownKinds is the registry of trackers that name themselves in issues.source.
var ownKinds = []ownKind{
	{config.SourceJira, jiraSpec, "issues.jira.project", func(s *Settings) *string { return &s.JiraProject }, " ...", "", func(ctx context.Context, c *CLI, s Settings) (IssueTracker, error) {
		t, err := newJiraTracker(ctx, c, s)
		if err != nil {
			return nil, err
		}
		return t, nil
	}, JiraTester{}},
	{config.SourceLinear, linearSpec, "issues.linear.team", func(s *Settings) *string { return &s.LinearTeam }, " --token-stdin", "check issues.linear.team against the team's key in Linear, and the API key's access", func(ctx context.Context, c *CLI, s Settings) (IssueTracker, error) {
		t, err := newLinearTracker(ctx, c, s)
		if err != nil {
			return nil, err
		}
		return t, nil
	}, LinearTester{}},
	{config.SourceItsaplan, itsaplanSpec, "issues.itsaplan.project", func(s *Settings) *string { return &s.ItsaplanProject }, " --site <url> --token-stdin", "check issues.itsaplan.project against the project's key in It's a Plan, and the API key's access", func(ctx context.Context, c *CLI, s Settings) (IssueTracker, error) {
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

// KindInfo is what doctor and the config checks read of a registered kind.
type KindInfo struct {
	// Source is the issues.source value; ConnectionKind the connection kind.
	Source, ConnectionKind string
	// KeyName is the config key naming the project or team; Key reads it.
	KeyName     string
	Key         func(Settings) string
	ConnectArgs string
	ProbeHint   string
}

// OwnKinds lists every registered own-tracker kind, in registry order.
func OwnKinds() []KindInfo {
	out := make([]KindInfo, 0, len(ownKinds))
	for _, k := range ownKinds {
		field := k.field
		out = append(out, KindInfo{
			Source: k.source, ConnectionKind: k.spec.kind, KeyName: k.keyName,
			Key:         func(s Settings) string { return *field(&s) },
			ConnectArgs: k.connectArgs, ProbeHint: k.probeHint,
		})
	}
	return out
}
