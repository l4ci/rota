package worker

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/ledger"
	"github.com/l4ci/rota/internal/rotastate"
	"github.com/l4ci/rota/internal/roundlease"
)

// leaseEnv reads the round lease; tests swap it.
var leaseEnv = roundlease.DefaultEnv

// LedgerRound is the round of the lease the project's git common dir holds,
// 0 when no lease is held (solo without a lease, no round started). The lease
// sits in the common dir, so any process of the round reads it, a worker in
// its own worktree included. A stale lease is nobody's round. A lease that
// cannot be read is no round either, and says so on stderr: the entries would
// land under round 0. A verb that writes several entries reads it once.
func LedgerRound(root string) int {
	cd, err := rotastate.CommonDir(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rota: round lease not read, ledger entries get round 0: %v\n", err)
		return 0
	}
	l, st, err := leaseEnv().Read(cd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rota: round lease not read, ledger entries get round 0: %v\n", err)
		return 0
	}
	if st != roundlease.Live && st != roundlease.Foreign {
		return 0
	}
	return l.Round
}

// RoundMemo reads the round lease once for a verb: the verb makes one and hands
// it to every ledger write it triggers. A nil memo reads the lease each time.
type RoundMemo struct {
	once sync.Once
	n    int
}

// Round is the lease's round, read on the first call only.
func (m *RoundMemo) Round(root string) int {
	if m == nil {
		return LedgerRound(root)
	}
	m.once.Do(func() { m.n = LedgerRound(root) })
	return m.n
}

// LedgerNote appends e to the round ledger, stamped with the lease's round
// unless e names one. Slot, account and harness the caller left empty are read
// from the registry: the slot named, else the one holding e.Issue. A ledger
// that cannot be written never fails the verb, but says so on stderr.
func LedgerNote(root string, e ledger.Entry) { LedgerNoteIn(nil, root, e) }

// LedgerNoteIn is LedgerNote reading the round through m.
func LedgerNoteIn(m *RoundMemo, root string, e ledger.Entry) {
	if e.Round == 0 {
		e.Round = m.Round(root)
	}
	ledgerAppend(root, e)
}

// ledgerAppend is LedgerNote for an entry whose round is settled.
func ledgerAppend(root string, e ledger.Entry) {
	if e.Account == "" || e.Harness == "" || e.Slot == "" {
		if s := ledgerSlot(LoadRegistry(root), e); s != nil {
			e.Slot = cmp.Or(e.Slot, s.Name())
			e.Account = cmp.Or(e.Account, s.Account())
			e.Harness = cmp.Or(e.Harness, s.Kind())
		}
	}
	if err := ledger.Append(root, e); err != nil {
		fmt.Fprintf(os.Stderr, "rota: round ledger not written (%s): %v\n", ledger.Path(root), err)
	}
}

// ledgerSlot is the slot an entry belongs to: the one it names, else the first
// that holds its issue.
func ledgerSlot(reg Registry, e ledger.Entry) *Slot {
	if e.Slot != "" {
		return reg.Slot(e.Slot)
	}
	id := strings.TrimPrefix(strings.TrimSpace(e.Issue), "#")
	if id == "" {
		return nil
	}
	for _, s := range reg.Slots() {
		if strings.EqualFold(HeldID(s.Task(), s.Branch(), s.Name()), id) {
			return s
		}
	}
	return nil
}

// Headroom is the account's headroom percentage now, nil when no meter reads
// it (an unconfigured account, an unknown verdict): unknown is never 0.
func (a *Accounts) Headroom(ctx context.Context, root, account string) *float64 {
	if account == "" {
		return nil
	}
	return HeadroomOf(a.Meters(ctx, root), account)
}

