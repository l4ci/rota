package round

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/bugreport"
	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/ledger"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// MaintainerWait is a recorded question or a hold that needs a human decision.
// Since is empty when the source has no timestamp; readers must not invent one.
type MaintainerWait struct {
	Kind, Item, Ask, Since, DefaultReply string
}

// NeedsYou collects decisions for status and the watch's prompt snapshot. It never
// answers an escalation, changes a label or runs a gate. Spec waiver requests
// use explicit escalations: a failing review alone is work for the worker.
func (e Env) NeedsYou(ctx context.Context, root string) ([]MaintainerWait, []string) {
	var out []MaintainerWait
	var warnings []string
	reg, err := worker.LoadRegistry(root)
	if err != nil {
		return nil, []string{fmt.Sprintf("maintainer digest: %v", err)}
	}
	entries, err := ledger.Load(root)
	if err != nil {
		warnings = append(warnings, fmt.Sprintf("maintainer digest ledger: %v", err))
	}
	if e.Forge == nil && e.NeedsHuman != "" && e.ForgeErr != "" {
		// File-mode rounds have no needs-human labels to read. A missing
		// forge there does not make their local escalation count incomplete.
		if mode, err := config.Backend(e.Worker.Config(root)); err != nil || mode == "issues" {
			warnings = append(warnings, "maintainer digest forge unavailable: "+e.ForgeErr)
		}
	}
	// An explicit escalation replaces the implicit hold on its thread or slot.
	escalated := map[string]bool{}
	for _, esc := range reg.Escalations() {
		if esc.Status != worker.EscalationPending {
			continue
		}
		reply := "reply"
		if esc.Recommend > 0 && esc.Recommend <= len(esc.Options) && esc.Recommend <= 26 {
			reply = string(rune('A' + esc.Recommend - 1))
		}
		item := esc.Ref()
		if esc.Kind == "pr" {
			item = "PR " + item
		}
		out = append(out, MaintainerWait{"escalation", item, esc.Title, esc.SentAt, reply})
		escalated[item] = true
		if esc.Slot != "" {
			escalated["slot:"+esc.Slot] = true
		}
	}

	// Only the latest gate result for a target still held by a slot or queue
	// can ask for merge approval; old refusals must not survive a later gate.
	gateByPR := map[int]ledger.Entry{}
	gateBySlot := map[string]ledger.Entry{}
	parkedAt := map[string]string{}
	for _, entry := range entries {
		id := worker.NormID(entry.Issue)
		switch entry.Kind {
		case ledger.KindTransfer:
			if entry.Slot == "human" {
				parkedAt[id] = waitStamp(entry.TS)
			} else {
				delete(parkedAt, id)
			}
		case ledger.KindGate, ledger.KindMerge:
			if n, ok := worker.PRRefNumber(entry.PR); ok {
				gateByPR[n] = entry
			} else if entry.Slot != "" {
				gateBySlot[entry.Slot] = entry
			}
		}
	}
	covered := func(item, pr, slot string) bool {
		if escalated[item] || (slot != "" && escalated["slot:"+slot]) {
			return true
		}
		n, ok := worker.PRRefNumber(pr)
		return ok && escalated[fmt.Sprintf("PR #%d", n)]
	}
	openPRs := map[int]bool{}
	openPR := func(pr string) bool {
		n, ok := worker.PRRefNumber(pr)
		if !ok || e.Forge == nil {
			return true
		}
		if open, seen := openPRs[n]; seen {
			return open
		}
		state, err := e.Forge.PRState(ctx, n)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("maintainer digest PR #%d: %v", n, err))
		}
		open := err == nil && strings.EqualFold(state, "open")
		openPRs[n] = open
		return open
	}
	approvalSeen := map[string]bool{}
	approval := func(item, pr, slot, issue string) {
		if covered(item, pr, slot) {
			return
		}
		n, ok := worker.PRRefNumber(pr)
		key := "slot:" + slot
		g := gateBySlot[slot]
		if ok {
			key = fmt.Sprintf("PR #%d", n)
			g = gateByPR[n]
			item = key
		} else if slot == "" || worker.NormID(g.Issue) != worker.NormID(issue) {
			return // never attach a previous assignment's refusal to this item
		}
		if approvalSeen[key] || g.Kind != ledger.KindGate || g.DetailStr("verdict") != worker.GateApprovalRequired {
			return
		}
		approvalSeen[key] = true
		for _, esc := range reg.Escalations() {
			same := (ok && esc.Kind == "pr" && esc.Number == n) || (!ok && esc.Kind == "issue" && worker.NormID(esc.IssueKey().String()) == worker.NormID(issue))
			if same && strings.HasPrefix(esc.Title, "Merge approval: ") && esc.Status == worker.EscalationAnswered && esc.Answer != nil {
				answered, err := time.Parse(time.RFC3339, esc.Answer.SeenAt)
				if err == nil && !answered.Before(g.TS) {
					return
				}
			}
		}
		if !openPR(pr) {
			return
		}
		out = append(out, MaintainerWait{"merge-approval", item, "Approve this merge?", waitStamp(g.TS), "hold"})
	}
	for _, s := range reg.Slots() {
		if s.PRMerged() || s.HeldID() == "" {
			continue
		}
		item := tracker.RefOf(s.HeldID())
		approval(item, s.PR(), s.Name(), s.HeldID())
	}
	for _, q := range reg.PRs() {
		item := tracker.RefOf(q.Issue)
		approval(item, q.PR, "", q.Issue)

	}
	if e.Forge != nil && e.NeedsHuman != "" {
		issues, err := e.Forge.List(ctx, tracker.ListFilter{State: "open", Labels: []string{e.NeedsHuman}})
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("maintainer digest needs-human: %v", err))
		} else {
			for _, issue := range issues {
				item := issue.Key.Ref()
				if !escalated[item] {
					out = append(out, MaintainerWait{e.NeedsHuman, item, "Resolve the parked item and clear " + e.NeedsHuman, parkedAt[worker.NormID(issue.Key.String())], "hold"})
				}
			}
		}
	}
	if e.Board != nil {
		claims, scanWarnings := e.StaleClaims(ctx, root, e.Board, nil)
		warnings = append(warnings, scanWarnings...)
		for _, claim := range claims {
			item := tracker.RefOf(claim.Issue)
			if !escalated[item] {
				out = append(out, MaintainerWait{StaleClaimKind, item, "Approve reclaiming stale claim " + claim.Holder + "?", waitStamp(claim.Last.Add(claim.Threshold)), "hold"})
			}
		}
	}
	run := e.Git
	if run == nil {
		run = git.Exec
	}
	bugs, err := bugreport.Open(ctx, run, root)
	if err != nil {
		warnings = append(warnings, fmt.Sprintf("maintainer digest bugreports: %v", err))
	} else {
		for _, bug := range bugs {
			out = append(out, MaintainerWait{"bugreport", bug.ID, "Approve filing rota bugreport: " + bug.Summary, bug.TS, "hold"})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		// Known oldest waits first; missing timestamps sort last.
		if (out[i].Since == "") != (out[j].Since == "") {
			return out[i].Since != ""
		}
		return out[i].Since < out[j].Since
	})
	return out, warnings
}

func waitStamp(at time.Time) string {
	if at.IsZero() {
		return ""
	}
	return at.UTC().Format(time.RFC3339)
}
