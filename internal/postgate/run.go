package postgate

import (
	"context"
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/gatereport"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/landing"
)

// Report is a prepared outcome and its resolved branch reference for transport.
type Report struct {
	Gate      landing.GateResult
	Ref, Body string
}

// Runner supplies the gate and train execution boundary.
type Runner interface {
	Gate(context.Context, string, GateOpts) (GateResult, error)
	Train(context.Context, string, TrainOpts) (TrainResult, error)
}

// Env supplies landing execution, bookkeeping and remote reporting.
type Env struct {
	Runner Runner
	Git    git.Runner
	Book   Book
	Report func(context.Context, string, Report) error
}

// Request selects exactly one landing operation and its input adapters.
type Request struct {
	Gate  *GateOpts
	Train *TrainOpts
}

// GateRun keeps the gate verdict separate from report and bookkeeping failures.
// Refused distinguishes an expected refusal from an unexpected execution error.
type GateRun struct {
	Result   landing.GateResult
	Err      error
	Issue    string
	Refused  bool
	BookErr  error
	Warnings []string
}

// TrainRun retains the train result and any failed bookkeeping or report steps.
type TrainRun struct {
	Result   landing.TrainResult
	Err      error
	Refused  bool
	Book     []Failure
	Warnings []string
}

// Result contains the run selected by Request.
type Result struct {
	Gate  GateRun
	Train TrainRun
}

// Run executes one landing operation, records its reports even on refusal or
// error, then accounts for real outcomes. Reporting never changes the verdict.
func (e Env) Run(ctx context.Context, root string, req Request) Result {
	if (req.Gate == nil) == (req.Train == nil) {
		err := fmt.Errorf("postgate: select exactly one gate or train")
		return Result{Gate: GateRun{Err: err}, Train: TrainRun{Err: err}}
	}
	book := e.Book
	if req.Gate != nil {
		r, err := e.Runner.Gate(ctx, root, *req.Gate)
		run := GateRun{Result: r, Err: err, Issue: r.Issue, Refused: r.Refused(err)}
		if req.Gate.CheckOnly {
			return Result{Gate: run}
		}
		run.Warnings = e.report(ctx, root, r)
		if !run.Refused && err == nil {
			run.BookErr = Gate(book, root, r.Issue, r)
		}
		return Result{Gate: run}
	}
	o := *req.Train
	if o.Round == nil {
		o.Round = &landing.RoundMemo{}
	}
	// Landing can drop queued records, so retain the item identities first.
	targets, err := book.Targets(root, o.Targets)
	if err != nil {
		return Result{Train: TrainRun{Err: fmt.Errorf("postgate: load train targets: %w", err)}}
	}
	issues := make(map[string]string, len(targets))
	for target, gt := range targets {
		issues[target] = gt.Issue
	}
	r, err := e.Runner.Train(ctx, root, o)
	if r.Verdict == landing.GateCancelled {
		// The cancel stopped the train, not its bookkeeping: the reports of the
		// members that landed still go out.
		ctx = context.WithoutCancel(ctx)
	}
	run := TrainRun{Result: r, Err: err, Refused: r.Refused(err)}
	if o.Plan {
		return Result{Train: run}
	}
	// A verification failure may name the culprit without a populated gate
	// result. Resolve it once: reports and the bounce notice both read it.
	if r.Culprit != "" {
		g := trainCulprit(issues, targets, r)
		if g.SHA == "" && g.Branch != "" {
			g, _, _ = e.resolveHead(ctx, root, g)
		}
		r.CulpritGate = g
		run.Result = r
	}
	for _, g := range trainReports(issues, r) {
		run.Warnings = append(run.Warnings, e.report(ctx, root, g)...)
	}
	if !run.Refused && err == nil {
		run.Book = Train(book, root, issues, targets, r)
	}
	return Result{Train: run}
}

func (e Env) report(ctx context.Context, root string, r landing.GateResult) []string {
	if e.Report == nil || r.Branch == "" || r.Verdict == "" {
		return nil
	}
	r, ref, ok := e.resolveHead(ctx, root, r)
	if !ok {
		return nil
	}
	if r.SHA == "" {
		return []string{fmt.Sprintf("gate report pending: cannot resolve sha for %s", r.Branch)}
	}
	if err := e.Report(ctx, root, Report{Gate: r, Ref: ref, Body: gatereport.RenderReport(r)}); err != nil {
		return []string{fmt.Sprintf("gate report pending: %s", err)}
	}
	return nil
}

// trainReports contains only outcomes that changed: excluded culprits, landed
// members and the train culprit. A section culprit is never reported twice.
func trainReports(issues map[string]string, r landing.TrainResult) []landing.GateResult {
	var out []landing.GateResult
	seen := map[string]bool{}
	for _, c := range r.Culprits {
		if !seen[c.Target] {
			out = append(out, c.Gate)
			seen[c.Target] = true
		}
	}
	for _, m := range r.Members {
		if !m.Landed {
			continue
		}
		checks := append(append([]string{}, r.Verified...), r.E2EVerified...)
		out = append(out, landing.GateResult{Branch: m.Branch, PR: m.PR, Issue: issues[m.Target], SHA: r.SHA, Verdict: landing.GatePass, Verified: checks, VerifySkipped: len(checks) == 0})
	}
	if r.Culprit != "" && !seen[r.Culprit] {
		out = append(out, r.CulpritGate)
	}
	return out
}

// trainCulprit is the one place the primary culprit's gate result is built,
// even when the worker stopped before a member gate ran (verification and
// pre-merge refusals). Identity comes from r.CulpritGate, then the culprit's
// member, then the registry target. It keeps what r.CulpritGate already holds
// (a resolved SHA), so a second call returns the same value.
func trainCulprit(issues map[string]string, targets map[string]GateTarget, r landing.TrainResult) landing.GateResult {
	g := r.CulpritGate
	if g.Branch == "" {
		for _, m := range r.Members {
			if m.Target == r.Culprit {
				g.Branch, g.PR = m.Branch, m.PR
				break
			}
		}
	}
	target := targets[r.Culprit]
	if g.TargetSlot == "" {
		g.TargetSlot = target.Name
	}
	if g.Branch == "" {
		g.Branch = target.Branch
	}
	if g.PR == "" {
		g.PR = target.PR
	}
	g.Issue = issues[r.Culprit]
	g.Verdict, g.Err, g.Hint = r.Verdict, r.Err, r.Hint
	if g.Err == "" {
		g.Err = fmt.Sprintf("train refused: %s", r.Verdict)
	}
	return g
}

// resolveHead preserves the no-origin boundary and resolves the same branch
// identity for reporting and deduplicating a reconstructed train culprit.
func (e Env) resolveHead(ctx context.Context, root string, r landing.GateResult) (landing.GateResult, string, bool) {
	repo := git.Repo{Dir: root, Runner: e.Git}
	// A recorded PR without origin must never cause forge traffic.
	if r.PR != "" && !e.Book.HasOrigin(ctx, root) {
		return r, "", false
	}
	ref := r.Branch
	if ok, _ := repo.Verify(ctx, "refs/heads/"+ref); !ok {
		ref = "origin/" + ref
	}
	if r.SHA == "" {
		if res, err := repo.Run(ctx, "rev-parse", "--short", ref); err == nil && res.ExitCode == 0 {
			r.SHA = strings.TrimSpace(res.Stdout)
		}
	}
	return r, ref, true
}
