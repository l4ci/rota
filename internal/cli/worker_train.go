package cli

import (
	"context"
	"flag"
	"fmt"
	"github.com/l4ci/rota/internal/exitcode"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/gate"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/milestone"
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
	d.Set("order", jsonx.Strings(r.Order))
	if len(r.Culprits) > 0 {
		cs := []any{}
		for _, culprit := range r.Culprits {
			c := jsonx.NewObject()
			c.Set("target", culprit.Target)
			c.Set("pr", culprit.PR)
			c.Set("blockedBy", culprit.BlockedBy)
			cs = append(cs, c)
		}
		d.Set("culprits", cs)
	}
	if r.Culprit != "" {
		d.Set("culprit", r.Culprit)
	}
	if len(r.Interaction) > 0 {
		d.Set("interaction", jsonx.Strings(r.Interaction))
	}
	if r.Stop != nil {
		st := jsonx.NewObject()
		st.Set("tier", r.Stop.Tier)
		st.Set("command", r.Stop.Command)
		st.Set("tests", jsonx.Strings(r.Stop.Tests))
		st.Set("scratch", r.Stop.Scratch)
		st.Set("base", r.Stop.Base)
		ms := []any{}
		for _, m := range r.Stop.Members {
			o := jsonx.NewObject()
			o.Set("target", m.Target)
			o.Set("sha", m.SHA)
			ms = append(ms, o)
		}
		st.Set("members", ms)
		st.Set("culprit", nil)
		st.Set("diagnosed", false)
		d.Set("stop", st)
	}
	d.Set("landed", jsonx.Strings(r.Landed))
	d.Set("anyLanded", len(r.Landed) > 0)
	d.Set("timing", trainTimingData(r))
	if r.Original != nil {
		d.Set("originalFailure", failureData(*r.Original))
	}
	if len(r.Diagnostic) > 0 {
		fs := []any{}
		for _, f := range r.Diagnostic {
			fs = append(fs, failureData(f))
		}
		d.Set("diagnosticFailures", fs)
	}
	d.Set("verified", jsonx.Strings(r.Verified))
	if r.Tier != "" {
		d.Set("tier", r.Tier)
		d.Set("skipped", jsonx.Strings(r.Skipped))
	}
	d.Set("e2eVerified", jsonx.Strings(r.E2EVerified))
	if len(r.Flaky) > 0 {
		d.Set("flaky", flakyData(r.Flaky))
	}
	d.Set("cacheHits", jsonx.Strings(r.CacheHits))
	d.Set("transient", jsonx.Strings(r.Transient))
	d.Set("changed", r.Changed)
	setLedgerData(d, r.Excluded, r.Expired)
	if r.SHA != "" {
		d.Set("sha", r.SHA)
	}
	if r.Plan != nil {
		d.Set("plan", planData(r.Plan))
	}
	if r.Log != "" {
		d.Set("log", r.Log)
	}
	if r.Integration != nil {
		d.Set("integration", integrationData(*r.Integration))
	}
	return d
}

// integrationData is the integration branch a train assembled: its branch,
// the tip it pushed and verified, its members and its PR.
func integrationData(it worker.Integration) *jsonx.Object {
	o := jsonx.NewObject()
	o.Set("name", it.Name)
	o.Set("branch", it.Branch)
	o.Set("tip", it.Assembled)
	o.Set("verifiedTip", it.VerifiedTip)
	o.Set("verifiedBase", it.VerifiedBase)
	ms := []any{}
	for _, m := range it.Members {
		mo := jsonx.NewObject()
		mo.Set("target", m.Target)
		mo.Set("pr", m.PR)
		mo.Set("issue", m.Issue)
		mo.Set("head", m.Head)
		ms = append(ms, mo)
	}
	o.Set("members", ms)
	if it.PR != "" {
		o.Set("pr", it.PR)
	}
	o.Set("items", jsonx.Strings(it.Items))
	return o
}

func trainTimingData(r worker.TrainResult) *jsonx.Object {
	o := jsonx.NewObject()
	o.Set("verifyMs", r.VerifyTime.Milliseconds())
	o.Set("diagnoseMs", r.DiagnoseTime.Milliseconds())
	return o
}