// HeadroomOf picks the account's headroom out of one Meters reading, so a
// caller with several slots fetches once.
func HeadroomOf(ms []Meter, account string) *float64 {
	for _, m := range ms {
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

// LedgerDone records a slot turning done with the account's headroom now, the
// end point of its quota burn. e names slot, issue, account, harness and PR.
// Call it after the registry write: the usage fetch must not run under the
// registry lock.
func LedgerDone(ctx context.Context, a *Accounts, root string, e ledger.Entry) {
	e.Kind = ledger.KindDone
	e.Detail = ledger.Detail("headroom", LedgerHeadroom(ctx, a, root, e.Harness, e.Account))
	LedgerNote(root, e)
}

// paneDone is the ledger entry for a slot a pane row just moved to done; ok is
// false when the row is not done or the slot already was (prev is its state
// before the row was recorded). Call it after recordRow, so the PR is set.
func paneDone(prev string, s *Slot, r PollRow) (e ledger.Entry, ok bool) {
	if r.State != StateDone || strings.EqualFold(prev, "done") {
		return e, false
	}
	return ledger.Entry{Issue: HeldID(s.Task(), s.Branch(), s.Name()), Slot: s.Name(), Account: s.Account(), Harness: s.Kind(), PR: s.PR()}, true
}

// gateLedger records a gate run: one gate entry with the verdict, and a merge
// entry when the PR landed. A target no slot owns (queued record, external PR)
// has no slot name.
func gateLedger(m *RoundMemo, root string, t GateTarget, res GateResult) {
	issue := t.Issue
	if !t.Queued {
		issue = HeldID(t.Task, t.Branch, t.Name)
	}
	e := ledger.Entry{Kind: ledger.KindGate, Issue: issue, Slot: t.Name, PR: cmp.Or(res.PR, t.PR), Detail: ledger.Detail("verdict", res.Verdict), Round: m.Round(root)}
	ledgerAppend(root, e)
	switch {
	case res.Verdict == GatePass:
		e.Kind, e.Detail = ledger.KindMerge, nil
		ledgerAppend(root, e)
	case res.AlreadyMerged && !mergeRecorded(root, e):
		e.Kind, e.Detail = ledger.KindMerge, ledger.Detail("by", "remote")
		ledgerAppend(root, e)
	}
}

// mergeRecorded reports a merge entry already on file for e's PR, so a PR
// merged remotely is noted once however often it is gated again.
func mergeRecorded(root string, e ledger.Entry) bool {
	es, _ := ledger.Load(root)
	return slices.ContainsFunc(es, func(o ledger.Entry) bool {
		return o.Kind == ledger.KindMerge && o.Issue == e.Issue && o.PR == e.PR
	})
}

// trainLedger records the train's verdict for every member it gated that did
// not get a landing gate of its own: the culprit that failed its check, and
// members that passed theirs but did not land. Landed members, and the member
// whose landing gate failed, were recorded by that gate. Only the culprit
// carries the train's verdict; the others name it as the culprit.
func trainLedger(m *RoundMemo, root string, res TrainResult, gated map[string]bool) {
	if res.Verdict == "" {
		return
	}
	targets := make([]string, 0, len(res.Members)+1)
	for _, m := range res.Members {
		targets = append(targets, m.Target)
	}
	if res.Culprit != "" && !slices.Contains(targets, res.Culprit) {
		targets = append(targets, res.Culprit)
	}
	reg := LoadRegistry(root)
	round := m.Round(root)
	for _, t := range targets {
		if gated[t] || slices.Contains(res.Landed, t) {
			continue
		}
		gt, err := reg.GateTarget(t)
		if err != nil {
			continue
		}
		issue := gt.Issue
		if !gt.Queued {
			issue = HeldID(gt.Task, gt.Branch, gt.Name)
		}
		verdict := res.Verdict
		if res.Culprit != "" && t != res.Culprit {
			verdict = ""
		}
		ledgerAppend(root, ledger.Entry{Kind: ledger.KindGate, Issue: issue, Slot: gt.Name, PR: gt.PR, Round: round,
			Detail: ledger.Detail("verdict", verdict, "train", true, "culprit", res.Culprit)})
	}
}
