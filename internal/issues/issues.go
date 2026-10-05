// Package issues is the upstream-issue side of rota: provider detection, list,
// label, close and the open-only filter of the cross-reference index. It
// ports bin/hv-issues-{provider,list,label,close,imported}; every forge call
// here goes through tracker.Adapter, so what differs between gh and glab
// lives in one place.
package issues

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/marker"
	"github.com/l4ci/rota/internal/pystr"
	"github.com/l4ci/rota/internal/tracker"
)

// ErrCommitNotFound: the commit is not in the repository (exit 3).
var ErrCommitNotFound = errors.New("commit not found")

// ErrNoProvider: neither the origin remote nor issues.provider names a forge
// (exit 3).
var ErrNoProvider = errors.New("no issue provider")

// Env is what every call runs with.
type Env struct {
	// Config is the loaded .rota/config.json (nil reads the defaults).
	Config any
	// Opts reach every forge CLI the calls build (tests swap the executor).
	Opts []tracker.Option
}

// Provider is "github", "gitlab" or "unknown" for dir ("" is the process
// cwd). The origin host decides, as in hv-issues-provider; issues.provider
// is only the fallback when origin is missing or names no forge.
func Provider(ctx context.Context, env Env, dir string) string {
	origin := tracker.OriginURL(ctx, dir, execOf(env))
	return tracker.ProviderFromOrigin(origin, tracker.SettingsFromConfig(env.Config).Provider)
}

func noProvider(verb string) error {
	return fmt.Errorf("%w: cannot %s (no origin remote naming github or gitlab, and issues.provider is not set)", ErrNoProvider, verb)
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
func (e Env) adapter(ctx context.Context, provider, dir string) (tracker.Adapter, error) {
	return tracker.NewFromConfig(ctx, e.Config, provider, dir, e.Opts...)
}

// ---- label ----------------------------------------------------------------

// Label adds or removes one label on an upstream issue and reports whether it
// changed anything. A label already there (or already absent) is a no-op:
// changed is read from the issue first, and a failed read leaves it true.
// autoCreate lets a missing label be created on the forge.
func Label(ctx context.Context, env Env, dir string, number int, label string, add, autoCreate bool) (bool, error) {
	provider := Provider(ctx, env, dir)
	if provider == "unknown" {
		return false, noProvider("label upstream issue")
	}
	a, err := env.adapter(ctx, provider, dir)
	if err != nil {
		return false, err
	}
	changed := true
	if is, err := a.Get(ctx, number, false); err == nil {
		changed = slices.Contains(is.Labels, label) != add
	}
	if add {
		err = a.AddLabels(ctx, number, []string{label}, autoCreate)
	} else {
		err = a.RemoveLabels(ctx, number, []string{label})
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
func Close(ctx context.Context, env Env, dir string, number int, commit, item string) (bool, error) {
	short, err := shortSHA(dir, commit)
	if err != nil {
		return false, err
	}
	body := "Closed by rota: shipped in " + short
	if item != "" {
		body += " ([" + item + "])"
	}
	body += "\n\n" + marker.Line("shipped")
	provider := Provider(ctx, env, dir)
	if provider == "unknown" {
		return false, noProvider("close upstream issue")
	}
	a, err := env.adapter(ctx, provider, dir)
	if err != nil {
		return false, err
	}
	if err := a.CheckAuth(ctx); err != nil {
		return false, err
	}
	// A failed state read counts as "not closed".
	if is, err := a.Get(ctx, number, false); err == nil && is.State == "closed" {
		return false, nil
	}
	if err := a.Close(ctx, number, "completed", body); err != nil {
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
func StillOpen(ctx context.Context, env Env, provider, dir string, issue int) bool {
	a, err := env.adapter(ctx, provider, dir)
	if err != nil {
		return false
	}
	is, err := a.Get(ctx, issue, false)
	return err == nil && is.State == "open"
}
