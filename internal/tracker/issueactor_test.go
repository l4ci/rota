package tracker

import (
	"context"
	"errors"
	"testing"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/proc"
)

// execOrigin answers every git call with origin ("" is no origin remote), and
// every forge CLI call with nothing.
func execOrigin(origin string) Option {
	return func(c *CLI) {
		WithExec(func(context.Context, proc.Cmd) (proc.Result, error) {
			return proc.Result{}, nil
		}, func(n string) (string, error) { return "/fake/" + n, nil })(c)
		WithGit(originGit(origin))(c)
	}
}

// originGit is a git seam that answers every call with origin ("" is no origin
// remote).
func originGit(origin string) git.Runner {
	return func(context.Context, string, ...string) (git.Result, error) {
		if origin == "" {
			return git.Result{Stderr: "fatal: no origin", ExitCode: 2}, nil
		}
		return git.Result{Stdout: origin + "\n"}, nil
	}
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

// resolveIssueSource is the one place an issues.source becomes a provider:
// every source crossed with each use, the origin kinds that tell them apart.
func TestResolveIssueSource(t *testing.T) {
	const gh, gl, unk = "https://github.com/o/r.git", "https://gitlab.com/o/r.git", "https://example.org/r.git"
	for _, c := range []struct {
		name, origin string
		s            Settings
		use          issueUse
		want, how    string // want "" is an error
		own          bool
	}{
		{"github outright", gl, Settings{Source: "github"}, useActor, "github", HowConfig, false},
		{"gitlab outright, no origin", "", Settings{Source: "gitlab"}, useReader, "gitlab", HowConfig, false},
		{"github outright for the tracker", gl, Settings{Source: "github"}, useTracker, "github", HowConfig, false},
		{"forge follows the origin", gl, Settings{Source: "forge"}, useActor, "gitlab", HowOrigin, false},
		{"forge.provider beats the origin", gh, Settings{Source: "forge", Provider: "gitlab"}, useReader, "gitlab", HowConfig, false},
		{"forge, no origin, is local", "", Settings{Source: "forge"}, useTracker, "local", HowNoOrigin, false},
		{"forge, unknown origin, actor refuses", unk, Settings{Source: "forge"}, useActor, "", "", false},
		{"forge, unknown origin, reader assumes", unk, Settings{Source: "forge"}, useReader, "github", HowAssumed, false},
		{"forge, unknown origin, tracker assumes", unk, Settings{Source: "forge"}, useTracker, "github", HowAssumed, false},
		{"legacy issues follows the origin", gl, Settings{Source: "issues"}, useTracker, "gitlab", "", false},
		{"legacy issues, forge.provider local, still the origin", gh, Settings{Source: "issues", Provider: "local"}, useTracker, "github", "", false},
		{"legacy issues, no origin, cannot tell", "", Settings{Source: "issues"}, useTracker, "", "", false},
		{"legacy issues as an actor is dir's forge", "", Settings{Source: "issues"}, useActor, "local", HowNoOrigin, false},
		{"legacy issues as a reader is dir's forge", unk, Settings{Source: "issues"}, useReader, "github", HowAssumed, false},
		{"unknown source as a reader is dir's forge", gl, Settings{Source: "svn"}, useReader, "gitlab", HowOrigin, false},
		{"unknown source as an actor is dir's forge", gh, Settings{Source: "svn"}, useActor, "github", HowOrigin, false},
		{"file as a reader is dir's forge", gh, Settings{Source: "file"}, useReader, "github", HowOrigin, false},
		{"file has no tracker", gh, Settings{Source: "file"}, useTracker, "", "", false},
		{"unknown source has no tracker", gh, Settings{Source: "svn"}, useTracker, "", "", false},
		{"jira names its own tracker", gh, Settings{Source: "jira"}, useActor, "", "", true},
		{"jira builds its own tracker", gh, Settings{Source: "jira"}, useTracker, "", "", true},
		{"jira as a reader is dir's forge", gh, Settings{Source: "jira"}, useReader, "github", HowOrigin, false},
	} {
		r, err := newCLI(c.s, "", execOrigin(c.origin)).resolveIssueSource(context.Background(), c.s, c.s.Source, c.use)
		switch {
		case c.own:
			if err != nil || r.own == nil {
				t.Errorf("%s: %+v, %v; want an own kind", c.name, r, err)
			}
		case c.want == "":
			if err == nil {
				t.Errorf("%s: %+v, want an error", c.name, r)
			}
		default:
			if err != nil || r.own != nil || r.Provider != c.want || r.How != c.how {
				t.Errorf("%s: %+v, %v; want %s via %q", c.name, r, err, c.want, c.how)
			}
		}
	}
}

// The zero IssueActor refuses instead of resolving from the origin.
func TestZeroIssueActorRefuses(t *testing.T) {
	if _, err := (IssueActor{}).Adapter(context.Background()); !IsKind(err, KindInternal) {
		t.Errorf("err %v, want KindInternal", err)
	}
}
