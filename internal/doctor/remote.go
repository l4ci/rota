package doctor

import (
	"context"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/tracker"
)

// Tracker is the issue port shared by the claim board and label scan. Label
// listing and metadata remain optional capabilities of the concrete adapter.
type Tracker = backlog.Tracker

// RemoteScanner gathers doctor's remote inputs. A scan shares one deadline and
// one tracker per directory (an umbrella may have several tracker directories).
// RoundEnv supplies local settings, git and the clock, without building a forge.
type RemoteScanner struct {
	NewTracker func(context.Context, string) (Tracker, error)
	NewForge   func(context.Context) (tracker.Forge, error)
	RoundEnv   func(context.Context) round.Env
	Cwd        string
	Today      func() time.Time
	Timeout    time.Duration
}

type RemoteScan struct {
	StaleClaims []StaleClaim
	Warnings    []string
	Labels      *LabelState
}

const remoteScanTimeout = 5 * time.Second

func (s RemoteScanner) Scan(ctx context.Context, root string, cfg any) RemoteScan {
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = remoteScanTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	type client struct {
		tracker Tracker
		err     error
	}
	clients := map[string]client{}
	get := func(ctx context.Context, dir string) (Tracker, error) {
		if c, ok := clients[dir]; ok {
			return c.tracker, c.err
		}
		tr, err := s.NewTracker(ctx, dir)
		clients[dir] = client{tr, err}
		return tr, err
	}
	out := RemoteScan{}
	out.StaleClaims, out.Warnings = s.staleClaims(ctx, root, cfg, get)
	out.Labels = scanLabels(ctx, cfg, func() (Tracker, error) { return get(ctx, root) })
	return out
}

// scanForge supplies round's forge and issue sides without building a second
// issue client. The stale-claim algorithm itself remains owned by round.
type scanForge struct {
	tracker.Forge
	Tracker
}

func (s RemoteScanner) staleClaims(ctx context.Context, root string, cfg any, get func(context.Context, string) (Tracker, error)) ([]StaleClaim, []string) {
	env := s.RoundEnv(ctx)
	if env.RoundErr != nil {
		return nil, []string{env.RoundErr.Error()}
	}
	forge, err := s.NewForge(ctx)
	if err != nil {
		return nil, []string{err.Error()}
	}
	tr, err := get(ctx, root)
	if err != nil {
		return nil, []string{err.Error()}
	}
	env.Forge = scanForge{forge, tr}
	board := env.Board
	if board == nil {
		be, err := backlog.Open(ctx, root, cfg, backlog.Options{Cwd: s.Cwd, Today: s.Today, NewTracker: get})
		if err != nil {
			return nil, []string{err.Error()}
		}
		var ok bool
		board, ok = round.BoardOf(be)
		if !ok {
			return nil, []string{"tracker does not support claims"}
		}
	}
	claims, warnings := env.StaleClaims(ctx, root, board, nil)
	if err := ctx.Err(); err != nil {
		reason := err.Error()
		found := false
		for _, warning := range warnings {
			if strings.Contains(warning, reason) {
				found = true
				break
			}
		}
		if !found {
			warnings = append(warnings, reason)
		}
	}
	var out []StaleClaim
	for _, claim := range claims {
		out = append(out, StaleClaim{Issue: claim.Issue, Holder: claim.Holder, Last: claim.Last})
	}
	return out, warnings
}

func scanLabels(ctx context.Context, cfg any, get func() (Tracker, error)) *LabelState {
	state := &LabelState{}
	var err error
	state.Entries, err = config.ResolvedLabels(cfg)
	if err != nil {
		state.Problem = err.Error()
		return state
	}
	tr, err := get()
	if err != nil {
		state.Problem = err.Error()
		return state
	}
	lister, ok := tr.(tracker.LabelLister)
	if !ok {
		state.Problem = "tracker does not support listing label definitions"
		return state
	}
	_, state.Metadata = tr.(tracker.LabelManager)
	state.Existing, err = lister.ListLabels(ctx)
	if err != nil {
		state.Problem = err.Error()
		return state
	}
	open, err := tr.List(ctx, tracker.ListFilter{State: "open"})
	if err != nil {
		state.Problem = err.Error()
		return state
	}
	state.Used = map[string]bool{}
	for _, issue := range open {
		for _, label := range issue.Labels {
			state.Used[label] = true
		}
	}
	if err := ctx.Err(); err != nil {
		state.Problem = err.Error()
	}
	return state
}
