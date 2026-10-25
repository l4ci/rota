package cli

import (
	"flag"
	"fmt"
	"github.com/l4ci/rota/internal/exitcode"
	"strings"

	"github.com/l4ci/rota/internal/gate"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/postgate"
	"github.com/l4ci/rota/internal/worker"
)

func trainData(r worker.TrainResult) *jsonx.Object {
	d := jsonx.NewObject()
	d.Set("base", r.Base)
	d.Set("verdict", r.Verdict)
	ms := []any{}
	for _, m := range r.Members {
		o := jsonx.NewObject()
		o.Set("target", m.Target)
		o.Set("branch", m.Branch)
		if m.PR != "" {
			o.Set("pr", m.PR)
		}
		o.Set("landed", m.Landed)
		o.Set("culprit", m.Culprit)
		o.Set("transient", containsStr(r.Transient, m.Target))
		ms = append(ms, o)
	}
	d.Set("members", ms)
	d.Set("order", strList(r.Order))
	if r.Culprit != "" {
		d.Set("culprit", r.Culprit)
	}
	d.Set("landed", strList(r.Landed))
	d.Set("verified", strList(r.Verified))
	d.Set("e2eVerified", strList(r.E2EVerified))
	d.Set("cacheHits", strList(r.CacheHits))
	d.Set("transient", strList(r.Transient))
	d.Set("changed", r.Changed)
	setLedgerData(d, r.Excluded, r.Expired)
	if r.SHA != "" {
		d.Set("sha", r.SHA)
	}
	if r.Plan != nil {
		d.Set("plan", planData(r.Plan))
	}
	return d
}

func planData(p *worker.TrainPlan) *jsonx.Object {
	o := jsonx.NewObject()
	cs := []any{}
	for _, c := range p.Conflicts {
		co := jsonx.NewObject()
		co.Set("a", c.A)
		co.Set("b", c.B)
		co.Set("paths", strList(c.Paths))
		cs = append(cs, co)
	}
	o.Set("conflicts", cs)
	o.Set("landable", strList(p.Landable))
	bs := []any{}
	for _, b := range p.Blocked {
		bo := jsonx.NewObject()
		bo.Set("target", b.Target)
		bo.Set("conflictsWith", strList(b.By))
		bs = append(bs, bo)
	}
	o.Set("orderBlocked", bs)
	o.Set("command", p.Command)
	o.Set("noVerify", p.NoVerify)
	return o
}

// planText is the human plan: the conflicting pairs, the subset, the rest, and
// the line that lands the subset.
func planText(p *worker.TrainPlan) string {
	var b strings.Builder
	if len(p.Conflicts) == 0 {
		b.WriteString("plan: no member pair conflicts; the whole train is landable")
	} else {
		b.WriteString("plan: conflicts")
		for _, c := range p.Conflicts {
			fmt.Fprintf(&b, "\n  %s x %s (%s)", c.A, c.B, strings.Join(c.Paths, ", "))
		}
	}
	fmt.Fprintf(&b, "\nlandable, in merge order: %s", strings.Join(p.Landable, ", "))
	for _, bl := range p.Blocked {
		fmt.Fprintf(&b, "\norder-blocked: %s (conflicts with %s)", bl.Target, strings.Join(bl.By, ", "))
	}
	if p.Command != "" {
		fmt.Fprintf(&b, "\nrun: %s", p.Command)
	}
	return b.String()
}

// trainRunOpts is what one merge train needs: the verb's flags, without the verb.
type trainRunOpts struct {
	Targets             []string
	Base                string
	Order               []string
	Plan                bool
	LandGreen, NoVerify bool
	FinalSlice          []string
	Confirm             gate.Confirm
	Approval            approvalReq
}

// trainRun is one train run and the bookkeeping that followed it.
type trainRun struct {
	Result  worker.TrainResult
	Err     error
	Refused bool
	// Book lists the bookkeeping steps that failed; the verdict stands.
	Book []postgate.Failure
}

