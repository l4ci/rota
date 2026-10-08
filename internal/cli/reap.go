package cli

import (
	"context"
	"flag"
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/reap"
	"github.com/l4ci/rota/internal/round"
)

// Deps.ReapEnv builds the round environment reap reads its live set from and
// the host it may close tabs on. Tests swap it for fakes.

// defaultReapEnv is the round's environment without a forge (reap proves
// "merged" from git alone and never asks one, so none is built).
func defaultReapEnv(ctx context.Context, root string, d *Deps) (round.Env, reap.HostOps) {
	e := d.RoundEnvLocal(ctx, root)
	var ops reap.HostOps
	if e.Snapshot != nil && e.HostName == "herdr" {
		if tl, ok := d.Host("herdr").(host.TabLister); ok {
			ops = herdrReapOps{tl}
		}
	}
	return e, ops
}

// herdrReapOps adapts herdr's tab listing. herdr reports no process tree it
// can prove agent-free, so reap lists no process on a real host.
type herdrReapOps struct{ host.TabLister }

func (herdrReapOps) Processes(context.Context) ([]reap.Process, error) { return nil, nil }
func (herdrReapOps) StopProcess(context.Context, int) error {
	return fmt.Errorf("stopping a process is not supported on a real host")
}

func reapCommand() *Command {
	return &Command{Name: "reap", Summary: "list (and with --apply remove) what a round left behind that nothing live owns", Verb: reapVerb}
}

func reapVerb(fs *flag.FlagSet) RunFunc {
	kind := fs.String("kind", "", "only these kinds, comma-separated: worktree, branch, tab, process, lease")
	apply := fs.Bool("apply", false, "delete the candidates that hold no work (default: preview)")
	return func(c *Ctx, args []string) (Result, error) {
		if err := noArgs(args); err != nil {
			return Result{}, err
		}
		kinds, err := parseReapKinds(*kind)
		if err != nil {
			return Result{}, err
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		ctx := c.Context()
		env, ops := c.deps().ReapEnv(ctx, root)
		var agents []host.Agent
		if snap := env.Snapshot; snap != nil {
			env.Snapshot = func(ctx context.Context) ([]host.Agent, error) {
				a, err := snap(ctx)
				agents = a
				return a, err
			}
		}
		rep, err := env.Status(ctx, root)
		if err != nil {
			return Result{}, err
		}
		in := reap.Input{Root: root, Base: env.Base, Git: env.Git, Report: rep, Agents: agents, Host: ops, Lease: env}
		found, err := reap.Find(ctx, in, kinds)
		if err != nil {
			return Result{}, err
		}
		for _, w := range found.Warnings {
			c.Warn("%s", w)
		}
		var applied reap.Result
		if *apply {
			applied = reap.Apply(ctx, in, found.Candidates)
			for _, w := range applied.Warnings {
				c.Warn("%s", w)
			}
		} else {
			c.Warn("preview only; pass --apply")
		}

		cands := make([]any, 0, len(found.Candidates))
		var lines []string
		for _, cd := range found.Candidates {
			o := jsonx.NewObject()
			o.Set("id", cd.ID)
			o.Set("kind", cd.Kind)
			o.Set("name", cd.Name)
			setIf(o, "path", cd.Path)
			o.Set("reason", cd.Reason)
			setIf(o, "held", cd.Held)
			cands = append(cands, o)
			line := cd.ID + "\t" + cd.Reason
			if cd.Held != "" {
				line += "\theld: " + cd.Held
			}
			lines = append(lines, line)
		}
		failed := make([]any, 0, len(applied.Failed))
		for _, f := range applied.Failed {
			o := jsonx.NewObject()
			o.Set("id", f.ID)
			o.Set("error", f.Error)
			failed = append(failed, o)
			lines = append(lines, "failed\t"+f.ID+"\t"+f.Error)
		}
		d := jsonx.NewObject()
		d.Set("candidates", cands)
		d.Set("reaped", strs(applied.Reaped))
		d.Set("failed", failed)
		d.Set("changed", len(applied.Reaped) > 0)
		res := Result{Data: d, Text: strings.Join(lines, "\n")}
		if len(applied.Failed) > 0 {
			return res, Failed("reap: %d deletion(s) failed", len(applied.Failed))
		}
		return res, nil
	}
}

func parseReapKinds(s string) ([]string, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	var out []string
	for _, k := range strings.Split(s, ",") {
		k = strings.TrimSpace(k)
		ok := false
		for _, known := range reap.Kinds {
			ok = ok || k == known
		}
		if !ok {
			return nil, Usage("unknown --kind %q (want %s)", k, strings.Join(reap.Kinds, ", "))
		}
		out = append(out, k)
	}
	return out, nil
}
