package cli

import (
	"flag"
	"fmt"
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
	return d
}

// trainRunOpts is what one merge train needs: the verb's flags, without the verb.
type trainRunOpts struct {
	Targets             []string
	Base                string
	Order               []string
	LandGreen, NoVerify bool
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
	approve := func(files func() ([]string, error)) error {
		req := o.Approval
		req.Thread = func() (approvalThread, error) { return slotApprovalThread(root, o.Targets[0]) }
		return clearMerge(c, policy, "train "+strings.Join(o.Targets, ", ")+" into "+o.Base, o.Confirm, req, files, nil)
	}
	ctx, stop := workerContext()
	defer stop()
	issues := make(map[string]string, len(o.Targets)) // a landed PR drops its queued record, so read before
	for _, t := range o.Targets {
		issues[t] = gateIssue(root, t)
	}
	round := &worker.RoundMemo{}
	r, err := workerEnvCtx(c, ctx).Train(ctx, root, worker.TrainOpts{Targets: o.Targets, Base: o.Base, Order: o.Order, Say: func(l string) { fmt.Fprintln(c.Stderr, l) }, LandGreen: o.LandGreen, NoVerify: o.NoVerify, Approve: approve, Verdict: shipVerdict(c, root, root).Block, Recorded: recordedReviews(root), Round: round})
	run := trainRun{Result: r, Err: err, Refused: isRefusal(worker.ClassifyVerdict(r.Verdict), err)}
	if run.Refused || err != nil {
		return run
	}
	run.Book = postgate.Train(round, root, issues, r, parkNeedsHuman(c, root))
	return run
}

func workerTrain(fs *flag.FlagSet) RunFunc {
	base := fs.String("base", "", "the cycle branch the PRs merge into")
	landGreen := fs.Bool("land-green", false, "when verification fails, still land the verified members before the culprit")
	noVerify := fs.Bool("no-verify", false, "allow an empty test.full (the train is refused otherwise)")
	order := fs.String("order", "", "merge order as comma-separated PR numbers (or slots); replaces the computed order and must name every member once")
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
		run := runTrain(c, root, trainRunOpts{Targets: args, Base: *base, Order: override, LandGreen: *landGreen, NoVerify: *noVerify, Confirm: conf, Approval: req})
		r, err := run.Result, run.Err
		if run.Refused {
			res, _, rerr := verdictRefusal(r.Verdict, err, r.Err, r.Hint, trainData(r))
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