func failureData(f worker.TrainFailure) *jsonx.Object {
	o := jsonx.NewObject()
	o.Set("tier", f.Tier)
	o.Set("command", f.Command)
	o.Set("tests", jsonx.Strings(f.Tests))
	o.Set("members", f.Members)
	o.Set("tip", f.Tip)
	o.Set("onlyInDiagnostic", jsonx.Strings(f.OnlyInDiagnostic))
	return o
}

// trainSummary is the closing line of a train's text: what was spent verifying
// versus diagnosing and whether anything landed (#2753).
func trainSummary(r worker.TrainResult) string {
	landed := "nothing landed"
	if len(r.Landed) > 0 {
		landed = fmt.Sprintf("landed %d of %d member(s)", len(r.Landed), len(r.Members))
	}
	return fmt.Sprintf("verify %s, diagnose %s; %s", r.VerifyTime.Round(time.Millisecond), r.DiagnoseTime.Round(time.Millisecond), landed)
}

// trainFailText is the failed train's message, with the timing summary only
// when verify or diagnose ran: a refusal before verify has none to report.
func trainFailText(r worker.TrainResult) string {
	if r.VerifyTime == 0 && r.DiagnoseTime == 0 {
		return r.Err
	}
	return r.Err + "\n" + trainSummary(r)
}

func planData(p *worker.TrainPlan) *jsonx.Object {
	o := jsonx.NewObject()
	cs := []any{}
	for _, c := range p.Conflicts {
		co := jsonx.NewObject()
		co.Set("a", c.A)
		co.Set("b", c.B)
		co.Set("paths", jsonx.Strings(c.Paths))
		cs = append(cs, co)
	}
	o.Set("conflicts", cs)
	o.Set("landable", jsonx.Strings(p.Landable))
	bs := []any{}
	for _, b := range p.Blocked {
		bo := jsonx.NewObject()
		bo.Set("target", b.Target)
		bo.Set("conflictsWith", jsonx.Strings(b.By))
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
			fmt.Fprintf(&b, "\n  %s x %s (%s)", c.A, c.B, strings.Join(git.DisplayPaths(c.Paths), ", "))
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
	NoWait              bool
	Targets             []string
	Base                string
	Order               []string
	Plan                bool
	LandGreen, NoVerify bool
	OnFail              string
	FinalSlice          []string
	Confirm             gate.Confirm
	Approval            approvalReq
	// RequestID is the call's receipt id: the train's live marker carries it.
	RequestID string
	// Integration names the round's integration branch the members merge into.
	Integration string
}

// trainRun is one train run and the bookkeeping that followed it.
type trainRun = postgate.TrainRun

// prepareTrain supplies the CLI approval and input adapters for a domain run.
func prepareTrain(c *Ctx, ctx context.Context, root string, o trainRunOpts) (postgate.Env, postgate.Request, error) {
	policy, err := mergePolicy(c)
	if err != nil {
		return postgate.Env{}, postgate.Request{}, err
	}
	approve := func(files func() ([]string, error)) (*exitcode.Refusal, error) {
		req := o.Approval
		req.Thread = func() (worker.ApprovalThread, error) { return worker.SlotApprovalThread(root, o.Targets[0]) }
		return clearMerge(c, policy, "train "+strings.Join(o.Targets, ", ")+" into "+o.Base, o.Confirm, req, files, nil)
	}
	syncBase, err := milestoneSyncBase(c, ctx, root, o.Base)
	if err != nil {
		return postgate.Env{}, postgate.Request{}, err
	}
	opts := worker.TrainOpts{NoWait: o.NoWait, Targets: o.Targets, Base: o.Base, SyncBase: syncBase, Order: o.Order, Plan: o.Plan, Say: func(l string) { fmt.Fprintln(c.Stderr, l) }, LandGreen: o.LandGreen, OnFail: o.OnFail, NoVerify: o.NoVerify, FinalSlice: o.FinalSlice, Approve: approve, VerdictRepo: shipVerdict(c, root, root).Repo, CheckReviews: true, ItemBody: lazyItemBody(c, root), RequestID: o.RequestID, Integration: o.Integration}
	return landingEnv(c, ctx, root), postgate.Request{Train: &opts}, nil
}

func runTrain(c *Ctx, root string, o trainRunOpts) trainRun {
	ctx, stop := workerContext()
	defer stop()
	env, req, err := prepareTrain(c, ctx, root, o)
	if err != nil {
		return trainRun{Err: err}
	}
	run := env.Run(ctx, root, req).Train
	reportWarnings(c, run.Warnings)
	return run
}

// milestoneSyncBase is the project base branch a train onto milestone/<id>
// merges into the milestone first, "" for a train on any other base.
func milestoneSyncBase(c *Ctx, ctx context.Context, root, base string) (string, error) {
	if !strings.HasPrefix(base, milestone.Branch("")) {
		return "", nil
	}
	b, ok, err := resolveBase(ctx, c.deps(), root)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", Resolution("%s", noBaseMsg)
	}
	return b, nil
}

