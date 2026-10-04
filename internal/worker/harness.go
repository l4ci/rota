package worker

import (
	"context"
	"errors"
	"fmt"
	"github.com/l4ci/rota/internal/exitcode"
	"path/filepath"
	"strings"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/harness"
)

// The glue between the worker verbs and internal/harness: dispatch and assign
// pick a harness adapter once and call it, and what an adapter refuses with is
// mapped onto the verbs' exit codes here.

func loadConfig(root string) any { return config.Load(filepath.Join(root, ".rota", "config.json")) }

// Harness is the adapter for kind ("" is the default), or a usage error when
// the kind is not a worker harness.
func Harness(kind string) (harness.Harness, error) {
	h, ok := harness.Lookup(kind)
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
			return harness.Result(r), err
		},
	}
}

// ModelApplies reports whether a chosen model reaches the default harness's
// launch command: the default command always takes it, a custom
// work.workerCommand only through the {model} placeholder.
func ModelApplies(root string) bool { return ModelAppliesTo(root, harness.Default) }

// ModelAppliesTo is ModelApplies for a harness kind.
func ModelAppliesTo(root, kind string) bool {
	h, ok := harness.Lookup(kind)
	return ok && h.ModelApplies(loadConfig(root))
}

// NeedsModel reports whether a launch of kind needs a tier model.
func NeedsModel(root, kind string) bool {
	h, ok := harness.Lookup(kind)
	return !ok || h.NeedsModel(loadConfig(root))
}

// CommonDir is the git common dir of root, absolute and symlink-resolved: the
// directory beside which the round lease and the codex homes live.
func CommonDir(ctx context.Context, git GitFunc, root string) (string, error) {
	out, errOut, code, err := git(ctx, root, "rev-parse", "--git-common-dir")
	if err != nil || code != 0 {
		return "", fail(exitcode.ExitUnavailable, "git rev-parse --git-common-dir failed: "+strings.TrimSpace(errOut))
	}
	p := strings.TrimSpace(out)
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return filepath.Clean(p), nil
}

// Preflight is the harness's check of a slot before anything is marked or
// killed, shared by `worker dispatch` and `round assign`. A harness with
// nothing to check passes.
func (e Env) Preflight(ctx context.Context, root, kind, slot string, accept bool) (harness.Setup, error) {
	e = e.withDefaults()
	h, err := Harness(kind)
	if err != nil {
		return harness.Setup{}, err
	}
	wt := ""
	if s := LoadRegistry(root).Slot(slot); s != nil {
		wt = Str(s, "worktree")
	}
	set, err := h.Preflight(ctx, e.probe(), harness.PreflightOpts{
		Slot: slot, Accept: accept, Herdr: dispatchKind(root) == "herdr", Worktree: wt,
		CommonDir: func() (string, error) { return CommonDir(ctx, e.Git, root) },
	})
	return set, asError(err)
}

// launchLine is the launch command of h for a chosen model, with the problem
// that makes it unusable, as the verbs report it.
func launchLine(root string, h harness.Harness, model string) (launch string, hit harness.ResumeHit, err error) {
	launch, err = h.Launch(loadConfig(root), model)
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
