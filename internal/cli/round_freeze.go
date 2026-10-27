package cli

import (
	"encoding/json"
	"flag"
	"strings"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/landfreeze"
	"github.com/l4ci/rota/internal/worker"
)

func freezeData(r *landfreeze.Record) any {
	if r == nil {
		return nil
	}
	b, _ := json.Marshal(r)
	v, _ := jsonx.Decode(b)
	return v
}

func roundFreeze(fs *flag.FlagSet) RunFunc {
	issue := fs.String("repair-issue", "", "issue allowed through the landing freeze")
	pr := fs.String("repair-pr", "", "PR number or URL allowed through the landing freeze")
	verify := fs.Bool("verify", false, "verify the frozen base and clear only on green")
	clear := fs.Bool("clear", false, "clear without verification; requires --reason")
	reason := fs.String("reason", "", "reason for manually clearing the freeze (recorded in the gate audit)")
	return func(c *Ctx, args []string) (Result, error) {
		if err := noArgs(args); err != nil {
			return Result{}, err
		}
		n := 0
		for _, set := range []bool{*issue != "", *pr != "", *verify, *clear} {
			if set {
				n++
			}
		}
		if n > 1 || (*reason != "" && !*clear) {
			return Result{}, Usage("choose one of --repair-issue, --repair-pr, --verify or --clear --reason")
		}
		if *clear && strings.TrimSpace(*reason) == "" {
			return Result{}, Usage("freeze clear requires --reason <text>")
		}
		if err := landfreeze.ValidateRepair(*issue, *pr); err != nil {
			return Result{}, Usage(err.Error())
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		ctx, stop := workerContext()
		defer stop()
		r, err := workerEnvCtx(c, ctx).Freeze(ctx, root, worker.FreezeOpts{RepairIssue: strings.TrimSpace(*issue), RepairPR: strings.TrimSpace(*pr), Verify: *verify, Clear: *clear, Reason: *reason})
		d := jsonx.NewObject()
		d.Set("freeze", freezeData(r))
		d.Set("changed", n > 0 && err == nil)
		text := "no landing freeze"
		if r != nil {
			if r.Active {
				text = r.Line()
			} else {
				text = "freeze cleared by " + r.ClearedBy + " at " + r.ClearedSHA + ": " + r.Reason
			}
		}
		return Result{Data: d, Text: text}, err
	}
}
