package tracker

import (
	"context"
	"errors"
	"testing"

	"github.com/l4ci/rota/internal/config"
)

// execOrigin answers every git call with origin ("" is no origin remote).
func execOrigin(origin string) Option {
	return WithExec(func(_ context.Context, _ string, name string, _ []string, _ []byte) ([]byte, []byte, int, error) {
		if name == "git" {
			if origin == "" {
				return nil, []byte("fatal: no origin"), 2, nil
			}
			return []byte(origin + "\n"), nil, 0, nil
		}
		return nil, nil, 0, nil
	}, func(n string) (string, error) { return "/fake/" + n, nil })
}

// The one source-to-tracker decision: named sources outright, anything else
// the forge of dir, a local forge no tracker.
func TestResolveIssueActor(t *testing.T) {
	const gh, gl = "https://github.com/o/r.git", "https://gitlab.com/o/r.git"
	for _, c := range []struct {
		name, origin string
		s            Settings
		want         string
		noActor      bool
	}{
		{"github names itself", gl, Settings{Source: "github"}, "github", false},
		{"gitlab names itself", gh, Settings{Source: "gitlab"}, "gitlab", false},
		{"jira beats the origin", gh, Settings{Source: config.SourceJira}, "jira", false},
		{"linear beats the origin", gh, Settings{Source: config.SourceLinear}, "linear", false},
		{"itsaplan beats the origin", gh, Settings{Source: config.SourceItsaplan}, "itsaplan", false},
		{"forge follows the origin", gl, Settings{Source: config.SourceForge}, "gitlab", false},
		{"forge.provider beats the origin", gh, Settings{Source: config.SourceForge, Provider: "gitlab"}, "gitlab", false},
		{"no origin is local", "", Settings{Source: config.SourceForge}, "", true},
	} {
		a, err := ResolveIssueActor(context.Background(), c.s, "", execOrigin(c.origin))
		if c.noActor {
			if !errors.Is(err, ErrNoIssueActor) {
				t.Errorf("%s: err %v, want ErrNoIssueActor", c.name, err)
			}
			continue
		}
		if err != nil || a.Source != c.want {
			t.Errorf("%s: %q, %v; want %q", c.name, a.Source, err, c.want)
		}
	}
}

// IssueProvider is the one rule issues adds to the forge resolver: an
// issues.source of github or gitlab names the provider outright, whatever the
// forge; any other source is dir's forge, as the kind resolves it.
func TestIssueProvider(t *testing.T) {
	const gh = "https://github.com/o/r.git"
	for _, c := range []struct {
		origin string
		s      Settings
		kind   ForgeKind
		want   string
	}{
		{gh, Settings{Source: "gitlab"}, ForgeActor, "gitlab"},
		{"", Settings{Source: "github"}, ForgeActor, "github"},
		{gh, Settings{Source: "file"}, ForgeActor, "github"},
		{"", Settings{Source: "jira"}, ForgeActor, "local"},
		{gh, Settings{Source: "forge", Provider: "gitlab"}, ForgeActor, "gitlab"}, // forge.provider beats the origin
		{"https://example.org/r.git", Settings{Source: "forge"}, ForgeReader, "github"},
		{"https://example.org/r.git", Settings{Source: "forge"}, ForgeActor, ""},
	} {
		r, err := IssueProvider(context.Background(), c.s, c.kind, "", execOrigin(c.origin))
		if r.Provider != c.want || (err != nil) != (c.want == "") {
			t.Errorf("origin %q %+v kind %d: %+v, %v; want %q", c.origin, c.s, c.kind, r, err, c.want)
		}
	}
}

// The zero IssueActor refuses instead of resolving from the origin.
func TestZeroIssueActorRefuses(t *testing.T) {
	if _, err := (IssueActor{}).Adapter(context.Background()); !IsKind(err, KindInternal) {
		t.Errorf("err %v, want KindInternal", err)
	}
}
