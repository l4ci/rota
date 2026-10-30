package cli

import (
	"errors"
	"flag"
	"fmt"

	"github.com/l4ci/rota/internal/change"
	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// changeCommands is the `rota change` group (#2348).
func changeCommands() *Command {
	return &Command{Name: "change", Summary: "size a branch's change: the tier the gate and review depth work to", Subs: []*Command{
		{Name: "classify", Summary: "tier (docs, small, full) of a branch's diff against the base, and the files that decided it", Repo: true, Verb: changeClassify},
	}}
}

// changeClassify is `rota change classify [<branch>] [--base <ref>]`: the tier
// change.* puts the branch's diff in. It fails closed: anything it cannot read
// classifies as full with the reason, so the verb only fails to resolve the branch.
func changeClassify(fs *flag.FlagSet) RunFunc {
	baseFlag := fs.String("base", "", "compare against this ref instead of the base branch")
	return func(c *Ctx, args []string) (Result, error) {
		t, err := resolveBranch(c, args)
		if err != nil {
			return Result{}, err
		}
		if *baseFlag != "" {
			t.Base = *baseFlag
		}
		if t.Branch == t.Base {
			return Result{}, Failed("cannot classify base branch '%s' against itself", t.Base)
		}
		res := branchChange(c, t)
		files := make([]any, 0, len(res.Files))
		for _, f := range res.Files {
			files = append(files, f)
		}
		data := gitObj("branch", t.Branch, "base", t.Base, "tier", string(res.Tier), "reason", res.Reason, "files", files, "changedLines", res.Lines)
		return Result{Data: data, Text: fmt.Sprintf("TIER %s — %s (%s)", t.Branch, res.Tier, res.Reason)}, nil
	}
}

// branchChange classifies t's diff against its base, reading the Door line of
// the branch's open PR when one can be read.
func branchChange(c *Ctx, t branchTarget) change.Result {
	src := worker.ChangeSources{Git: c.deps().Git, Dir: t.Dir, Span: t.Base + "..." + t.Branch}
	root, err := backlogScope(c)
	if err != nil {
		src.PolicyErr = err
		return worker.ClassifyChange(c.Context(), src)
	}
	cfg := c.deps().Config(root)
	src.Policy, src.PolicyErr = config.ChangePolicyOf(cfg)
	f, err := c.deps().forge(c.Context(), cfg, "", root)
	switch {
	case err == nil:
		src.PRBody = func() (string, error) {
			body, _, err := branchPRBody(c, f, t.Branch)
			return body, err
		}
	case errors.Is(err, tracker.ErrLocalForge) || tracker.IsCLIMissing(err):
		// no forge, so no PR and no Door line to read
	default:
		src.PRBody = func() (string, error) { return "", err }
	}
	return worker.ClassifyChange(c.Context(), src)
}
