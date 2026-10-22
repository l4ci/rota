// Package issues is the upstream-issue side of rota: provider detection, list,
// label, close and the open-only filter of the cross-reference index. It
// ports bin/hv-issues-{provider,list,label,close,imported}; every forge call
// here goes through tracker.Adapter, so what differs between gh and glab
// lives in one place. With issues.source jira or linear, label and close go
// through that tracker instead.
package issues

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/marker"
	"github.com/l4ci/rota/internal/pystr"
	"github.com/l4ci/rota/internal/tracker"
)

// ErrCommitNotFound: the commit is not in the repository (exit 3).
var ErrCommitNotFound = errors.New("commit not found")

// ErrNoProvider: neither issues.source, the origin remote nor forge.provider
// names a tracker host (exit 3).
var ErrNoProvider = errors.New("no issue provider")

// Env is what every call runs with.
type Env struct {
	// Config is the loaded .rota/config.json (nil reads the defaults).
	Config any
	// Opts reach every forge CLI the calls build (tests swap the executor).
	Opts []tracker.Option
	// Forge, when set, replaces the adapter built from the provider; tests
	// use it to stand in a key-based tracker.
	Forge func(ctx context.Context, provider, dir string) (Forge, error)
}

// Forge is the part of tracker.Adapter the verbs call.
type Forge interface {
	CheckAuth(ctx context.Context) error
	Get(ctx context.Context, key tracker.IssueKey, withComments bool) (tracker.Issue, error)
	AddLabels(ctx context.Context, key tracker.IssueKey, labels []string, autoCreate bool) error
	RemoveLabels(ctx context.Context, key tracker.IssueKey, labels []string) error
	Close(ctx context.Context, key tracker.IssueKey, reason, comment string) error
}

// Provider is the issue tracker's provider for dir ("" is the process cwd).
// An issues.source of github or gitlab names it outright; any other source
// takes dir's forge as kind resolves it (tracker.ResolveProvider), which can be
// the local forge.
func Provider(ctx context.Context, env Env, kind tracker.ForgeKind, dir string) (tracker.Resolution, error) {
	s := tracker.SettingsFromConfig(env.Config)
	if s.Source == "github" || s.Source == "gitlab" {
		return tracker.Resolution{Provider: s.Source, How: tracker.HowConfig}, nil
	}
	return tracker.ResolveProvider(ctx, kind, "", s.Provider, dir, execOf(env))
}

func noProvider(verb string) error {
	return fmt.Errorf("%w: cannot %s (issues.source does not name a tracker, no origin remote names github or gitlab, and forge.provider is not set)", ErrNoProvider, verb)
}

// execOf is the executor the env's options install, or nil for the real one.
func execOf(env Env) tracker.Exec {
	c := &tracker.CLI{}
	for _, o := range env.Opts {
		o(c)
	}
	return c.Exec
}

func unavailable(format string, a ...any) *tracker.Error {
	return &tracker.Error{Kind: tracker.KindUnavailable, Code: 3, Message: fmt.Sprintf(format, a...)}
}

// adapter is the forge adapter for provider, whose CLI runs in dir.
func (e Env) adapter(ctx context.Context, provider, dir string) (Forge, error) {
	if e.Forge != nil {
		return e.Forge(ctx, provider, dir)
	}
	return tracker.NewFromConfig(ctx, e.Config, provider, dir, e.Opts...)
}

// tracker is what label and close act on: issues.source's own tracker when it
// is not a forge (jira, linear, itsaplan), else the forge adapter of the resolved provider.
func (e Env) tracker(ctx context.Context, verb, dir string) (Forge, error) {
	if s := tracker.SettingsFromConfig(e.Config); s.Source == config.SourceJira || s.Source == config.SourceLinear || s.Source == config.SourceItsaplan {
		if e.Forge != nil {
			return e.Forge(ctx, s.Source, dir)
		}
		return tracker.NewIssueTracker(ctx, s, "", dir, e.Opts...)
	}
	r, err := Provider(ctx, e, tracker.ForgeActor, dir)
	if err != nil || r.Provider == tracker.ProviderLocal {
		return nil, noProvider(verb)
	}
	return e.adapter(ctx, r.Provider, dir)
}

// ---- label ----------------------------------------------------------------

// Label adds or removes one label on an upstream issue and reports whether it
// changed anything. A label already there (or already absent) is a no-op:
// changed is read from the issue first, and a failed read leaves it true.
// autoCreate lets a missing label be created on the forge.
func Label(ctx context.Context, env Env, dir string, key tracker.IssueKey, label string, add, autoCreate bool) (bool, error) {
	a, err := env.tracker(ctx, "label upstream issue", dir)
	if err != nil {
		return false, err
	}
	changed := true
	if is, err := a.Get(ctx, key, false); err == nil {
		changed = slices.Contains(is.Labels, label) != add
	}
	if add {
		err = a.AddLabels(ctx, key, []string{label}, autoCreate)
	} else {
		err = a.RemoveLabels(ctx, key, []string{label})
	}
	if err != nil {
		return false, err
	}
	return changed, nil
}

// ---- close ----------------------------------------------------------------

// Close closes one upstream issue with a comment naming the commit that
// shipped it. changed is false when the issue was already closed. dir is the
// checkout the commit lives in.
func Close(ctx context.Context, env Env, dir string, key tracker.IssueKey, commit, item string) (bool, error) {
	short, err := shortSHA(dir, commit)
	if err != nil {
		return false, err
	}
	body := "Closed by rota: shipped in " + short
	if item != "" {
		body += " ([" + item + "])"
	}
	body += "\n\n" + marker.Line("shipped")
	a, err := env.tracker(ctx, "close upstream issue", dir)
	if err != nil {
		return false, err
	}
	if err := a.CheckAuth(ctx); err != nil {
		return false, err
	}
	// A failed state read counts as "not closed".
	if is, err := a.Get(ctx, key, false); err == nil && is.State == "closed" {
		return false, nil
	}
	if err := a.Close(ctx, key, tracker.CloseCompleted, body); err != nil {
		return false, err
	}
	return true, nil
}

// shortSHA is `git rev-parse --short <commit>` in dir.
func shortSHA(dir, commit string) (string, error) {
	notFound := fmt.Errorf("%w: commit '%s' not found in current repository", ErrCommitNotFound, commit)
	if strings.HasPrefix(commit, "-") {
		return "", notFound
	}
	res, err := git.Repo{Dir: dir}.Run(context.Background(), "rev-parse", "--short", commit)
	if errors.Is(err, git.ErrNoGit) {
		return "", unavailable("git is not installed")
	}
	if err != nil || res.ExitCode != 0 {
		return "", notFound
	}
	return pystr.Strip(res.Stdout), nil
}

// ---- imported, open-only ----------------------------------------------------------

// StillOpen reports whether the upstream issue of a cross-reference is open:
// provider says which forge, dir is the checkout of the entry's repo. An issue
// whose state cannot be read (no CLI, no auth, deleted) is not open.
func StillOpen(ctx context.Context, env Env, provider, dir string, key tracker.IssueKey) bool {
	a, err := env.adapter(ctx, provider, dir)
	if err != nil {
		return false
	}
	is, err := a.Get(ctx, key, false)
	return err == nil && is.State == "open"
}
