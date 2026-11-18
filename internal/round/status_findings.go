package round

import (
	"context"
	"fmt"
	"time"

	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/stamp"
	"github.com/l4ci/rota/internal/strutil"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// statusNotes preserves read diagnostics in encounter order. A family owns
// these notes; only Status applies them to the assembled report.
type statusNote struct{ source, message string }
type statusNotes []statusNote

func (n *statusNotes) warn(message string) { *n = append(*n, statusNote{message: message}) }
func (n *statusNotes) unavailable(source, message string) {
	*n = append(*n, statusNote{source: source, message: message})
}
func (r *Report) applyStatusNotes(notes statusNotes) {
	for _, note := range notes {
		if note.source != "" {
			r.unavailable(note.source, note.message)
		} else {
			r.Warnings = append(r.Warnings, note.message)
		}
	}
}

// hostFindings matches native agents to rows and returns host drift. It needs
// no forge or registry read; rows retain their native state for later families.
func (e Env) hostFindings(ctx context.Context, root string, rows []*Row, views map[string]*view, slotObj map[string]*worker.Slot, agents []host.Agent, hostOK bool) ([]*Row, []Finding, map[string]bool, map[string]bool) {
	var findings []Finding
	claimed := map[int]bool{}
	alive := map[string]bool{}       // slots the host shows an agent or window for
	sameSession := map[string]bool{} // a completion belongs to this exact host session
	for _, r := range rows {
		if !hostOK {
			break
		}
		if r.HostState == ExternalHost { // adopted work has no agent in the host
			continue
		}
		wt := views[r.Name].worktree
		// A parked slot owns no agent: one still running in its worktree is a
		// leftover, reported as unclaimed rather than absorbed by the slot.
		if r.Tab == "" && (r.Branch == "" || worker.IsOwnPark(r.Branch, r.Name)) {
			wt = ""
		}
		if i := matchAgent(agents, r.Name, r.Tab, wt); i >= 0 {
			claimed[i] = true
			alive[r.Name] = true
			if s := slotObj[r.Name]; s != nil {
				sameSession[r.Name] = s.PaneHandle() != "" && s.PaneHandle() == agents[i].Handle()
			}
			r.Agent, r.HostState = agents[i].Name, agents[i].Status
			if v := worker.JudgeLiveness(worker.LivenessEvidence{Native: r.HostState}); v.State == worker.LivenessUnverifiable {
				r.HostState, r.Evidence = v.State, v.Reason
			}
			// An internal tool wait can look blocked to the host. Only
			// harnesses with that vocabulary need a pane read; ordinary
			// snapshots and other harnesses keep their existing state.
			if r.HostState == "blocked" && e.ReadPane != nil && worker.ReadsWaitPane(r.Kind) {
				pane, _ := e.ReadPane(ctx, r.Name, agents[i].Handle(), 60)
				if st, ev := worker.ClassifyFor(r.Kind, pane, false, 60, r.HostState); st == worker.StateBusy {
					r.HostState, r.Evidence = "working", ev
				}
			}
			if r.Tab == "" {
				r.Tab = agents[i].Tab
			} else if cur := agents[i].Handle(); cur != "" && cur != r.Tab && agents[i].Name == host.AgentName(r.Name, r.Tab) && r.Registered {
				// Found by the name it was started under, under a handle that
				// is not the recorded one: the pane was moved.
				detail := fmt.Sprintf("recorded tab %s, pane is now in tab %s", r.Tab, agents[i].Tab)
				if agents[i].Tab == r.Tab {
					detail = fmt.Sprintf("recorded tab %s now holds other panes; pane %s is not alone in it", r.Tab, agents[i].Pane)
				}
				findings = append(findings, Finding{Kind: TabMoved, Slot: r.Name, Detail: detail, Repair: "record " + cur, from: r.Tab, to: cur})
			}
		} else if r.Registered && r.Tab != "" {
			// A runtime restart can orphan Orca handles while their processes
			// survive. Never offer the destructive dead-tab repair for absence.
			if v := e.absence(slotObj[r.Name]); v.State == worker.LivenessUnverifiable {
				r.HostState, r.Evidence = v.State, v.Reason
				continue
			}
			findings = append(findings, Finding{Kind: DeadTab, Slot: r.Name, Detail: fmt.Sprintf("host has no live agent for tab %s", r.Tab), Repair: "clear handle, set state dead"})
		}
	}
	if hostOK {
		for i, a := range agents {
			if claimed[i] || !inWorktreeRoot(root, a.Cwd) || inExternal(rows, views, a.Cwd) {
				continue
			}
			name := strutil.FirstNonEmpty(a.Name, a.Tab)
			rows = append(rows, &Row{Name: name, Agent: a.Name, Tab: a.Tab, HostState: a.Status})
			findings = append(findings, Finding{Kind: UnclaimedTab, Slot: name, Detail: fmt.Sprintf("live agent in %s matches no slot", a.Cwd)})
		}
	}

	return rows, findings, alive, sameSession
}

// slotFindings reads PR evidence for assembled rows, returning slot drift.
func (e Env) slotFindings(ctx context.Context, root string, rows []*Row, views map[string]*view, slotObj map[string]*worker.Slot, fs forgeState) ([]Finding, map[string]bool, statusNotes) {
	var findings []Finding
	var notes statusNotes
	prs, labelled, forgeOK, labelsOK := fs.prs, fs.labelled, fs.ok, fs.labelsOK
	held := map[string]bool{}
	for _, r := range rows {
		v := views[r.Name]
		if v == nil { // unclaimed tab
			continue
		}
		parked := r.Branch == "" || worker.IsOwnPark(r.Branch, r.Name) || r.Branch == v.base
		if r.Issue != "" {
			held[worker.NormID(r.Issue)] = true
		}
		if so := slotObj[r.Name]; so != nil { // a bundled slot holds its members too
			for _, m := range so.Bundle() {
				held[worker.NormID(m)] = true
			}
		}
		if !r.Registered {
			findings = append(findings, Finding{Kind: UnregisteredWorktree, Slot: r.Name, Detail: fmt.Sprintf("%s has no slot in .rota/workers.json", v.worktree), Repair: "register the slot"})
		}
		if forgeOK && !parked {
			if pr := prs[r.Branch]; pr != nil {
				v.openPR = pr
				r.PRTitle = pr.Title
				r.PRState = "open"
				if r.PR == "" {
					r.PR = pr.URL
					if r.Registered {
						findings = append(findings, Finding{Kind: PRUnrecorded, Slot: r.Name, Issue: r.Issue, Detail: fmt.Sprintf("open PR #%d has branch %s as head, slot records none", pr.Number, r.Branch), Repair: "record pr"})
					}
				} else if n, ok := worker.PRRefNumber(r.PR); ok && n != pr.Number {
					// An open PR for the branch does not prove the recorded one
					// was superseded: a merged PR must keep its record.
					st, err := e.Forge.PRState(ctx, n)
					if err != nil {
						v.prErr = true
						notes.unavailable(SourceForge, fmt.Sprintf("PR #%d state: %v", n, err))
					} else if st == "closed" {
						findings = append(findings, Finding{Kind: PRSuperseded, Slot: r.Name, Issue: r.Issue, Detail: fmt.Sprintf("slot records closed unmerged PR #%d; open PR #%d has branch %s as head", n, pr.Number, r.Branch), Repair: "record open pr"})
					}
				}
			} else if n, ok := e.adoptedPRNumber(ctx, root, &notes, v, r); ok {
				st, err := e.Forge.PRState(ctx, n)
				if err != nil {
					notes.warn(fmt.Sprintf("PR #%d state: %v", n, err))
					v.prErr = true
				} else {
					r.PRState = st
					if st == "merged" && r.HostState == ExternalHost { // the gate releases it; so does reconcile --apply
						findings = append(findings, Finding{Kind: MergedExternal, Slot: r.Name, Issue: r.Issue, Detail: fmt.Sprintf("PR #%d is merged and the adopted slot is still registered", n), Repair: "release the slot"})
					}
					if (st == "merged" || st == "closed") && r.HostState != ExternalHost { // release, not park, ends an external slot
						f := Finding{Kind: PRStale, Slot: r.Name, Issue: r.Issue, Detail: fmt.Sprintf("PR #%d is %s and the slot still holds %s", n, st, r.Branch)}
						if st == "merged" && r.Registered { // a closed PR may hold unmerged work: report only
							f.Repair = "park the slot"
						}
						findings = append(findings, f)
					}
				}
			} else {
				ahead := e.ahead(ctx, root, v.base, r.Branch)
				if m := e.unrecordedMergedPR(ctx, root, &notes, v, r, slotObj[r.Name]); m != nil {
					v.mergedPR = m
					r.PR = m.URL
					findings = append(findings, Finding{Kind: PRUnrecorded, Slot: r.Name, Issue: r.Issue, Detail: fmt.Sprintf("merged PR #%d has branch %s as head, slot records none", m.Number, r.Branch), Repair: "record pr"})
				} else if ahead > 0 && slotFinished(r, slotObj[r.Name]) {
					findings = append(findings, Finding{Kind: BranchNoPR, Slot: r.Name, Issue: r.Issue, Detail: fmt.Sprintf("%s is %d commit(s) ahead of %s with no PR", r.Branch, ahead, v.base)})
				}
			}
		}
		if forgeOK && parked && r.PRState == "" {
			// A parked slot (the transfer to the human parks it) still records
			// its PR: read its state so the watch can mark a merged one.
			if n, ok := worker.PRRefNumber(r.PR); ok {
				if st, err := e.Forge.PRState(ctx, n); err != nil {
					notes.warn(fmt.Sprintf("PR #%d state: %v", n, err))
					v.prErr = true
				} else {
					r.PRState = st
				}
			}
		}
		if labelsOK && !parked && r.Issue != "" && r.PRState != "merged" && r.PRState != "closed" {
			if n, ok := tracker.ParseKey(r.Issue); ok && !labelled[n] && !e.issueClosed(ctx, &notes, n) {
				findings = append(findings, Finding{Kind: LabelMissing, Slot: r.Name, Issue: r.Issue, Detail: fmt.Sprintf("slot holds %s, which lacks %s", n.Ref(), e.Label), Repair: "add " + e.Label})
			}
		}
	}
	return findings, held, notes
}

// queuedFindings reads only the PR queue and forge; queued issues count as held.
func queuedFindings(ctx context.Context, forge Forge, forgeOK bool, queued []worker.QueuedPR, held map[string]bool) ([]QueuedPR, []Finding, statusNotes) {
	var out []QueuedPR
	var findings []Finding
	var notes statusNotes
	for _, q := range queued {
		out = append(out, QueuedPR{Issue: q.Issue, PR: q.PR, Branch: q.Branch, From: q.From, Bundle: q.Bundle})
		if id := queuedIssue(q); id != "" {
			held[worker.NormID(id)] = true
		}
		for _, m := range q.Bundle {
			held[worker.NormID(m)] = true
		}
		if n, ok := worker.PRRefNumber(q.PR); ok && forgeOK {
			st, err := forge.PRState(ctx, n)
			if err == nil {
				out[len(out)-1].State = st
			}
			switch {
			case err != nil:
				notes.warn(fmt.Sprintf("PR #%d state: %v", n, err))
			case st == "merged" || st == "closed":
				findings = append(findings, Finding{Kind: PRStale, Issue: q.Issue, Detail: fmt.Sprintf("PR #%d in review is %s", n, st), Repair: "drop it from review", pr: q.PR})
			case q.Branch != "":
				// A record can pair a PR with another issue's branch (#648); the gate would read the wrong head.
				if info, err := forge.PRView(ctx, n); err == nil && info.Head != "" && info.Head != q.Branch {
					findings = append(findings, Finding{Kind: QueuedBranchMismatch, Issue: q.Issue, Detail: fmt.Sprintf("queued record names branch %s but PR #%d is headed by %s; drop the record and re-queue it with the right branch", q.Branch, n, info.Head)})
				}
			}
		}
	}
	return out, findings, notes
}

// workflowLabelFindings needs only label and ownership snapshots.
func workflowLabelFindings(label string, stale []tracker.IssueKey, labelled map[tracker.IssueKey]bool, labelsOK bool, held map[string]bool) []Finding {
	var findings []Finding
	tracker.SortKeys(stale)
	for _, n := range stale {
		findings = append(findings, Finding{Kind: LabelStale, Issue: n.String(), Detail: fmt.Sprintf("%s is closed and still has %s or claimed: labels", n.Ref(), label), Repair: "remove workflow and claim labels"})
	}
	if labelsOK {
		var orphans []tracker.IssueKey
		for n := range labelled {
			if !held[n.String()] {
				orphans = append(orphans, n)
			}
		}
		tracker.SortKeys(orphans)
		for _, n := range orphans {
			findings = append(findings, Finding{Kind: LabelOrphan, Issue: n.String(), Detail: fmt.Sprintf("%s has %s and no slot holds it", n.Ref(), label)})
		}
	}

	return findings
}

// stalledFindings checks activity only for alive, unfinished slots.
func (e Env) stalledFindings(ctx context.Context, rows []*Row, views map[string]*view, slotObj map[string]*worker.Slot, hostOK bool, alive, waiting map[string]bool) []Finding {
	var findings []Finding
	now := e.Worker.Now
	if hostOK {
		for _, r := range rows {
			s := slotObj[r.Name]
			// Alive as Health counts it: a tmux window carries no agent name.
			if s == nil || r.Issue == "" || !alive[r.Name] {
				continue
			}
			// A done report that is not older than the last state change is the
			// worker waiting on the orchestrator, not a worker that stopped.
			if ev := s.Event(); ev != nil && ev.Kind == worker.EventDone && !evBefore(ev.TS, s.ActiveAt()) {
				continue
			}
			// A finished slot (done or idle) has handed its PR over and waits on review.
			if slotFinished(r, s) {
				continue
			}
			v := views[r.Name]
			st := e.Stalled(ctx, StallInput{
				Worktree: v.worktree, Base: v.base, Holds: true, Alive: true,
				Escalated: waiting[r.Name], ActiveAt: s.LastAlive(), Minutes: e.Round.StallMinutes,
			}, now())
			if st.Stalled {
				findings = append(findings, Finding{Kind: StalledSlot, Slot: r.Name, Issue: r.Issue,
					Detail: fmt.Sprintf("nothing moved for %d min (last: %s)", int(st.Idle.Minutes()), st.Signal)})
			}
		}
	}
	return findings
}

// itemTimeoutFindings compares assignment clocks without a host or forge.
func itemTimeoutFindings(rows []*Row, minutes int, itemStart func(string) (time.Time, bool), now func() time.Time) []Finding {
	var findings []Finding
	if minutes > 0 {
		limit := time.Duration(minutes) * time.Minute
		for _, r := range rows {
			if r.Issue == "" || !r.Registered || r.HostState == ExternalHost {
				continue
			}
			if at, ok := itemStart(r.Issue); ok && now().Sub(at) >= limit {
				findings = append(findings, Finding{Kind: ItemTimeout, Slot: r.Name, Issue: r.Issue,
					Detail: fmt.Sprintf("running %d min since first assignment (round.itemTimeoutMinutes is %d)", int(now().Sub(at).Minutes()), minutes),
					Repair: "park as needs-human"})
			}
		}
	}
	return findings
}

// staleClaimFindings formats the already-derived claim activity verdicts.
func staleClaimFindings(staleClaims []StaleClaim, now func() time.Time) []Finding {
	var findings []Finding
	for _, claim := range staleClaims {
		detail := fmt.Sprintf("claim %s on %s has had no push or PR activity since %s", claim.Holder, tracker.RefOf(claim.Issue), stamp.Format(claim.Last))
		detail += fmt.Sprintf(" (%s > %s)", now().Sub(claim.Last).Truncate(time.Minute), claim.Threshold)
		findings = append(findings, Finding{Kind: StaleClaimKind, Issue: claim.Issue, Detail: detail})
	}
	return findings
}
