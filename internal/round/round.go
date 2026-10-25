// Package round rebuilds a round's state from what is on disk and in the
// outside world: the worker registry (.rota/workers.json), the git worktrees
// under .worktrees/, the host's live agents, the forge's open PRs and the
// issues carrying the in-progress label. It reports where those disagree
// (drift) and, on request, makes the safe repairs.
//
// It owns no state file. A round that was started with `herdr worktree
// create` has no registry at all, so the worktrees are the roster and the
// registry only enriches a row.
package round

import (
	"cmp"
	"context"
	"fmt"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/strutil"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/escalation"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/limits"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/roundlease"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// Drift kinds.
const (
	DeadTab              = "dead-tab"
	UnclaimedTab         = "unclaimed-tab"
	UnregisteredWorktree = "unregistered-worktree"
	BranchNoPR           = "branch-no-pr"
	PRUnrecorded         = "pr-unrecorded"
	PRStale              = "pr-stale"
	LabelMissing         = "label-missing"
	LabelOrphan          = "label-orphan"
	LabelStale           = "label-stale"
	StalledSlot          = "stalled"
	ItemTimeout          = "item-timeout"
	ClaimMismatch        = "claim-mismatch"
	UnregisteredBranch   = "unregistered-branch"
	// Bounced: on the support machine, a queued PR main sent back (#1598).
	Bounced              = "bounced"
	MergedExternal       = "merged-external"
	QueuedBranchMismatch = "queued-branch-mismatch"
	// LeaseStale is declared in lease.go.
)

// Source names, as `unavailable` lists them.
const (
	SourceHost  = "host"
	SourceForge = "forge"
)

// NeedsInfoLabel is the label `/rota-capture --triage` puts on an issue it
// found too unclear to build. A constant: triage writes it literally.
const NeedsInfoLabel = "needs-info"

// Forge is the part of the tracker adapter a round reads and, for the
// label repair, writes. tracker.Adapter satisfies it.
type Forge interface {
	OpenPRs(ctx context.Context) ([]tracker.PR, error)
	MergedPRs(ctx context.Context, branch string) ([]tracker.PR, error)
	PRState(ctx context.Context, pr int) (string, error)
	PRView(ctx context.Context, pr int) (tracker.PRInfo, error)
	ClosedNumbers(body string) []int
	List(ctx context.Context, f tracker.ListFilter) ([]tracker.Issue, error)
	Get(ctx context.Context, key tracker.IssueKey, withComments bool) (tracker.Issue, error)
	AddLabels(ctx context.Context, key tracker.IssueKey, labels []string, autoCreate bool) error
	RemoveLabels(ctx context.Context, key tracker.IssueKey, labels []string) error
}

// Env is what a round touches outside its own memory. A nil Snapshot or Forge
// means that source is unavailable; tests fill every field with fakes.
type Env struct {
	Git git.Runner
	// Snapshot lists the host's live agents; HostName is "herdr" or "tmux", or
	// "solo" with no Snapshot when the round has no terminal host.
	Snapshot func(ctx context.Context) ([]host.Agent, error)
	HostName host.Kind
	Forge    Forge
	// Base is the branch slots are cut from when a slot records none.
	Base  string
	Label string
	// HostErr and ForgeErr say why Snapshot or Forge is nil.
	HostErr, ForgeErr string
	// Lease reads the orchestrator lease; the zero value is the real process
	// table and host name.
	Lease roundlease.Env
	// Worker is every worker operation the round performs. It is the one owner
	// of the clock (Now, which dates timed-out escalations), the environment
	// reader (Getenv, for the lease, host and brief lookups) and Accounts, the
	// meter reader that quota and the ledger read; picking an account is
	// AccountPicking's job. Nil is not usable: build it with FromWorkerEnv.
	Worker Worker
	// Login reads a slot account's Claude login for the no-login check
	// (SlotLoginProblem). The zero value reads credentials files only, never the
	// macOS Keychain; baseRoundEnv sets the system one.
	Login worker.Credentials
	// AccountPicking turns the delivery's account step on: keep the slot's own
	// round.accounts account while it has headroom, else pick. Off is the zero
	// value; WithAccounts is the one way to turn it on, so a verb opts in by
	// name and Worker.Accounts alone (the meter read) never picks.
	AccountPicking bool
	// Board is the backlog's claim side; claim-mismatch drift needs it, and
	// without it (file mode) that kind is skipped.
	Board Board
	// Round is the round.* settings (and round.itemTimeoutMinutes) loaded with
	// the env: StallMinutes 0 turns the stalled check off, ItemTimeoutMinutes 0
	// the item-timeout check, an empty AdoptPattern the unregistered-branch
	// check; SharedPaths feeds the overlap pass of an adoption. RoundErr is why
	// they failed to load; Status refuses to run on it, so bad keys never
	// become zero values.
	Round    roundcfg.Settings
	RoundErr error
	// HolderPID names the lease holder for the item-timeout park; 0 discovers
	// it from the process, as the other verbs do.
	HolderPID int
	// NeedsHuman is issues.labels.needsHuman, the label of an issue handed to the
	// human (C10); config.Label supplies its default.
	NeedsHuman string
	// SupportLabel is issues.labels.support; config.Label supplies its default.
	SupportLabel string
	// NeedsReview is issues.labels.needsReview, the label of an issue whose PR
	// awaits review; config.Label supplies its default.
	NeedsReview string
}

// WithAccounts is the env with account picking on, reading the meters through
// a. It is the single owner of the choice: build the env this way before use,
// never flip it afterwards. A nil a leaves picking off, since the pick would
// have no meters to read.
func (e Env) WithAccounts(a *worker.Accounts) Env {
	e.Worker = e.Worker.WithAccounts(a)
	e.AccountPicking = a != nil
	return e
}

// getenv is the env's environment reader, from the worker env.
func (e Env) getenv() func(string) string { return e.Worker.Getenv }

// now is the env's clock, from the worker env.
func (e Env) now() time.Time { return e.Worker.Now() }

// Row is one line of `rota round status`.
type Row struct {
	Name  string
	Agent string
	Issue string
	// Bundle is the member issues the slot holds beside Issue, the lead.
	Bundle    []string
	Branch    string
	PR        string
	PRState   string
	HostState string
	// State is the derived state of an external slot (done, busy, idle,
	// unknown); "" for a slot a host drives, whose state is HostState.
	State      string
	Tab        string
	Registered bool
	Drift      []string
	// Escalations are the ids of the slot's open escalations.
	Escalations []string
	// Kind, Tier, Model and TierReason are the slot's C9 fields.
	Kind, KindSource, Tier, Model, TierReason string
	// Bounces is how often the slot's issue has been sent back (round.maxBounces
	// caps it); 0 when never.
	Bounces int
	// Draining marks a roster slot past the round's slot count that still
	// holds work: it takes nothing new (Members).
	Draining bool
	// Account is the round.accounts entry the slot last ran under ("" when it
	// has none) and AccountKind that entry's kind (claude, codex or opencode).
	Account, AccountKind string
	// NoLogin is why the slot's account has no usable Claude login ("" when it
	// has one): assign skips the slot and refuses to name it.
	NoLogin string
	// BestOf is the other attempt's slot when the row's issue is built best-of:2.
	BestOf string
	// PRTitle is the open PR's title ("" without one) and Evidence the last
	// evidence line `round wait` returned for the slot. Neither is in
	// `round status` Data; the --ui screen reads them.
	PRTitle, Evidence string
	// Event is the worker's standing event (`rota worker send`, done, reply),
	// nil when none stands. Its body is the row's Evidence: the worker's own
	// word reads ahead of what `round wait` last returned.
	Event *worker.Event
	// Outcome is the outcome the slot's worker reported through
	// `worker done --outcome` ("" when it reported none).
	Outcome string
	// Burn is the quota the slot has consumed, in percentage points of its
	// account's headroom since its assign; nil when unknown (codex, no meter). Only the --ui screen fills it; Data never carries it.
	Burn *float64
}

// inWorktreeRoot says whether cwd is <root>/.worktrees or inside it.
func inWorktreeRoot(root, cwd string) bool {
	return worker.PathContains(filepath.Join(root, worker.WorktreeRoot), cwd)
}

// ExternalHost is the HostState of an adopted slot: no host session runs it.
const ExternalHost = "external"

// inExternal says whether cwd is inside an adopted slot's worktree: whatever
// runs there is the other tool's, not an unclaimed rota tab.
func inExternal(rows []*Row, views map[string]*view, cwd string) bool {
	for _, r := range rows {
		if v := views[r.Name]; r.HostState == ExternalHost && v != nil && v.worktree != "" {
			if worker.PathContains(v.worktree, cwd) {
				return true
			}
		}
	}
	return false
}

// ExternalState derives an adopted slot's state from the forge and git, since
// no pane reports one: merged when its PR merged (the slot awaits release), done when its PR is open, busy when the branch is ahead
// of base with no PR, idle otherwise, unknown while the forge cannot answer.
func (e Env) ExternalState(ctx context.Context, root string, forgeOK bool, base string, r *Row) string {
	switch {
	case !forgeOK:
		return "unknown"
	case r.PRState == "merged":
		return "merged"
	case r.PRState == "open":
		return "done"
	case r.PRState == "" && r.Branch != "" && e.ahead(ctx, root, base, r.Branch) > 0:
		return "busy"
	}
	return "idle"
}

// adoptedPRNumber is the number of the PR a row's checks run on: the one it
// records, else, for an adopted slot that recorded none and has no open PR, the
// merged PR its branch headed. That PR opened and merged between two looks at
// the open list, so only a branch lookup finds it; the row then carries it and
// the merged-external finding releases the slot.
func (e Env) adoptedPRNumber(ctx context.Context, root string, rep *Report, v *view, r *Row) (int, bool) {
	if n, ok := worker.PRRefNumber(r.PR); ok {
		return n, true
	}
	if r.HostState != ExternalHost || r.PR != "" {
		return 0, false
	}
	merged, err := e.Forge.MergedPRs(ctx, r.Branch)
	if err != nil {
		rep.Warnings = append(rep.Warnings, fmt.Sprintf("merged PRs of %s: %v", r.Branch, err))
		v.prErr = true
		return 0, false
	}
	for _, m := range merged {
		if e.ownsMergedPR(ctx, root, m.HeadSHA, v.base, r.Branch) {
			r.PR = m.URL
			return m.Number, true
		}
	}
	return 0, false
}

// unrecordedMergedPR is the merged PR a registered, finished slot holding an
// issue should record but does not, nil when the slot records one or the forge
// shows none. The forge is asked only of a done or idle slot (a working slot's
// missing PR is the normal state). A merge commit leaves the branch tip inside
// base, so how far ahead the branch is cannot gate the lookup; a failed lookup
// warns and sets v.prErr, so --apply skips the repair.
func (e Env) unrecordedMergedPR(ctx context.Context, root string, rep *Report, v *view, r *Row, so *worker.Slot) *tracker.PR {
	if !r.Registered || so == nil || r.Issue == "" || r.PR != "" || !worker.Parkable(strutil.FirstNonEmpty(r.HostState, so.State())) {
		return nil
	}
	m, err := e.mergedBranchPR(ctx, root, r.Branch, v.base)
	if err != nil {
		rep.Warnings = append(rep.Warnings, fmt.Sprintf("merged PRs of %s: %v", r.Branch, err))
		v.prErr = true
	}
	return m
}

// ownsMergedPR says whether a merged PR is this slot's: the forge matches a
// head branch by name alone, so an older PR from a reused name (or a fork's
// same-named branch) is told apart by its commits. Being reachable from the
// branch is not enough: a branch recreated from base contains every PR base
// ever merged. The PR is ours when the branch tip is its head, or its head is
// on the branch but not yet in base (a squash merge leaves it out). A PR without
// a head sha, or a sha git does not have, proves nothing and is not ours.
func (e Env) ownsMergedPR(ctx context.Context, root, sha, base, branch string) bool {
	if sha == "" {
		return false
	}
	tip, err := e.Git(ctx, root, "rev-parse", "--verify", "--quiet", branch+"^{commit}")
	if err != nil || tip.ExitCode != 0 {
		return false
	}
	if strings.TrimSpace(tip.Stdout) == sha {
		return true
	}
	onBranch, err := e.Git(ctx, root, "merge-base", "--is-ancestor", sha, branch)
	if err != nil || onBranch.ExitCode != 0 {
		return false
	}
	// Status never fetches, so origin/<base> can run ahead of local <base>: the
	// head counts as in base when either holds it.
	checked := false
	for _, ref := range []string{base, "origin/" + base} {
		if ok, _ := e.Git(ctx, root, "rev-parse", "--verify", "-q", ref); ok.ExitCode != 0 {
			continue
		}
		in, err := e.Git(ctx, root, "merge-base", "--is-ancestor", sha, ref)
		if err != nil || in.ExitCode != 1 {
			return false
		}
		checked = true
	}
	return checked
}

// Finding is one drift. Repair names what Reconcile(apply) would do and is
// empty for kinds that are never repaired.
type Finding struct {
	Kind, Slot, Issue, Detail, Repair string
	// pr is the registry's ref of the queued PR a pr-stale finding is about,
	// so its repair drops that record and not the issue's other attempt's.
	pr string
	// branch is the branch an unregistered-branch finding is about.
	branch string
	// support marks an unregistered-branch finding of the support machine's
	// (#1598): its pr is the open PR to adopt with, and adopting fetches it.
	support bool
}

// Key names what the finding is about, for a caller that tells findings apart:
// the slot, else the branch, else the issue.
func (f Finding) Key() string {
	switch {
	case f.Slot != "":
		return f.Slot
	case f.branch != "":
		return f.branch
	}
	return tracker.RefOf(f.Issue)
}

// Report is the assembled state.
type Report struct {
	Rows        []Row
	Findings    []Finding
	Unavailable []string
	Warnings    []string
	Host        host.Kind
	// Escalations are the open ones (pending or timed-out), read from the
	// registry without a forge call; `rota round escalate check` looks for answers.
	Escalations []escalation.Report
	// Queued are the PRs waiting for review or merge whose slot moved on.
	Queued []QueuedPR
	// Limits are the usage-limit entries still waiting (D3), read from the
	// registry like escalations.
	Limits []limits.Entry

	views map[string]*view
	// openPRs is the forge's open PRs by head branch, nil when the forge was
	// not read; the support handoff scan reuses it.
	openPRs map[string]*tracker.PR
}

// QueuedPR is one record of the registry's PR queue.
type QueuedPR struct {
	Issue, PR, Branch, From string
	Bundle                  []string // the member issues beside Issue
	// State is the PR's forge state (open, merged, closed), "" when it was not
	// read. Only the --ui overview shows it.
	State string
}

// view is a row plus what repairs need.
type view struct {
	worktree string
	base     string
	openPR   *tracker.PR
	mergedPR *tracker.PR // merged PR the branch owns, for a slot that records none
	prErr    bool        // reading the slot's PR state failed
}

var ()

type worktree struct{ path, name, branch string }

// roundCfgErr is RoundErr as a usage error, so a bad round.* key is the user's
// to fix and never reads as a rota bug; nil when the settings loaded.
func (e Env) roundCfgErr() error {
	if e.RoundErr == nil {
		return nil
	}
	return &exitcode.Error{Exit: exitcode.ExitUsage, Message: e.RoundErr.Error()}
}

// Status assembles the round. It fails when git does, and with a usage error
// (exit 2) when the round.* keys did not load; an unavailable host or forge is
// reported in the result.
func (e Env) Status(ctx context.Context, root string) (*Report, error) {
	if err := e.roundCfgErr(); err != nil {
		return nil, err
	}
	rep := &Report{views: map[string]*view{}}
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	wts, err := e.worktrees(ctx, root)
	if err != nil {
		return nil, err
	}
	reg, err := worker.LoadRegistry(root)
	if err != nil {
		return nil, err
	}

	rows, slotObj := e.readRows(wts, reg, rep)
	if len(e.Round.Roster) > 0 {
		_, draining := Members(reg, e.Round)
		for _, r := range rows {
			r.Draining = slices.Contains(draining, r.Name)
		}
	}

	// A draining slot takes nothing new, so its login is not read. Slots on
	// one account share a config dir, so each dir is read once per call.
	loginByDir := map[string]string{}
	for _, r := range rows {
		s := reg.Slot(r.Name)
		if r.Draining || s == nil {
			continue
		}
		key := s.Kind() + "\x00" + s.ConfigDir()
		why, seen := loginByDir[key]
		if !seen {
			why = e.SlotLoginProblem(s)
			loginByDir[key] = why
		}
		r.NoLogin = why
	}

	agents, hostOK := e.readHost(ctx, rep)
	claimed := map[int]bool{}
	alive := map[string]bool{} // slots the host shows an agent or window for
	for _, r := range rows {
		if !hostOK {
			break
		}
		if r.HostState == ExternalHost { // adopted work has no agent in the host
			continue
		}
		wt := rep.views[r.Name].worktree
		// A parked slot owns no agent: one still running in its worktree is a
		// leftover, reported as unclaimed rather than absorbed by the slot.
		if r.Tab == "" && (r.Branch == "" || worker.IsOwnPark(r.Branch, r.Name)) {
			wt = ""
		}
		if i := matchAgent(agents, r.Name, r.Tab, wt); i >= 0 {
			claimed[i] = true
			alive[r.Name] = true
			r.Agent, r.HostState = agents[i].Name, agents[i].Status
			if r.Tab == "" {
				r.Tab = agents[i].Tab
			}
		} else if r.Registered && r.Tab != "" {
			rep.add(Finding{Kind: DeadTab, Slot: r.Name, Detail: fmt.Sprintf("host has no live agent for tab %s", r.Tab), Repair: "clear handle, set state dead"})
		}
	}
	if hostOK {
		for i, a := range agents {
			if claimed[i] || !inWorktreeRoot(root, a.Cwd) || inExternal(rows, rep.views, a.Cwd) {
				continue
			}
			name := strutil.FirstNonEmpty(a.Name, a.Tab)
			rows = append(rows, &Row{Name: name, Agent: a.Name, Tab: a.Tab, HostState: a.Status})
			rep.add(Finding{Kind: UnclaimedTab, Slot: name, Detail: fmt.Sprintf("live agent in %s matches no slot", a.Cwd)})
		}
	}

	fs := e.readForge(ctx, rep)
	prs, labelled, stale, forgeOK, labelsOK := fs.prs, fs.labelled, fs.stale, fs.ok, fs.labelsOK
	if forgeOK {
		rep.openPRs = prs
		e.issueBounces(root, rows, rep)
	}

	held := map[string]bool{}
	for _, r := range rows {
		v := rep.views[r.Name]
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
			rep.add(Finding{Kind: UnregisteredWorktree, Slot: r.Name, Detail: fmt.Sprintf("%s has no slot in .rota/workers.json", v.worktree), Repair: "register the slot"})
		}
		if forgeOK && !parked {
			if pr := prs[r.Branch]; pr != nil {
				v.openPR = pr
				r.PRTitle = pr.Title
				r.PRState = "open"
				if r.PR == "" {
					r.PR = pr.URL
					if r.Registered {
						rep.add(Finding{Kind: PRUnrecorded, Slot: r.Name, Issue: r.Issue, Detail: fmt.Sprintf("open PR #%d has branch %s as head, slot records none", pr.Number, r.Branch), Repair: "record pr"})
					}
				}
			} else if n, ok := e.adoptedPRNumber(ctx, root, rep, v, r); ok {
				st, err := e.Forge.PRState(ctx, n)
				if err != nil {
					rep.Warnings = append(rep.Warnings, fmt.Sprintf("PR #%d state: %v", n, err))
					v.prErr = true
				} else {
					r.PRState = st
					if st == "merged" && r.HostState == ExternalHost { // the gate releases it; so does reconcile --apply
						rep.add(Finding{Kind: MergedExternal, Slot: r.Name, Issue: r.Issue, Detail: fmt.Sprintf("PR #%d is merged and the adopted slot is still registered", n), Repair: "release the slot"})
					}
					if (st == "merged" || st == "closed") && r.HostState != ExternalHost { // release, not park, ends an external slot
						f := Finding{Kind: PRStale, Slot: r.Name, Issue: r.Issue, Detail: fmt.Sprintf("PR #%d is %s and the slot still holds %s", n, st, r.Branch)}
						if st == "merged" && r.Registered { // a closed PR may hold unmerged work: report only
							f.Repair = "park the slot"
						}
						rep.add(f)
					}
				}
			} else {
				ahead := e.ahead(ctx, root, v.base, r.Branch)
				if m := e.unrecordedMergedPR(ctx, root, rep, v, r, slotObj[r.Name]); m != nil {
					v.mergedPR = m
					r.PR = m.URL
					rep.add(Finding{Kind: PRUnrecorded, Slot: r.Name, Issue: r.Issue, Detail: fmt.Sprintf("merged PR #%d has branch %s as head, slot records none", m.Number, r.Branch), Repair: "record pr"})
				} else if ahead > 0 {
					rep.add(Finding{Kind: BranchNoPR, Slot: r.Name, Issue: r.Issue, Detail: fmt.Sprintf("%s is %d commit(s) ahead of %s with no PR", r.Branch, ahead, v.base)})
				}
			}
		}
		if labelsOK && !parked && r.Issue != "" && r.PRState != "merged" && r.PRState != "closed" {
			if n, ok := tracker.ParseKey(r.Issue); ok && !labelled[n] && !e.issueClosed(ctx, rep, n) {
				rep.add(Finding{Kind: LabelMissing, Slot: r.Name, Issue: r.Issue, Detail: fmt.Sprintf("slot holds %s, which lacks %s", n.Ref(), e.Label), Repair: "add " + e.Label})
			}
		}
	}
	for _, q := range reg.PRs() {
		rep.Queued = append(rep.Queued, QueuedPR{Issue: q.Issue, PR: q.PR, Branch: q.Branch, From: q.From, Bundle: q.Bundle})
		if id := queuedIssue(q); id != "" {
			held[worker.NormID(id)] = true
		}
		for _, m := range q.Bundle {
			held[worker.NormID(m)] = true
		}
		if n, ok := worker.PRRefNumber(q.PR); ok && forgeOK {
			st, err := e.Forge.PRState(ctx, n)
			if err == nil {
				rep.Queued[len(rep.Queued)-1].State = st
			}
			switch {
			case err != nil:
				rep.Warnings = append(rep.Warnings, fmt.Sprintf("PR #%d state: %v", n, err))
			case st == "merged" || st == "closed":
				rep.add(Finding{Kind: PRStale, Issue: q.Issue, Detail: fmt.Sprintf("PR #%d in review is %s", n, st), Repair: "drop it from review", pr: q.PR})
			case q.Branch != "":
				// A record can pair a PR with another issue's branch (#648); the gate would read the wrong head.
				if info, err := e.Forge.PRView(ctx, n); err == nil && info.Head != "" && info.Head != q.Branch {
					rep.add(Finding{Kind: QueuedBranchMismatch, Issue: q.Issue, Detail: fmt.Sprintf("queued record names branch %s but PR #%d is headed by %s; drop the record and re-queue it with the right branch", q.Branch, n, info.Head)})
				}
			}
		}
	}
	tracker.SortKeys(stale)
	for _, n := range stale {
		rep.add(Finding{Kind: LabelStale, Issue: n.String(), Detail: fmt.Sprintf("%s is closed and still has %s", n.Ref(), e.Label), Repair: "remove " + e.Label})
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
			rep.add(Finding{Kind: LabelOrphan, Issue: n.String(), Detail: fmt.Sprintf("%s has %s and no slot holds it", n.Ref(), e.Label)})
		}
	}

	now := e.Worker.Now
	waiting := map[string]bool{}
	for _, x := range escalation.Load(root) {
		if x.Status == escalation.StatusPending && x.Slot != "" {
			waiting[x.Slot] = true
		}
	}
	if hostOK {
		for _, r := range rows {
			s := slotObj[r.Name]
			// Alive as Health counts it: a tmux window carries no agent name.
			if s == nil || r.Issue == "" || !alive[r.Name] {
				continue
			}
			v := rep.views[r.Name]
			st := e.Stalled(ctx, StallInput{
				Worktree: v.worktree, Base: v.base, Holds: true, Alive: true,
				Escalated: waiting[r.Name], ActiveAt: s.LastAlive(), Minutes: e.Round.StallMinutes,
			}, now())
			if st.Stalled {
				rep.add(Finding{Kind: StalledSlot, Slot: r.Name, Issue: r.Issue,
					Detail: fmt.Sprintf("nothing moved for %d min (last: %s)", int(st.Idle.Minutes()), st.Signal)})
			}
		}
	}
	if e.Round.ItemTimeoutMinutes > 0 {
		limit := time.Duration(e.Round.ItemTimeoutMinutes) * time.Minute
		for _, r := range rows {
			if r.Issue == "" || !r.Registered || r.HostState == ExternalHost {
				continue
			}
			if at, ok := reg.ItemStart(r.Issue); ok && now().Sub(at) >= limit {
				rep.add(Finding{Kind: ItemTimeout, Slot: r.Name, Issue: r.Issue,
					Detail: fmt.Sprintf("running %d min since first assignment (round.itemTimeoutMinutes is %d)", int(now().Sub(at).Minutes()), e.Round.ItemTimeoutMinutes),
					Repair: "park as needs-human"})
			}
		}
	}
	e.claimFindings(ctx, rep, reg, rows, slotObj, reg.PRs(), labelled, labelsOK)

	e.leaseFinding(ctx, root, rep)

	for _, r := range rows {
		if r.HostState == ExternalHost {
			// Merged stays listed until the gate or reconcile --apply releases it.
			r.State = e.ExternalState(ctx, root, forgeOK && !rep.views[r.Name].prErr, rep.views[r.Name].base, r)
		}
		rep.Rows = append(rep.Rows, *r)
	}
	for _, f := range rep.Findings {
		for i := range rep.Rows {
			if f.Slot != "" && rep.Rows[i].Name == f.Slot {
				rep.Rows[i].Drift = append(rep.Rows[i].Drift, f.Kind)
			}
		}
	}
	for _, x := range escalation.Load(root) {
		if x.Status != escalation.StatusPending {
			continue
		}
		rep.Escalations = append(rep.Escalations, escalation.Report{Entry: x, Status: x.Derived(now())})
		for i := range rep.Rows {
			if x.Slot != "" && rep.Rows[i].Name == x.Slot {
				rep.Rows[i].Escalations = append(rep.Rows[i].Escalations, x.ID)
			}
		}
	}
	rep.Limits = limits.Waiting(limits.Load(root))
	return rep, nil
}

