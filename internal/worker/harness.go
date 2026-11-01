package worker

import (
	"context"
	"errors"
	"fmt"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/host"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/rotastate"
)

// The glue between the worker verbs and internal/harness: dispatch and assign
// pick a harness adapter once and call it, and what an adapter refuses with is
// mapped onto the verbs' exit codes here.

// LaunchHarness is the launch adapter for kind ("" is the default), or a usage error when
// the kind is not a worker harness.
func LaunchHarness(kind string) (harness.Launcher, error) {
	h, ok := harness.LookupLauncher(kind)
	if !ok {
		return nil, fail(exitcode.ExitUsage, "kind must be "+harness.KindList()+", got: "+kind)
	}
	return h, nil
}

// AccountHarness is the account adapter for kind, with the same validation
// as LaunchHarness for callers that select accounts without launching.
func AccountHarness(kind string) (harness.Accounts, error) {
	h, ok := harness.LookupAccounts(kind)
	if !ok {
		return nil, fail(exitcode.ExitUsage, "kind must be "+harness.KindList()+", got: "+kind)
	}
	return h, nil
}

// asError maps a harness refusal onto the exit its class names; any other
// error is returned as it is.
func asError(err error) error {
	var r *harness.Refusal
	if !errors.As(err, &r) {
		return err
	}
	exit := map[harness.Class]int{harness.Usage: exitcode.ExitUsage, harness.Unavailable: exitcode.ExitUnavailable,
		harness.Refused: exitcode.ExitRefused, harness.Resolution: exitcode.ExitResolution}[r.Class]
	e := &exitcode.Error{Exit: exit, Message: r.Msg, Hint: r.Hint}
	if r.BlockedBy != "" {
		e.Data = BlockData{BlockedBy: r.BlockedBy}
	}
	return e
}

// probe is the env's reach into the outside world, for a harness's readiness
// check.
func (e Env) probe() harness.Probe {
	return harness.Probe{
		Look: func(name string) (string, bool) { p, err := e.LookPath(name); return p, err == nil },
		Run: func(ctx context.Context, bin string, args, env []string) (harness.Result, error) {
			r, err := e.Run(ctx, bin, args, env)
			return harness.Result{Stdout: r.Stdout, Stderr: r.Stderr, ExitCode: r.ExitCode}, err
		},
	}
}

// ModelApplies reports whether a chosen model reaches the default harness's
// launch command: the default command always takes it, a custom
// round.commands.claude only through the {model} placeholder.
func (e Env) ModelApplies(root string) bool { return e.ModelAppliesTo(root, harness.Default) }

// ModelAppliesTo is ModelApplies for a harness kind.
func (e Env) ModelAppliesTo(root, kind string) bool {
	h, ok := harness.LookupLauncher(kind)
	return ok && h.ModelApplies(harness.ConfigFor(e.WithDefaults().Config(root), h.Kind()))
}

// NeedsModel reports whether a launch of kind needs a tier model.
func (e Env) NeedsModel(root, kind string) bool {
	h, ok := harness.LookupLauncher(kind)
	return !ok || h.NeedsModel(harness.ConfigFor(e.WithDefaults().Config(root), h.Kind()))
}

// CommonDir is the git common dir of root, absolute and symlink-resolved: the
// directory beside which the round lease and the codex homes live.
func CommonDir(ctx context.Context, run git.Runner, root string) (string, error) {
	cd, err := rotastate.CommonDirVia(ctx, run, root)
	if err != nil {
		return "", fail(exitcode.ExitUnavailable, err.Error())
	}
	return cd, nil
}

// Preflight is the harness's check of a slot before anything is marked or
// killed, shared by `worker dispatch` and `round assign`. A harness with
// nothing to check passes.
func (e Env) Preflight(ctx context.Context, root, kind, slot, model string) (harness.Setup, error) {
	e = e.WithDefaults()
	h, err := LaunchHarness(kind)
	if err != nil {
		return harness.Setup{}, err
	}
	wt, current := "", ""
	reg, err := LoadRegistry(root)
	if err != nil {
		return harness.Setup{}, err
	}
	if s := reg.Slot(slot); s != nil {
		wt, current = s.Worktree(), s.HomeAccount()
	}
	var accts []harness.HomeAccount
	load := map[string]int{}
	accounts := harness.ResolveAccounts(h.Kind())
	if !accounts.WorkAccounts() {
		home, _ := e.Home()
		for _, a := range accounts.Accounts(harness.ConfigFor(e.Config(root), h.Kind())).In(home).Entries {
			accts = append(accts, harness.HomeAccount{Name: a.Name, Home: a.Dir})
		}
		for _, s := range reg.Slots() {
			if s.Name() != slot && s.Kind() == h.Kind() && s.HomeAccount() != "" {
				load[s.HomeAccount()]++
			}
		}
	}
	launch, _, err := e.launchLine(root, h, model)
	if err != nil {
		return harness.Setup{}, err
	}
	set, err := h.Preflight(ctx, e.probe(), harness.PreflightOpts{
		Slot: slot, Launch: launch, Herdr: e.hostKind(root) == host.Herdr, Orca: e.hostKind(root) == host.Orca, Worktree: wt,
		CommonDir: func() (string, error) { return CommonDir(ctx, e.Git, root) },
		Accounts:  accts, Account: current, Load: load,
	})
	return set, asError(err)
}

// launchLine is the launch command of h for a chosen model, with the problem
// that makes it unusable, as the verbs report it.
func (e Env) launchLine(root string, h harness.Launcher, model string) (launch string, hit harness.ResumeHit, err error) {
	launch, err = h.Launch(harness.ConfigFor(e.Config(root), h.Kind()), model)
	if err == nil {
		hit, err = h.Resume(launch)
	}
	if err != nil {
		var r *harness.Refusal
		if errors.As(err, &r) {
			return launch, hit, asError(err)
		}
		return launch, hit, fail(exitcode.ExitUsage, fmt.Sprintf("%s cannot be parsed (unbalanced quote?): %s", h.CommandKey(), launch))
	}
	return launch, hit, nil
}