// runTrain verifies and lands several PRs as one train and does the item
// bookkeeping after it, as runGate does for one slot. `worker train` and the
// round autopilot both go through it.
func runTrain(c *Ctx, root string, o trainRunOpts) trainRun {
	policy, err := mergePolicy(c)
	if err != nil {
		return trainRun{Err: err}
	}
	approve := func(files func() ([]string, error)) (*exitcode.Refusal, error) {
		req := o.Approval
		req.Thread = func() (worker.ApprovalThread, error) { return worker.SlotApprovalThread(root, o.Targets[0]) }
		return clearMerge(c, policy, "train "+strings.Join(o.Targets, ", ")+" into "+o.Base, o.Confirm, req, files, nil)
	}
	ctx, stop := workerContext()
	defer stop()
	issues := make(map[string]string, len(o.Targets)) // a landed PR drops its queued record, so read before
	reg, _ := worker.LoadRegistry(root)               // the train refuses a corrupt registry itself
	for _, t := range o.Targets {
		if gt, err := reg.GateTargetAny(t); err == nil {
			issues[t] = gt.Issue
		}
	}
	round := &worker.RoundMemo{}
	r, err := workerEnvCtx(c, ctx).Train(ctx, root, worker.TrainOpts{Targets: o.Targets, Base: o.Base, Order: o.Order, Plan: o.Plan, Say: func(l string) { fmt.Fprintln(c.Stderr, l) }, LandGreen: o.LandGreen, NoVerify: o.NoVerify, FinalSlice: o.FinalSlice, Approve: approve, Verdict: shipVerdict(c, root, root).Block, CheckReviews: true, Round: round, ItemBody: lazyItemBody(c, root)})
	run := trainRun{Result: r, Err: err, Refused: r.Refused(err)}
	if run.Refused || err != nil || o.Plan {
		return run
	}
	run.Book = postgate.Train(workerEnvCtx(c, ctx), round, root, issues, r, parkNeedsHuman(c, root))
	return run
}

func workerTrain(fs *flag.FlagSet) RunFunc {
	base := fs.String("base", "", "the cycle branch the PRs merge into")
	landGreen := fs.Bool("land-green", false, "when verification fails, still land the verified members before the culprit")
	noVerify := fs.Bool("no-verify", false, "allow an empty test.full (the train is refused otherwise)")
	noAssignCheck := fs.Bool("no-assign-check", false, "skip the advisory check for free slots with ready candidates")
	order := fs.String("order", "", "merge order as comma-separated PR numbers (or slots); replaces the computed order and must name every member once")
	plan := fs.Bool("plan", false, "preview the train: map member conflicts and name the largest conflict-free subset; runs no tests, pushes nothing")
	var final stringList
	fs.Var(&final, "final-slice", "a train member (slot or PR) that is the last slice of a partial-slice issue and may close it; repeatable")
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
		if *plan && *order != "" {
			return Result{}, Usage("--plan previews the computed order; it cannot be combined with --order")
		}
		var override []string
		if *order != "" {
			seenTok := map[string]bool{}
			for _, t := range strings.Split(*order, ",") {
				t = strings.TrimSpace(t)
				if t == "" || seenTok[t] {
					return Result{}, Usage("--order needs a comma-separated list of distinct PR numbers or slots, with no empty entries")
				}
				seenTok[t] = true
				override = append(override, t)
			}
		}
		if !*plan && !*noAssignCheck {
			warnAssignFirst(c, root)
		}
		run := runTrain(c, root, trainRunOpts{Targets: args, Base: *base, Order: override, Plan: *plan, LandGreen: *landGreen, NoVerify: *noVerify, FinalSlice: final, Confirm: conf, Approval: req})
		r, err := run.Result, run.Err
		if run.Refused {
			res, rerr := verdictRefusal(r.Verdict, r.Refusal, err, r.Err, r.Hint, trainData(r))
			return res, rerr
		}
		if err != nil {
			return Result{}, err
		}
		for _, n := range r.Notes {
			fmt.Fprintln(c.Stderr, n)
		}
		for _, h := range r.CacheHits {
			fmt.Fprintln(c.Stderr, "CACHE-HIT "+h+" — verdict reused, not re-run")
		}
		for _, f := range run.Book {
			fmt.Fprintln(c.Stderr, "BOUNCE-COUNT "+f.Target+" — "+f.Err.Error())
		}
		res := Result{Data: trainData(r)}
		if r.Plan != nil {
			res.Text = planText(r.Plan)
			return res, nil
		}
		if r.OK() {
			res.Text = fmt.Sprintf("pass: train of %d -> %s (%s): %s", len(r.Landed), r.Base, r.SHA, strings.Join(r.Landed, ", "))
			return res, nil
		}
		e := Failed("%s", r.Err)
		e.Hint = r.Hint
		return res, e
	}
}

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
