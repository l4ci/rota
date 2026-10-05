package cli

import (
	"errors"
	"flag"
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/worker"
)

func trainData(r worker.TrainResult) *jsonx.Object {
	d := jsonx.NewObject()
	d.Set("base", r.Base)
	d.Set("verdict", r.Verdict)
	var ms []any
	for _, m := range r.Members {
		o := jsonx.NewObject()
		o.Set("target", m.Target)
		o.Set("branch", m.Branch)
		if m.PR != "" {
			o.Set("pr", m.PR)
		}
		o.Set("landed", m.Landed)
		o.Set("culprit", m.Culprit)
		ms = append(ms, o)
	}
	d.Set("members", ms)
	if r.Culprit != "" {
		d.Set("culprit", r.Culprit)
	}
	d.Set("landed", strList(r.Landed))
	d.Set("verified", strList(r.Verified))
	d.Set("changed", r.Changed)
	if r.SHA != "" {
		d.Set("sha", r.SHA)
	}
	return d
}

func workerTrain(fs *flag.FlagSet) RunFunc {
	base := fs.String("base", "", "the cycle branch the PRs merge into")
	landGreen := fs.Bool("land-green", false, "when verification fails, still land the verified members before the culprit")
	confirm := approvalFlags(fs)
	return func(c *Ctx, args []string) (Result, error) {
		if len(args) == 0 {
			return Result{}, Usage("a train needs at least one slot or PR")
		}
		if *base == "" {
			return Result{}, Usage("--base is required")
		}
		conf, req, err := confirm()
		if err != nil {
			return Result{}, err
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		policy, err := mergePolicy(c)
		if err != nil {
			return Result{}, err
		}
		var gateRes Result
		var gateErr error
		approve := func(files func() ([]string, error)) error {
			req.Thread = func() (approvalThread, error) { return slotApprovalThread(root, args[0]) }
			gateRes, gateErr = clearMerge(c, policy, "train "+strings.Join(args, ", ")+" into "+*base, conf, req, files, nil)
			return gateErr
		}
		ctx, stop := workerContext()
		defer stop()
		issues := make([]string, len(args)) // a landed PR drops its queued record, so read before
		for i, t := range args {
			issues[i] = gateIssue(root, t)
		}
		r, err := workerEnvCtx(ctx).Train(ctx, root, worker.TrainOpts{Targets: args, Base: *base, LandGreen: *landGreen, Approve: approve})
		if gateErr != nil {
			var e *Error
			if !errors.As(gateErr, &e) {
				return Result{}, Unavailable("%v", gateErr) // listing the merge's files failed
			}
			d := trainData(r)
			if g, ok := gateRes.Data.(*jsonx.Object); ok {
				for _, k := range []string{"blockedBy", "gate", "paths", "changed"} {
					v, _ := g.Get(k)
					d.Set(k, v)
				}
				for _, k := range []string{"escalation", "status", "answer"} {
					if v, ok := g.Get(k); ok {
						d.Set(k, v)
					}
				}
			}
			return Result{Data: d}, gateErr
		}
		if err != nil {
			return Result{}, err
		}
		for _, n := range r.Notes {
			fmt.Fprintln(c.Stderr, n)
		}
		for i, m := range r.Members {
			if m.Landed && issues[i] != "" {
				if cerr := worker.ClearBounces(root, issues[i]); cerr != nil {
					fmt.Fprintln(c.Stderr, "BOUNCE-COUNT "+m.Target+" — "+cerr.Error())
				}
			}
		}
		res := Result{Data: trainData(r)}
		if r.OK() {
			res.Text = fmt.Sprintf("pass: train of %d -> %s (%s): %s", len(r.Landed), r.Base, r.SHA, strings.Join(r.Landed, ", "))
			return res, nil
		}
		e := Failed("%s", r.Err)
		e.Hint = r.Hint
		return res, e
	}
}
