package postgate

import (
	"context"
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/worker"
)

// Report is a prepared outcome and its resolved branch reference for transport.
type Report struct {
	Gate      worker.GateResult
	Ref, Body string
}

// Env supplies worker operations and the remote report and parking boundaries.
type Env struct {
	Worker worker.Env
	Report func(context.Context, string, Report) error
	Park   Park
}

// Request selects exactly one landing operation. Worker options carry approval
// and input adapters; lifecycle policy belongs to Run.
type Request struct {
	Gate  *worker.GateOpts
	Train *worker.TrainOpts
}

// GateRun keeps the gate verdict separate from report and bookkeeping failures.
// Refused distinguishes an expected refusal from an unexpected execution error.
type GateRun struct {
	Result   worker.GateResult
	Err      error
	Issue    string
	Refused  bool
	Book     Outcome
	BookErr  error
	Warnings []string
}

// TrainRun retains the train result and any failed bookkeeping or report steps.
type TrainRun struct {
	Result   worker.TrainResult
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
	if req.Gate != nil {
		r, err := e.Worker.Gate(ctx, root, *req.Gate)
		run := GateRun{Result: r, Err: err, Issue: r.Issue, Refused: r.Refused(err)}
		if req.Gate.CheckOnly {
			return Result{Gate: run}
		}
		run.Warnings = e.report(ctx, root, r)
		if !run.Refused && err == nil {
			run.Book, run.BookErr = Gate(e.Worker, root, r.Issue, r, e.Park)
		}
		return Result{Gate: run}
	}
	o := *req.Train
	if o.Round == nil {
		o.Round = &worker.RoundMemo{}
	}
	// Landing can drop queued records, so retain the item identities first.
	issues := make(map[string]string, len(o.Targets))
	targets := make(map[string]worker.GateTarget, len(o.Targets))
	reg, _ := worker.LoadRegistry(root)
	for _, target := range o.Targets {
		if gt, err := reg.GateTargetAny(target); err == nil {
			issues[target] = gt.Issue
			targets[target] = gt
		}
	}
	r, err := e.Worker.Train(ctx, root, o)
	run := TrainRun{Result: r, Err: err, Refused: r.Refused(err)}
	if o.Plan {
		return Result{Train: run}
	}
	// A verification failure may name the culprit without a populated gate
	// result. Resolve its head once for both durable reporting and bounce dedup.
	if r.Culprit != "" {
		g := trainCulprit(issues, r)
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
		run.Book = Train(e.Worker, o.Round, root, issues, r, e.Park)
	}
	return Result{Train: run}
}

func (e Env) report(ctx context.Context, root string, r worker.GateResult) []string {
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
	if err := e.Report(ctx, root, Report{Gate: r, Ref: ref, Body: RenderReport(r)}); err != nil {
		return []string{fmt.Sprintf("gate report pending: %s", err)}
	}
	return nil
}

// trainReports contains only outcomes that changed: excluded culprits, landed
// members and the train culprit. A section culprit is never reported twice.
func trainReports(issues map[string]string, r worker.TrainResult) []worker.GateResult {
	var out []worker.GateResult
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
		out = append(out, worker.GateResult{Branch: m.Branch, PR: m.PR, Issue: issues[m.Target], SHA: r.SHA, Verdict: worker.GatePass, Verified: checks, VerifySkipped: len(checks) == 0})
	}
	if r.Culprit != "" && !seen[r.Culprit] {
		g := trainCulprit(issues, r)
		out = append(out, g)
	}
	return out
}

// trainCulprit reconstructs the primary culprit even when the worker stopped
// before a member gate ran (verification and pre-merge refusals).
func trainCulprit(issues map[string]string, r worker.TrainResult) worker.GateResult {
	g := r.CulpritGate
	if g.Branch == "" {
		for _, m := range r.Members {
			if m.Target == r.Culprit {
				g.Branch, g.PR = m.Branch, m.PR
				break
			}
		}
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
func (e Env) resolveHead(ctx context.Context, root string, r worker.GateResult) (worker.GateResult, string, bool) {
	repo := git.Repo{Dir: root, Runner: e.Worker.Git}
	// A recorded PR without origin must never cause forge traffic.
	if r.PR != "" {
		res, err := repo.Run(ctx, "remote", "get-url", "origin")
		if err != nil || res.ExitCode != 0 {
			return r, "", false
		}
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
