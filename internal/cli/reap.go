package cli

import (
	"context"
	"flag"
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/reap"
	"github.com/l4ci/rota/internal/rotatree"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/skills"
	"github.com/l4ci/rota/internal/version"
	"github.com/l4ci/rota/internal/worker"
)

// Deps.ReapEnv builds the round environment reap reads its live set from and
// the host it may close tabs on. Tests swap it for fakes.

// defaultReapEnv is the round's environment without a forge (reap proves
// "merged" from git alone and never asks one, so none is built).
func defaultReapEnv(ctx context.Context, root string, d *Deps) (round.Env, reap.HostOps) {
	e := d.RoundEnvLocal(ctx, root)
	var ops reap.HostOps
	if e.Snapshot != nil {
		if tl := d.Host(e.HostName).Capabilities().TabLister; tl != nil {
			ops = tl
		}
	}
	return e, ops
}

func reapCommand() *Command {
	return &Command{Name: "reap", Fence: fenceAllowed, Summary: "list (and with --apply remove) what a round left behind that nothing live owns, including your leaked rota-smoke/gate/review dirs in the system temp dir", Verb: reapVerb}
}

func reapVerb(fs *flag.FlagSet) RunFunc {
	kind := fs.String("kind", "", "only these kinds, comma-separated: "+strings.Join(reap.Kinds, ", "))
	prunable := fs.Bool("prunable", false, "only worktrees Git marks prunable; requires --kind worktree")
	apply := fs.Bool("apply", false, "delete the candidates that hold no work, in the system temp dir too unless --kind excludes systmp (default: preview)")
	return func(c *Ctx, args []string) (Result, error) {
		if err := noArgs(args); err != nil {
			return Result{}, err
		}
		kinds, err := parseReapKinds(*kind)
		if err != nil {
			return Result{}, err
		}
		if *prunable && (len(kinds) != 1 || kinds[0] != reap.KindWorktree) {
			return Result{}, Usage("--prunable requires --kind worktree")
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		// An unreadable registry reads as an empty pool, which strips the
		// shield from every slot worktree: refuse before listing or deleting.
		if err := worker.CheckRegistry(root); err != nil {
			return Result{}, &Error{Exit: ExitUnavailable, Message: "reap refused: " + err.Error()}
		}
		set, err := c.deps().roundSettings(root, roundConfigRequired)
		if err != nil {
			return Result{}, err
		}
		ctx := c.Context()
		in, warns, err := c.deps().reapInput(ctx, root, set.Roster)
		if err != nil {
			return Result{}, err
		}
		for _, w := range warns {
			c.Warn("%s", w)
		}
		in.PrunableOnly = *prunable
		in.Progress = func(step string) { fmt.Fprintf(c.Stderr, "%s: %s\n", c.Path, step) }
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

		cands, lines := reapCandidateRows(found.Candidates)
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
		d.Set("reaped", jsonx.Strings(applied.Reaped))
		d.Set("failed", failed)
		d.Set("changed", len(applied.Reaped) > 0)
		res := Result{Data: d, Text: strings.Join(lines, "\n")}
		if len(applied.Failed) > 0 {
			return res, Failed("reap: %d deletion(s) failed", len(applied.Failed))
		}
		return res, nil
	}
}

// reapCandidateRows renders the candidates as the JSON rows and the text lines
// of the verb's result.
func reapCandidateRows(found []reap.Candidate) (rows []any, lines []string) {
	rows = make([]any, 0, len(found))
	for _, cd := range found {
		o := jsonx.NewObject()
		o.Set("id", cd.ID)
		o.Set("kind", cd.Kind)
		o.Set("name", cd.Name)
		setIf(o, "path", cd.Path)
		o.Set("reason", cd.Reason)
		setIf(o, "held", cd.Held)
		rows = append(rows, o)
		line := cd.ID + "\t" + cd.Reason
		if cd.Held != "" {
			line += "\theld: " + cd.Held
		}
		lines = append(lines, line)
	}
	return rows, lines
}

// reapInput builds the one reap.Input both `rota reap` and `rota doctor` list
// with, so doctor counts through the same held rules. roster is round.roster.
// The warnings are non-fatal (an unreadable round ledger); an error means the
// round status could not be read, so no live set can be proved.
func (d *Deps) reapInput(ctx context.Context, root string, roster []string) (reap.Input, []string, error) {
	env, ops := d.ReapEnv(ctx, root)
	rep, err := env.Status(ctx, root)
	if err != nil {
		return reap.Input{}, nil, err
	}
	var warns []string
	adopted, err := worker.AdoptedBranches(root)
	if err != nil {
		warns = append(warns, fmt.Sprintf("round ledger unreadable (%v): adopted branches will not be offered for deletion", err))
	}
	in := reap.Input{Root: root, Base: env.Base, Git: env.Git, Report: rep, Host: ops, Lease: env, Adopted: adopted, Roster: roster,
		ProcessUses: d.ProcessUses, SystemTemp: d.TempDir()}
	in.Refresh = func(ctx context.Context) (*round.Report, error) { return env.Status(ctx, root) }
	if set, err := skills.Embedded(); err == nil {
		in.SkillCache = skills.CacheDir(d.Getenv)
		in.SkillCacheKeep = skills.CacheName(version.Get().Version, set.Digest())
	}
	in.TempRoot, _ = rotatree.FindTempRoot(ctx, d.Git, root)
	return in, warns, nil
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
