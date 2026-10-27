package cli

import (
	"flag"
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/tracker"
)

func roundAccept(fs *flag.FlagSet) RunFunc {
	issues := fs.String("issues", "", "review evidence: filed issues (#a,#b) or none for a completed review with no findings")
	gist := fs.String("gist", "", "the close note (default \"filed #a, #b\"); required for a research item, where it is the milestone's decision-log line")
	return func(c *Ctx, args []string) (Result, error) {
		if len(args) != 1 {
			return Result{}, Usage("round accept takes one item ID")
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		env, be, err := moveEnv(c, root)
		if err != nil {
			return Result{}, err
		}
		id, _, err := resolveItem(be, args[0])
		if err != nil {
			return backlogFail(err)
		}
		in := round.AcceptInput{ID: id, Gist: *gist}
		for _, ref := range strings.Split(*issues, ",") {
			if ref = strings.TrimSpace(ref); ref != "" {
				in.Issues = append(in.Issues, ref)
			}
		}
		res, err := env.Accept(c.Context(), root, be, in)
		if err != nil {
			return moveFailure(err, res.Changed)
		}
		d := jsonx.NewObject()
		d.Set("id", res.ID)
		d.Set("slot", res.Slot)
		d.Set("issues", append([]string{}, res.Issues...))
		d.Set("finding", res.Finding)
		if res.NoFindings {
			d.Set("noFindings", true)
		}
		d.Set("changed", res.Changed)
		out := fmt.Sprintf("%s was already accepted", tracker.RefOf(res.ID))
		if res.Changed && res.NoFindings {
			out = fmt.Sprintf("accepted %s: closed with no findings, %s is free", tracker.RefOf(res.ID), res.Slot)
		} else if res.Changed && len(res.Issues) == 0 {
			out = fmt.Sprintf("accepted %s: closed on its finding, %s is free", tracker.RefOf(res.ID), res.Slot)
		} else if res.Changed {
			out = fmt.Sprintf("accepted %s: closed, filed %s, %s is free", tracker.RefOf(res.ID), strings.Join(res.Issues, ", "), res.Slot)
		}
		return Result{Data: d, Text: out}, nil
	}
}
