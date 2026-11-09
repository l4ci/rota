package cli

import (
	"context"
	"fmt"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/worker"
)

// The one place a round.Env is built from Deps: buildRoundEnv takes the worker
// env and the account option up front and fills every field once, so no later
// layer overwrites what another built. defaultRoundEnv and accountedRoundEnv
// are thin named entry points over it; withBoard adds the claim side. Every
// clock in the env (worker, lease) reads Deps.Now.

// baseRoundEnv is the round.Env every round verb starts from: the one Git, the
// lease and the worker env, all read off Deps so a swapped fake reaches them.
// Account picking is off.
func (d *Deps) baseRoundEnv() round.Env {
	return newRoundEnv(d, d.workerEnv(), nil)
}

// newRoundEnv builds the base env around w. accounts nil leaves picking off
// and drops the worker's meters; non-nil turns it on, reading them.
func newRoundEnv(d *Deps, w worker.Env, accounts *worker.Accounts) round.Env {
	e := round.Env{Git: d.Git, Lease: d.LeaseEnv(), Worker: round.FromWorkerEnv(w), Login: d.credentials()}
	return e.WithAccounts(accounts)
}

// buildRoundEnv wires the real git, host and forge. The host is herdr when
// the round recorded (C8); with none it is herdr when round.dispatch says so or
// the process runs inside herdr (rounds are started by hand with `herdr
// worktree create`, whatever the config says), else tmux.
// Account picking stays off: it is opt-in (accountedRoundEnv), so start, status
// and the other verbs never take that path by accident.
// A host or forge that cannot be built or reached is left nil: the verbs
// report it as unavailable instead of failing. withForge false builds no
// forge at all (Forge and ForgeErr stay empty), for a caller that reads only
// the local sources and would not wait on the network.
// It is the one constructor: w is the worker env the verb runs on
// and accounts the meters to pick from (nil keeps picking off).
func buildRoundEnv(ctx context.Context, root string, d *Deps, withForge bool, w worker.Env, accounts *worker.Accounts) round.Env {
	cfg := d.Config(root)
	e := newRoundEnv(d, w, accounts)
	e.Base = "main"
	configured, _ := config.Lookup(cfg, "git.baseBranch")
	baseCfg, _ := configured.(string)
	if b, ok, err := d.Repo(root).Base(ctx, baseCfg); err == nil && ok {
		e.Base = b
	} else if err == nil && baseCfg != "" {
		// A set but missing base is not replaced by main: the verbs refuse.
		e.Base = ""
		e.BaseErr = fmt.Sprintf("git.baseBranch %q does not resolve to a branch (fix or unset it in .rota/config.json)", baseCfg)
	}
	// A round started by hand (`herdr worktree create`) has no recorded host and
	// resolves from the environment like every other verb.
	hostKind := d.resolvePaneHost(root, cfg)
	if hostKind == host.Solo {
		e.HostName = host.Solo // no panes to snapshot, and not an unavailable host
	} else {
		h := d.Host(hostKind)
		if err := h.Require(); err != nil {
			e.HostErr = err.Error()
		} else if s := h.Capabilities().Snapshotter; s != nil {
			e.Snapshot, e.HostName = s.Snapshot, h.Kind()
			e.Capture = h.Capture
			if tf := h.Capabilities().TabFollower; tf != nil {
				e.FollowTab = tf.FollowTab
			}
		}
	}
	labels := roundcfg.Lenient(cfg).Labels
	e.NeedsHuman, e.Label, e.SupportLabel, e.NeedsReview = labels.NeedsHuman, labels.InProgress, roundcfg.MachineLabel(roundcfg.SupportName(cfg)), labels.NeedsReview
	e.Round, e.RoundErr = d.roundSettings(root, roundConfigUsage)
	if !withForge {
		return e
	}
	f, err := d.forgeAndIssues(ctx, cfg, root)
	if err != nil {
		e.ForgeErr = err.Error()
	} else {
		e.Forge = f
	}
	return e
}

// RoundOpts is what a verb chooses when its round env is built: the worker env
// it runs on and, to opt into account picking, the meters to pick from (nil
// leaves picking off).
type RoundOpts struct {
	Worker   worker.Env
	Accounts *worker.Accounts
}

// roundEnv is the plain env on the hook's own worker env: picking off.
func (d *Deps) roundEnv(ctx context.Context, root string) round.Env {
	return d.RoundEnv(ctx, root, RoundOpts{Worker: d.workerEnv()})
}

// roundEnv is the plain env on the invocation-bound worker env (workerEnvCtx).
func roundEnv(c *Ctx, ctx context.Context, root string) round.Env {
	return c.deps().RoundEnv(ctx, root, RoundOpts{Worker: workerEnvCtx(c, ctx)})
}

// accountedRoundEnv is roundEnv with account picking on, for a verb that reads
// quota or picks an account: the one place a verb opts in, chosen when the env
// is built.
func accountedRoundEnv(c *Ctx, ctx context.Context, root string) round.Env {
	d := c.deps()
	return d.RoundEnv(ctx, root, RoundOpts{Worker: workerEnvCtx(c, ctx), Accounts: d.WorkerAccounts()})
}

// withBoard gives the env the backlog's claim side, which claim-mismatch drift
// reads. File mode has no claims, and a backlog that cannot be opened is a
// warning: the other drift kinds still report.
func withBoard(c *Ctx, root string, env round.Env) round.Env {
	if env.Board != nil {
		return env
	}
	if caps, _, err := backendMode(c.deps(), root); err != nil || !caps.IssueIDs {
		return env
	}
	be, err := openBacklog(c, root, false, "")
	if err != nil {
		c.Warn("claim check skipped: %v", err)
		return env
	}
	if b, ok := round.BoardOf(be); ok {
		env.Board = b
	}
	return env
}
