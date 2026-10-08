package worker

import (
	"context"

	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/ledger"
)

// LedgerRound is the round number the registry last recorded, 0 when none was
// started (solo without a lease): the round every ledger entry carries.
func LedgerRound(root string) int {
	n, _ := LoadRegistry(root).Round()
	return n
}

// LedgerNote appends e to the round ledger, stamped with the registry's round
// unless e names one. A ledger that cannot be written never fails the verb.
func LedgerNote(root string, e ledger.Entry) {
	if e.Round == 0 {
		e.Round = LedgerRound(root)
	}
	_ = ledger.Append(root, e)
}

// Headroom is the account's headroom percentage now, nil when no meter reads
// it (an unconfigured account, an unknown verdict): unknown is never 0.
func (a *Accounts) Headroom(ctx context.Context, root, account string) *float64 {
	if account == "" {
		return nil
	}
	for _, m := range a.Meters(ctx, root) {
		if m.Name == account {
			return m.Headroom
		}
	}
	return nil
}

// LedgerHeadroom is the headroom of an assign or done entry:
// set only for a claude slot whose meter reads it. Codex has no meter.
func LedgerHeadroom(ctx context.Context, a *Accounts, root, kind, account string) *float64 {
	if a == nil || kind == harness.Codex || account == "" {
		return nil
	}
	return a.Headroom(ctx, root, account)
}

// gateLedger records a gate run: one gate entry with the verdict, and a merge
// entry when the PR landed. A target no slot owns (queued record, external PR)
// has no slot name.
func gateLedger(root string, t GateTarget, res GateResult) {
	issue := t.Issue
	if !t.Queued {
		issue = HeldID(t.Task, t.Branch, t.Name)
	}
	e := ledger.Entry{Kind: ledger.KindGate, Issue: issue, Slot: t.Name, PR: firstOf(res.PR, t.PR), Detail: ledger.Detail("verdict", res.Verdict)}
	LedgerNote(root, e)
	if res.Verdict == GatePass {
		e.Kind, e.Detail = ledger.KindMerge, nil
		LedgerNote(root, e)
	}
}

func firstOf(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