func workerTrain(fs *flag.FlagSet) RunFunc {
	base := fs.String("base", "", "the cycle branch the PRs merge into")
	landGreen := fs.Bool("land-green", false, "when verification fails, still land the verified members before the culprit")
	onFail := fs.String("on-fail", worker.OnFailBisect, "what a red tier does: bisect (name the culprit by re-verifying prefixes) or stop (end now, no attribution)")
	noVerify := fs.Bool("no-verify", false, "allow an empty test.full (the train is refused otherwise)")
	noAssignCheck := fs.Bool("no-assign-check", false, "skip the advisory check for free slots with ready candidates")
	order := fs.String("order", "", "merge order as comma-separated PR numbers (or slots); replaces the computed order and must name every member once")
	integration := fs.String("integration", "", "merge the members into the round's pushed integration/r<round>-<name> branch and verify it, landing nothing on --base")
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
		if *onFail != worker.OnFailStop && *onFail != worker.OnFailBisect {
			return Result{}, Usage("--on-fail takes stop or bisect, got %q", *onFail)
		}
		if *integration != "" && (*plan || *landGreen || *onFail == worker.OnFailStop) {
			return Result{}, Usage("--integration cannot be combined with --plan, --land-green or --on-fail stop")
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
		run := runTrain(c, root, trainRunOpts{Targets: args, Base: *base, Order: override, Plan: *plan, LandGreen: *landGreen, OnFail: *onFail, NoVerify: *noVerify, FinalSlice: final, Confirm: conf, Approval: req, RequestID: invocationReceipt(c.Context()).ID, Integration: *integration})
		r, err := run.Result, run.Err
		// Exclusion notes must remain visible when no member survives the checks.
		for _, n := range r.Notes {
			fmt.Fprintln(c.Stderr, n)
		}
		if run.Refused {
			res, rerr := verdictRefusal(r.Verdict, r.Refusal, err, r.Err, r.Hint, trainData(r))
			return res, rerr
		}
		if err != nil {
			return Result{}, err
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
		if r.OK() && r.Integration != nil {
			res.Text = fmt.Sprintf("pass: integrated %d member(s) into %s (%s), verified; nothing landed on %s\n%s", len(r.Members), r.Integration.Branch, r.SHA, r.Base, trainSummary(r))
			return res, nil
		}
		if r.OK() {
			res.Text = fmt.Sprintf("pass: train of %d -> %s (%s): %s\n%s", len(r.Landed), r.Base, r.SHA, strings.Join(r.Landed, ", "), trainSummary(r))
			return res, nil
		}
		if r.Verdict == worker.GateCancelled {
			res.Text = cancelledText(r)
		}
		e := Failed("%s", trainFailText(r))
		e.Hint = r.Hint
		return res, e
	}
}

// cancelledText is the text answer of a cancelled train: what landed and
// where the verify log was kept.
func cancelledText(r worker.TrainResult) string {
	t := fmt.Sprintf("cancelled: train -> %s; landed %d of %d", r.Base, len(r.Landed), len(r.Members))
	if len(r.Landed) > 0 {
		t += ": " + strings.Join(r.Landed, ", ")
	}
	if r.Log != "" {
		t += "\nlogs: " + r.Log
	}
	return t
}

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
