package worker

import (
	gateaudit "github.com/l4ci/rota/internal/gate"
	"github.com/l4ci/rota/internal/landing"
	"time"
)

// stepRecordLedger appends the verdict to the gate ledger.
func (runner gateRun) stepRecordLedger(result landing.GateResult) (struct{}, landing.GateResult, bool, error) {
	g := runner.operation(result, GateTarget{}, gateForgeResult{})
	finish := func(done bool, err error) (struct{}, landing.GateResult, bool, error) {
		return struct{}{}, *g.res, done, err
	}

	if !g.o.CheckOnly {
		gateLedger(g.e, g.o.Round, g.root, g.resolved, *g.res)
	}
	return finish(false, nil)
}

// stepDequeue drops the review record of a PR that merged: a pass, or a verdict
// (merged-remotely, a red verify of the landed tree) that still left the PR on
// the base. The target may have resolved to a slot, so the record is matched by
// PR, not by how the argument resolved.
func (runner gateRun) stepDequeue(result landing.GateResult, pr string) (struct{}, landing.GateResult, bool, error) {
	g := runner.operation(result, GateTarget{PR: pr}, gateForgeResult{})
	finish := func(done bool, err error) (struct{}, landing.GateResult, bool, error) {
		return struct{}{}, *g.res, done, err
	}

	if g.o.CheckOnly || !(g.res.Verdict == landing.GatePass || g.res.Changed) {
		return finish(false, nil)
	}
	return finish(false, RemoveQueuedPR(g.root, g.target.PR))
}

// stepReleaseExternal unregisters a merged adopted slot and keeps its checkout.
func (runner gateRun) stepReleaseExternal(result landing.GateResult, target GateTarget) (struct{}, landing.GateResult, bool, error) {
	g := runner.operation(result, target, gateForgeResult{})
	finish := func(done bool, err error) (struct{}, landing.GateResult, bool, error) {
		return struct{}{}, *g.res, done, err
	}

	t := g.target
	if t.External && t.Name != "" && g.res.Verdict == landing.GatePass && !g.o.CheckOnly {
		// The PR is merged: a release that cannot finish is a note, not a failed gate.
		if rerr := g.e.ReleaseExternal(g.root, t.Name, g.o.Prune); rerr != nil {
			g.res.Notes = append(g.res.Notes, "RELEASE-KEPT "+t.Name+" — "+rerr.Error())
		}
	}
	return finish(false, nil)
}

// appendGateAudit writes one line of .rota/gate-audit.jsonl for a gate step
// that changed what the gate would otherwise have refused.
func appendGateAudit(root string, now time.Time, verb, target, note string) error {
	return gateaudit.AppendAudit(root, gateaudit.Record{TS: now, Gate: "outcome", Verb: verb, Target: target, Note: note})
}