// issueBounces reads each row's bounce count from the issue when round.support
// keeps it there (#1598): the registry holds none then. A failed read leaves
// the registry's count and warns once.
func (e Env) issueBounces(root string, rows []*Row, rep *Report) {
	if e.Round.Support == "" {
		return
	}
	store, err := e.Worker.BounceStore(root)
	for _, r := range rows {
		if err != nil {
			break
		}
		if r.Issue != "" {
			r.Bounces, _, err = store.Count(r.Issue)
		}
	}
	if err != nil {
		rep.Warnings = append(rep.Warnings, fmt.Sprintf("bounce counts not read from the issues: %v", err))
	}
}

// readRows is the worktree and registry source: registry slots first, then
// the worktrees the registry lacks. It records each row's view on rep and
// returns the rows with the registry's slot objects by name.
func (e Env) readRows(wts []worktree, reg worker.Registry, rep *Report) ([]*Row, map[string]*worker.Slot) {
	// Rows: registry slots first, then worktrees the registry lacks.
	byWT := map[string]worktree{}
	for _, w := range wts {
		byWT[w.name] = w
	}
	seen := map[string]bool{}
	var rows []*Row
	add := func(r *Row, v *view) {
		rows = append(rows, r)
		rep.views[r.Name] = v
		seen[r.Name] = true
	}
	var slotObj = map[string]*worker.Slot{}
	for _, s := range reg.Slots() {
		name := s.Name()
		if name == "" || seen[name] {
			continue
		}
		slotObj[name] = s
		branch, wt := s.Branch(), s.Worktree()
		if w, ok := byWT[name]; ok { // the checkout is the truth about the branch
			branch, wt = w.branch, w.path
		}
		r := &Row{Name: name, Branch: branch, PR: s.PR(), Tab: s.Handle(), Registered: true}
		r.Issue = worker.HeldID(s.Task(), branch, name)
		r.Bundle = s.Bundle()
		if s.IsExternal() {
			r.HostState = ExternalHost
		}
		if _, ev, ok := strings.Cut(s.Seen(), "\t"); ok {
			r.Evidence, _, _ = strings.Cut(ev, "\t") // an event's seq trails the evidence
		}
		if s.State() == "unverifiable" { // an agent the host lists below overrides the host state
			r.HostState, r.Evidence = "unverifiable", s.Reason()
		}
		if ev := s.Event(); ev != nil && ev.Kind != worker.EventHeartbeat {
			r.Event = ev
			r.Evidence = strutil.FirstNonEmpty(ev.Body, r.Evidence)
		}
		r.Outcome, _ = s.Outcome()
		r.Kind, r.KindSource, r.Tier, r.Model, r.TierReason = s.HarnessKind(), s.KindSource(), s.Tier(), s.Model(), s.TierReason()
		kind, acct := SlotAccount(s)
		r.Account, r.AccountKind = acct, cmp.Or(kind, config.KindClaude)
		if r.Issue != "" {
			r.Bounces = reg.Bounces(r.Issue)
			if b := reg.BestOf(r.Issue); b != nil && b.Attempt(name) != nil {
				if sib := b.Sibling(name); sib != nil {
					r.BestOf = sib.Slot
				}
			}
		}
		add(r, &view{worktree: wt, base: strutil.FirstNonEmpty(s.Base(), e.Base)})
	}
	sort.Slice(wts, func(i, j int) bool { return wts[i].name < wts[j].name })
	for _, w := range wts {
		if seen[w.name] {
			continue
		}
		r := &Row{Name: w.name, Branch: w.branch}
		r.Issue = worker.HeldID("", w.branch, w.name)
		add(r, &view{worktree: w.path, base: e.Base})
	}
	return rows, slotObj
}

