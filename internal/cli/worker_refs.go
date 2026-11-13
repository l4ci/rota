package cli

import (
	"flag"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/worker"
)

// workerRefs is `rota worker refs <integration>` (#3099): run test.refs on an
// integration branch's tip behind a look approval, and push one commit of the
// reference files it changed. Refusals exit 4 and commit nothing.
func workerRefs(fs *flag.FlagSet) RunFunc {
	return func(c *Ctx, args []string) (Result, error) {
		if len(args) != 1 {
			return Result{}, Usage("worker refs takes one integration branch (integration/r<round>-<name>, or its name)")
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		ctx, stop := workerContext()
		defer stop()
		res, err := workerEnvCtx(c, ctx).Refs(ctx, root, worker.RefsOpts{Branch: args[0]})
		if err != nil {
			return Result{}, err
		}
		d := jsonx.NewObject()
		d.Set("verdict", res.Verdict)
		d.Set("branch", res.Branch)
		d.Set("tip", res.Tip)
		d.Set("commit", res.Commit)
		d.Set("files", jsonx.Strings(res.Files))
		d.Set("changed", res.Verdict == worker.RefsRenewed)
		out := Result{Data: d, Text: res.Message}
		switch res.Verdict {
		case worker.RefsRenewed, worker.RefsClean:
			return out, nil
		}
		return out, Refused("%s", res.Message)
	}
}
