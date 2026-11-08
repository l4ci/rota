package worker

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/l4ci/rota/internal/ledger"
	"github.com/l4ci/rota/internal/rotastate"
	"github.com/l4ci/rota/internal/roundlease"
)

// LedgerRound is the round of the lease the project's git common dir holds,
// 0 when no lease is held (solo without a lease, no round started). The lease
// sits in the common dir, so any process of the round reads it, a worker in
// its own worktree included. A stale lease is nobody's round. A lease that
// cannot be read is no round either, and says so on env's stderr: the entries
// would land under round 0. A verb that writes several entries reads it once.
func LedgerRound(env Env, root string) int {
	env = env.WithDefaults()
	cd, err := rotastate.CommonDirVia(env.Ctx, env.Git, root)
	if err != nil {
		fmt.Fprintf(env.Stderr, "rota: round lease not read, ledger entries get round 0: %v\n", err)
		return 0
	}
	l, st, err := env.Lease().Read(cd)
	if err != nil {
		fmt.Fprintf(env.Stderr, "rota: round lease not read, ledger entries get round 0: %v\n", err)
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
func (m *RoundMemo) Round(env Env, root string) int {
	if m == nil {
		return LedgerRound(env, root)
	}
	m.once.Do(func() { m.n = LedgerRound(env, root) })
	return m.n
}

// Ledger is the recorder of the round ledger: entries are stamped with the
// lease's round unless they name one, and with env's clock unless they carry a
// TS. Slot, account and harness the caller left empty are read from the
// registry: the slot named, else the one holding e.Issue. A ledger that cannot
// be written never fails the verb, but says so on env's stderr.
func (env Env) Ledger() *ledger.Recorder { return env.ledgerIn(nil) }

// ledgerIn is Ledger reading the round through m, so a verb that writes
// several entries reads the lease once.
func (env Env) ledgerIn(m *RoundMemo) *ledger.Recorder {
	env = env.WithDefaults()
	return &ledger.Recorder{
		Round: func(root string) int { return m.Round(env, root) },
		Resolve: func(root string, e ledger.Entry) (ledger.Stamp, bool) {
			// Only fills missing fields: a corrupt registry must not lose the entry.
			s := ledgerSlot(LoadRegistryTolerant(root), e)
			if s == nil {
				return ledger.Stamp{}, false
			}
			return ledger.Stamp{Slot: s.Name(), Harness: s.Kind(), Account: s.AccountFor(cmp.Or(e.Harness, s.Kind()))}, true
		},
		Now:  env.Now,
		Warn: func(msg string) { fmt.Fprintln(env.Stderr, msg) },
	}
}

// LedgerNote appends e to the round ledger through env's Recorder.
func LedgerNote(env Env, root string, e ledger.Entry) { env.Ledger().Note(root, e) }

// LedgerNoteIn is LedgerNote reading the round through m.
func LedgerNoteIn(env Env, m *RoundMemo, root string, e ledger.Entry) {
	env.ledgerIn(m).Note(root, e)
}

// LedgerAppendErr is LedgerNote that returns the failure instead of warning,
// for a verb that must exit non-zero when its ledger row is not written.
func LedgerAppendErr(env Env, root string, e ledger.Entry) error {
	return env.Ledger().AppendErr(root, e)
}

// ledgerSlot is the slot an entry belongs to: the one it names, else the first
// that holds its issue.
func ledgerSlot(reg Registry, e ledger.Entry) *Slot {
	if e.Slot != "" {
		return reg.Slot(e.Slot)
	}
	return reg.SlotHolding(e.Issue)
}

// LedgerDone records a slot turning done with the account's headroom now, the
// end point of its quota burn. e names slot, issue, account, harness and PR.
// Call it after the registry write: the usage fetch must not run under the
// registry lock.
func LedgerDone(env Env, ctx context.Context, a *Accounts, root string, e ledger.Entry) {
	LedgerDoneIn(env, nil, ctx, a, root, e)
}

// LedgerDoneIn is LedgerDone reading the round through m.
func LedgerDoneIn(env Env, m *RoundMemo, ctx context.Context, a *Accounts, root string, e ledger.Entry) {
	e.Kind = ledger.KindDone
	e.Detail = ledger.Detail("headroom", LedgerHeadroom(ctx, a, root, e.Harness, e.Account))
	LedgerNoteIn(env, m, root, e)
}

// paneDone is the ledger entry for a slot a pane row just moved to done; ok is
// false when the row is not done or the slot already was (prev is its state
// before the row was recorded). Call it after recordRow, so the PR is set.
func paneDone(prev string, s *Slot, r PollRow) (e ledger.Entry, ok bool) {
	if r.State != StateDone || strings.EqualFold(prev, "done") {
		return e, false
	}
	return ledger.Entry{Issue: HeldID(s.Task(), s.Branch(), s.Name()), Slot: s.Name(), Account: s.AccountFor(""), Harness: s.Kind(), PR: s.PR()}, true
}

// gateLedger records a gate run: one gate entry with the verdict, and a merge
// entry when the PR landed. A target no slot owns (queued record, external PR)
// has no slot name.
func gateLedger(env Env, m *RoundMemo, root string, t GateTarget, res GateResult) {
	e := ledger.Entry{Kind: ledger.KindGate, Issue: t.Issue, Slot: t.Name, PR: cmp.Or(res.PR, t.PR), Detail: ledger.Detail("verdict", res.Verdict, "tier", res.Tier, "skipped", tierSkipped(res.Skipped))}
	rec := env.ledgerIn(m)
	rec.Note(root, e)
	switch {
	case res.Verdict == GatePass:
		e.Kind, e.Detail = ledger.KindMerge, nil
		rec.Note(root, e)
	case res.AlreadyMerged && !mergeRecorded(root, e):
		e.Kind, e.Detail = ledger.KindMerge, ledger.Detail("by", "remote")
		rec.Note(root, e)
	}
}

// tierSkipped is the skipped test tiers as a ledger detail value, nil (left
// out) when the gate skipped none.
func tierSkipped(skipped []string) any {
	if len(skipped) == 0 {
		return nil
	}
	out := make([]any, len(skipped))
	for i, s := range skipped {
		out[i] = s
	}
	return out
}

// mergeRecorded reports a merge entry already on file for e's PR, so a PR
// merged remotely is noted once however often it is gated again.
func mergeRecorded(root string, e ledger.Entry) bool {
	es, _ := ledger.Load(root)
	return slices.ContainsFunc(es, func(o ledger.Entry) bool {
		return o.Kind == ledger.KindMerge && NormID(o.Issue) == NormID(e.Issue) && o.PR == e.PR
	})
}

// trainLedger records one train outcome, then its verdict for every member it gated that did
// not get a landing gate of its own: the culprit that failed its check, and
// members that passed theirs but did not land. Landed members, and the member
// whose landing gate failed, were recorded by that gate. Only the culprit
// carries the train's verdict; the others name it as the culprit.
func trainLedger(env Env, m *RoundMemo, root, requestID string, res TrainResult, gated map[string]bool) {
	if res.Verdict == "" {
		return
	}
	rec := env.ledgerIn(m)
	rec.Note(root, ledger.Entry{Kind: ledger.KindTrain,
		Detail: ledger.Detail("verdict", res.Verdict, "tier", res.Tier, "skipped", tierSkipped(res.Skipped), "requestId", requestID)})
	for _, culprit := range res.Culprits {
		gateLedger(env, m, root, GateTarget{Name: culprit.Gate.TargetSlot, Issue: culprit.Gate.Issue, Branch: culprit.Gate.Branch, PR: culprit.PR}, culprit.Gate)
	}
	targets := make([]string, 0, len(res.Members)+1)
	for _, m := range res.Members {
		targets = append(targets, m.Target)
	}
	if res.Culprit != "" && !slices.Contains(targets, res.Culprit) {
		targets = append(targets, res.Culprit)
	}
	reg := LoadRegistryTolerant(root) // best-effort ledger notes; the train already read the registry strictly
	for _, t := range targets {
		sectionCulprit := false
		for _, c := range res.Culprits {
			sectionCulprit = sectionCulprit || c.Target == t
		}
		if sectionCulprit {
			continue
		}
		if gated[t] || slices.Contains(res.Landed, t) {
			continue
		}
		gt, err := reg.GateTarget(t)
		if err != nil {
			continue
		}
		verdict := res.Verdict
		if res.Culprit != "" && t != res.Culprit {
			verdict = ""
		}
		rec.Note(root, ledger.Entry{Kind: ledger.KindGate, Issue: gt.Issue, Slot: gt.Name, PR: gt.PR,
			Detail: ledger.Detail("verdict", verdict, "train", true, "culprit", res.Culprit)})
	}
}

// AdoptedBranches is the set of branches `rota worker adopt` took over, read
// from the ledger. The slot is gone once released, so this is what still says
// an `issue-N` branch was adopted rather than made by someone else. The ledger
// is per-developer (gitignored), so it only knows the adoptions made from this
// checkout: on a read error the set is empty and the error says why.
func AdoptedBranches(root string) (map[string]bool, error) {
	out := map[string]bool{}
	entries, err := ledger.Load(root)
	for _, e := range entries {
		if e.Kind == ledger.KindAdopt {
			if b := e.DetailStr("branch"); b != "" {
				out[b] = true
			}
		}
	}
	return out, err
}

// recordVerification notes a fresh local tier execution once. Cached and CI
// answers carry no measurements; replaying a verdict must not invent elapsed time.
func (env Env) recordVerification(root string, m *RoundMemo, tier string, target GateTarget, prs []string, vr VerifyResult) {
	if vr.CI || vr.Cached || vr.WallSeconds == nil || len(vr.Suites) == 0 {
		return
	}
	detail := ledger.Detail("tier", tier, "wallSeconds", *vr.WallSeconds, "suites", vr.Suites)
	if len(prs) > 0 {
		detail.Set("prs", prs)
	}
	if target.PR == "" && len(prs) == 1 {
		target.PR = prs[0]
	}
	LedgerNoteIn(env, m, root, ledger.Entry{Kind: ledger.KindVerification, Issue: target.Issue, Slot: target.Name, PR: target.PR, Detail: detail})
}