// readHost is the host source: the live agents, or the reason there are none.
// It records an unavailable host on rep and sets rep.Host when it answers.
func (e Env) readHost(ctx context.Context, rep *Report) (agents []host.Agent, ok bool) {
	if e.HostName == host.Solo {
		// No panes to ask: solo is not an unavailable host, and the tab drift
		// kinds (dead-tab, unclaimed-tab, stalled) have nothing to read.
		rep.Host = host.Solo
	} else if e.Snapshot == nil {
		rep.unavailable(SourceHost, strutil.FirstNonEmpty(e.HostErr, "no host available"))
	} else if a, err := e.Snapshot(ctx); err != nil {
		rep.unavailable(SourceHost, err.Error())
	} else {
		agents, ok = a, true
		rep.Host = e.HostName
	}
	return agents, ok
}

// forgeState is what the forge source answered: open PRs by head branch, the
// open issues carrying the round label, and the closed ones still carrying it.
type forgeState struct {
	prs      map[string]*tracker.PR
	labelled map[tracker.IssueKey]bool
	stale    []tracker.IssueKey
	// ok says the PR list was read; labelsOK says the label lists were too.
	ok, labelsOK bool
}

// readForge is the forge source. An unavailable forge is recorded on rep and
// leaves ok false; nothing else in Status needs it to answer.
func (e Env) readForge(ctx context.Context, rep *Report) forgeState {
	fs := forgeState{prs: map[string]*tracker.PR{}, labelled: map[tracker.IssueKey]bool{}, ok: e.Forge != nil}
	if !fs.ok {
		rep.unavailable(SourceForge, strutil.FirstNonEmpty(e.ForgeErr, "no forge available"))
	} else if list, err := e.Forge.OpenPRs(ctx); err != nil {
		fs.ok = false
		rep.unavailable(SourceForge, err.Error())
	} else {
		for i := range list {
			fs.prs[list[i].Branch] = &list[i]
		}
	}
	if fs.ok {
		// Read the whole open list first: the claim and backlog reads below make
		// this same call, and the forge's read cache answers the label-filtered
		// list from it. The error surfaces on the filtered list.
		_, _ = e.Forge.List(ctx, tracker.ListFilter{State: "open"})
		if issues, err := e.Forge.List(ctx, tracker.ListFilter{State: "open", Labels: []string{e.Label}}); err != nil {
			rep.unavailable(SourceForge, err.Error())
		} else {
			fs.labelsOK = true
			for _, is := range issues {
				fs.labelled[is.Key] = true
			}
			if closed, err := e.Forge.List(ctx, tracker.ListFilter{State: "closed", Labels: []string{e.Label}}); err != nil {
				rep.unavailable(SourceForge, err.Error())
			} else {
				for _, is := range closed {
					if is.State == "closed" {
						fs.stale = append(fs.stale, is.Key)
					}
				}
			}
		}
	}
	return fs
}

func (r *Report) add(f Finding) { r.Findings = append(r.Findings, f) }

func (r *Report) unavailable(src, why string) {
	for _, s := range r.Unavailable {
		if s == src {
			return
		}
	}
	r.Unavailable = append(r.Unavailable, src)
	r.Warnings = append(r.Warnings, fmt.Sprintf("%s unavailable: %s", src, why))
}

// matchAgent finds the agent a row owns: by recorded tab, else by the herdr
// agent name derived from slot and handle (it survives `rota layout` moving the
// pane to another tab, which closes the recorded one), else by working in the
// row's worktree. -1 when none.
func matchAgent(agents []host.Agent, slot, tab, wt string) int {
	if tab != "" {
		for i, a := range agents {
			if a.Tab == tab {
				return i
			}
		}
		name := host.AgentName(slot, tab)
		for i, a := range agents {
			if a.Name == name {
				return i
			}
		}
	}
	if wt == "" {
		return -1
	}
	for i, a := range agents {
		if worker.PathContains(wt, a.Cwd) {
			return i
		}
	}
	return -1
}

// worktrees lists the checkouts directly under <root>/.worktrees/.
func (e Env) worktrees(ctx context.Context, root string) ([]worktree, error) {
	res, err := e.Git(ctx, root, "worktree", "list", "--porcelain")
	out, errOut, code := res.Stdout, res.Stderr, res.ExitCode
	if err != nil || code != 0 {
		return nil, &exitcode.Error{Exit: exitcode.ExitUnavailable, Message: "git worktree list failed: " + strings.TrimSpace(errOut)}
	}
	dir := filepath.Join(root, worker.WorktreeRoot)
	var wts []worktree
	var cur *worktree
	flush := func() {
		if cur != nil && filepath.Dir(cur.path) == dir {
			cur.name = filepath.Base(cur.path)
			wts = append(wts, *cur)
		}
		cur = nil
	}
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			cur = &worktree{path: filepath.Clean(strings.TrimPrefix(line, "worktree "))}
		case strings.HasPrefix(line, "branch ") && cur != nil:
			cur.branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		}
	}
	flush()
	return wts, nil
}

// ahead counts commits on branch that the base lacks, against origin/<base>
// when that exists. 0 when git cannot tell.
func (e Env) ahead(ctx context.Context, root, base, branch string) int {
	if base == "" {
		return 0
	}
	res, err := e.Git(ctx, root, "rev-list", "--count", git.RemoteBase(ctx, e.Git, root, base)+".."+branch)
	out, code := res.Stdout, res.ExitCode
	if err != nil || code != 0 {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(out))
	return n
}

// issueClosed reports whether the slot's issue is closed. A closed issue is
// finished work, so it is neither missing its label nor worth relabelling. A
// lookup that fails counts as open and leaves a warning: the drift stays
// visible rather than hiding behind a flaky forge.
func (e Env) issueClosed(ctx context.Context, rep *Report, n tracker.IssueKey) bool {
	is, err := e.Forge.Get(ctx, n, false)
	if err != nil {
		rep.Warnings = append(rep.Warnings, fmt.Sprintf("issue %s state: %v", n.Ref(), err))
		return false
	}
	return is.State == "closed"
}

// LocalReport is the registry-only report: slot rows and the review queue, with
// no git, host, login or forge read. A screen shows it while Status loads.
func LocalReport(root string) (*Report, error) {
	reg, err := worker.LoadRegistry(root)
	if err != nil {
		return nil, err
	}
	rep := &Report{views: map[string]*view{}}
	rows, _ := Env{}.readRows(nil, reg, rep)
	for _, r := range rows {
		rep.Rows = append(rep.Rows, *r)
	}
	for _, q := range reg.PRs() {
		rep.Queued = append(rep.Queued, QueuedPR{Issue: q.Issue, PR: q.PR, Branch: q.Branch, From: q.From, Bundle: q.Bundle})
	}
	return rep, nil
}

// recordablePR is the PR a pr-unrecorded repair writes to the slot: the open
// one, else the merged one. nil for no view or neither.
func (v *view) recordablePR() *tracker.PR {
	switch {
	case v == nil:
		return nil
	case v.openPR != nil:
		return v.openPR
	}
	return v.mergedPR
}
