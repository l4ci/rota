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
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/escalation"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/labels"
	"github.com/l4ci/rota/internal/limits"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/roundlease"
	"github.com/l4ci/rota/internal/stamp"
	"github.com/l4ci/rota/internal/strutil"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// Drift kinds.
const (
	DeadTab              = "dead-tab"
	TabMoved             = "tab-moved"
	UnclaimedTab         = "unclaimed-tab"
	UnregisteredWorktree = "unregistered-worktree"
	BranchNoPR           = "branch-no-pr"
	PRUnrecorded         = "pr-unrecorded"
	PRSuperseded         = "pr-superseded"
	PRStale              = "pr-stale"
	LabelMissing         = "label-missing"
	LabelOrphan          = "label-orphan"
	LabelStale           = "label-stale"
	StalledSlot          = "stalled"
	ItemTimeout          = "item-timeout"
	ClaimMismatch        = "claim-mismatch"
	ClaimLabelDrift      = "claim-label-drift"
	UnregisteredBranch   = "unregistered-branch"
	// Bounced: on the support machine, a queued PR main sent back (#1598).
	Bounced        = "bounced"
	MergedExternal = "merged-external"
	// BaseDrift: a waiting PR targets a base its issue's milestone no longer routes it to.
	BaseDrift            = "base-drift"
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
const NeedsInfoLabel = labels.NeedsInfo

// Forge is the part of the tracker adapter a round reads and, for the
// label repair, writes. tracker.Adapter satisfies it.
type Forge interface {
	OpenPRs(ctx context.Context) ([]tracker.PR, error)
	MergedPRs(ctx context.Context, branch string) ([]tracker.PR, error)
	PRState(ctx context.Context, pr int) (string, error)
	PRView(ctx context.Context, pr int) (tracker.PRInfo, error)
	PRSetBase(ctx context.Context, pr int, base string) error
	ClosedNumbers(body string) []int
	List(ctx context.Context, f tracker.ListFilter) ([]tracker.Issue, error)
	Get(ctx context.Context, key tracker.IssueKey, withComments bool) (tracker.Issue, error)
	AddLabels(ctx context.Context, key tracker.IssueKey, labels []string, autoCreate bool) error
	RemoveLabels(ctx context.Context, key tracker.IssueKey, labels []string) error
}

// Env is what a round touches outside its own memory. A nil Snapshot or Forge
// means that source is unavailable; tests fill every field with fakes.
type Env struct {
	// inspectReads keeps observation's shared status probes read-only. Remote
	// activity requiring a fetch is reported as unknown instead.
	inspectReads bool
	Git          git.Runner
	// Snapshot lists the host's live agents; HostName is "herdr" or "tmux", or
	// "solo" with no Snapshot when the round has no terminal host.
	Snapshot func(ctx context.Context) ([]host.Agent, error)
	// ReadPane reads a pane when the harness can distinguish an internal tool
	// wait from the host's blocked status. Nil leaves the snapshot unchanged.
	ReadPane func(ctx context.Context, slot, handle string, lines int) (string, error)
	// FollowTab renames a slot's agent for the tab its pane moved to; nil
	// where the host has no such name (tmux).
	FollowTab func(ctx context.Context, slot, from, to string) error
	HostName  host.Kind
	Forge     Forge
	// SweepClosedClaims lets Reconcile list every closed issue for stray claim
	// labels. Only `round reconcile` sets it; status, watch and the autopilot
	// tick stay on label-filtered closed reads.
	SweepClosedClaims bool
	// Base is the branch slots are cut from when a slot records none.
	Base  string
	Label string
	// HostErr and ForgeErr say why Snapshot or Forge is nil.
	HostErr, ForgeErr string
	// BaseErr is why Base is unusable: git.baseBranch is set but does not
	// resolve. The verbs that cut or compare against Base refuse on it.
	BaseErr string
	// Stop is the autopilot's stop state, which WindDown sets, restores or
	// clears; nil leaves the autopilot alone.
	Stop AutopilotStop
	// Lease reads the orchestrator lease; the zero value is the real process
	// table and host name.
	Lease roundlease.Env
	// Worker is every worker operation the round performs. It is the one owner
	// of the clock (Now, which dates timed-out escalations), the environment
	// reader (Getenv, for the lease, host and brief lookups) and Accounts, the
	// meter reader that quota and the ledger read; picking an account is
	// AccountPicking's job. Nil is not usable: every verb panics on it, so build
	// it with FromWorkerEnv (the zero Env is not a working one).
	Worker Worker
	// Login reads a slot account's Claude login for the no-login check
	// (SlotLoginProblem). The zero value reads credentials files only, never the
	// macOS Keychain; baseRoundEnv sets the system one.
	Login harness.Credentials
	// AccountPicking turns the delivery's account step on: keep the slot's own
	// round.accounts account while it has headroom, else pick. Off is the zero
	// value; WithAccounts is the one way to turn it on, so a verb opts in by
	// name and Worker.Accounts alone (the meter read) never picks.
	AccountPicking bool
	// Quotas reads the accounts of harnesses with no usage meter (codex) as
	// scheduling meters for pickWorker, cached per account. Nil leaves their
	// headroom unknown.
	Quotas *worker.Quotas
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
	// HolderPID names the lease holder for every verb that checks or takes the
	// lease; 0 discovers it from the process ancestry. The one place the pid
	// lives: no verb opts struct carries its own.
	HolderPID int
	// NeedsHuman is issues.labels.needsHuman, the label of an issue handed to the
	// human (C10); config.Label supplies its default.
	NeedsHuman string
	// OnHold is issues.labels.onHold: work waits until a person removes it.
	OnHold string
	// SupportLabel is the derived machine:<round.support> pin.
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
	// unknown), or a driven slot's still-current accepted completion (done).
	// HostState remains the native host observation.
	State      string
	Tab        string
	Registered bool
	// Worktree is the slot's checkout path as the registry or the checkouts
	// record it; "" when neither does.
	Worktree string
	Drift    []string
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
func (e Env) adoptedPRNumber(ctx context.Context, root string, notes *statusNotes, v *view, r *Row) (int, bool) {
	if n, ok := worker.PRRefNumber(r.PR); ok {
		return n, true
	}
	if r.HostState != ExternalHost || r.PR != "" {
		return 0, false
	}
	merged, err := e.Forge.MergedPRs(ctx, r.Branch)
	if err != nil {
		notes.warn(fmt.Sprintf("merged PRs of %s: %v", r.Branch, err))
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
func (e Env) unrecordedMergedPR(ctx context.Context, root string, notes *statusNotes, v *view, r *Row, so *worker.Slot) *tracker.PR {
	if !r.Registered || so == nil || r.Issue == "" || r.PR != "" || !slotFinished(r, so) {
		return nil
	}
	m, err := e.mergedBranchPR(ctx, root, r.Branch, v.base)
	if err != nil {
		notes.warn(fmt.Sprintf("merged PRs of %s: %v", r.Branch, err))
		v.prErr = true
	}
	return m
}

// slotFinished prefers the live host state, falling back to the registry when
// no host state is available. A working slot is still producing its PR.
func slotFinished(r *Row, s *worker.Slot) bool {
	state := r.HostState
	if state == "" && s != nil {
		state = s.State()
	}
	// A native done/idle on a slot dispatched since is the last turn's (#3311).
	if worker.NativeDoneIsStale(s, state) {
		return false
	}
	return worker.Parkable(state)
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
	if onBranch, err := e.isAncestor(ctx, root, sha, branch); err != nil || !onBranch {
		return false
	}
	// Status never fetches, so origin/<base> can run ahead of local <base>: the
	// head counts as in base when either holds it.
	checked := false
	for _, ref := range []string{base, "origin/" + base} {
		if ok, _ := e.Git(ctx, root, "rev-parse", "--verify", "-q", ref); ok.ExitCode != 0 {
			continue
		}
		// Unreadable counts as not ours, like a head already in base.
		if in, err := e.isAncestor(ctx, root, sha, ref); err != nil || in {
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
	// to is the base a base-drift finding retargets the PR onto: the milestone
	// branch, or "" for the slot's registered base. For tab-moved it is the
	// handle the pane has now (its tab, or the pane when the tab is shared)
	// and from the one the registry records.
	to, from string
	// tag is the issue's milestone tag the repair records on the slot.
	tag string
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
	// Agents is the host snapshot the report was built from, nil when the host
	// was not read (solo, unavailable or failed).
	Agents []host.Agent
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
	if e.RoundErr != nil {
		return &exitcode.Error{Exit: exitcode.ExitUsage, Message: e.RoundErr.Error()}
	}
	return e.baseCfgErr()
}

// baseCfgErr refuses a set git.baseBranch that does not resolve, with exit 3
// like `git base`; nil when the base resolved or the key is unset.
func (e Env) baseCfgErr() error {
	if e.BaseErr != "" {
		return &exitcode.Error{Exit: exitcode.ExitResolution, Message: e.BaseErr}
	}
	return nil
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
	rows, findings, alive, sameSession := e.hostFindings(ctx, root, rows, rep.views, slotObj, agents, hostOK)
	rep.Findings = append(rep.Findings, findings...)

	fs := e.readForge(ctx, rep)
	prs, labelled, stale, forgeOK, labelsOK := fs.prs, fs.labelled, fs.stale, fs.ok, fs.labelsOK
	if forgeOK {
		rep.openPRs = prs
		e.issueBounces(root, rows, rep)
	}

	findings, held, notes := e.slotFindings(ctx, root, rows, rep.views, slotObj, fs)
	rep.Findings = append(rep.Findings, findings...)
	rep.applyStatusNotes(notes)

	rep.Queued, findings, notes = queuedFindings(ctx, e.Forge, forgeOK, reg.PRs(), held)
	rep.Findings = append(rep.Findings, findings...)
	rep.applyStatusNotes(notes)

	rep.Findings = append(rep.Findings, workflowLabelFindings(e.Label, stale, labelled, labelsOK, held)...)

	now := e.Worker.Now
	waiting := map[string]bool{}
	for _, x := range escalation.Load(root) {
		if escalation.IsOpen(x) && x.Slot != "" {
			waiting[x.Slot] = true
		}
	}
	rep.Findings = append(rep.Findings, e.stalledFindings(ctx, rows, rep.views, slotObj, hostOK, alive, waiting)...)

	rep.Findings = append(rep.Findings, itemTimeoutFindings(rows, e.Round.ItemTimeoutMinutes, reg.ItemStart, e.Worker.Now)...)

	findings, warnings := claimFindings(ctx, e.Board, reg, rows, slotObj, reg.PRs(), labelled, fs.claimLabelled, labelsOK)
	rep.Findings = append(rep.Findings, findings...)
	rep.Warnings = append(rep.Warnings, warnings...)
	if e.Board != nil && forgeOK && (e.Round.ClaimStaleMinutes > 0 || e.Round.ClaimStaleMinutesMajor > 0) {
		snapshot := make([]tracker.PR, 0, len(prs))
		for _, pr := range prs {
			snapshot = append(snapshot, *pr)
		}
		items, err := e.Board.List(false)
		if err != nil {
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("stale claims: %v", err))
			items = nil
		}
		var claimedItems []backlog.Item
		for _, it := range items {
			if !it.Closed && (fs.claimLabelled[it.IssueKey()] || labelled[it.IssueKey()] || e.hasClaimSignal(it)) {
				claimedItems = append(claimedItems, it)
			}
		}
		staleClaims, warnings := e.staleClaims(ctx, root, e.Board, claimedItems, snapshot)
		rep.Warnings = append(rep.Warnings, warnings...)
		rep.Findings = append(rep.Findings, staleClaimFindings(staleClaims, e.Worker.Now)...)
	}

	lease, leaseState, leaseErr := e.ReadLease(ctx, root)
	rep.Findings = append(rep.Findings, leaseFindings(lease, leaseState, leaseErr)...)

	// Host and forge reads can outlast a relay or a session replacement. Re-read
	// without writing: a missing/unreadable registry cannot vouch for completion.
	current := worker.LoadRegistryTolerant(root)
	for _, r := range rows {
		if r.HostState == ExternalHost {
			// Merged stays listed until the gate or reconcile --apply releases it.
			r.State = e.ExternalState(ctx, root, forgeOK && !rep.views[r.Name].prErr, rep.views[r.Name].base, r)
		} else if sameSession[r.Name] {
			r.State = drivenCompletion(r, slotObj[r.Name], current.Slot(r.Name))
		}
		// The findings above judge the native state; only what is shown follows
		// worker poll. A done the host still shows for a slot dispatched since
		// is the last turn's, unless the pane shows this turn finished.
		if r.State == "" && alive[r.Name] && worker.NativeDoneIsStale(slotObj[r.Name], r.HostState) {
			done := false
			if e.ReadPane != nil {
				pane, _ := e.ReadPane(ctx, r.Name, r.Tab, 60)
				st, _ := worker.ClassifyFor(r.Kind, pane, false, 60, r.HostState)
				done = st == worker.StateDone
			}
			if !done {
				r.HostState = "working"
			}
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
	for _, x := range escalation.LoadOpen(root) {
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

// drivenCompletion exposes accepted completion separately from native idle.
// Native work outranks it, and a dispatch during this status read invalidates
// the session/turn snapshot even when a replacement reuses the same handle.
func drivenCompletion(r *Row, before, current *worker.Slot) string {
	if before == nil || current == nil || r.Branch != current.Branch() {
		return ""
	}
	switch r.HostState {
	case "", "idle", "done": // tmux has no native agent state
	default:
		return ""
	}
	for _, s := range []*worker.Slot{before, current} {
		if !worker.IsDone(s.State()) || s.RelayPending() != 0 || s.AwaitingDone() || s.Unsent() || s.IsExternal() {
			return ""
		}
		if ev := s.Event(); ev != nil {
			at, err := stamp.Parse(s.ActiveAt())
			if err != nil || worker.Decide(ev, worker.PollRow{Name: r.Name, State: worker.StateDone}, at).State != worker.StateDone {
				return ""
			}
		}
	}
	if before.Task() != current.Task() || before.Branch() != current.Branch() || before.PaneHandle() != current.PaneHandle() ||
		before.Worktree() != current.Worktree() || before.Attempt() != current.Attempt() || before.TurnSeq() != current.TurnSeq() || before.ActiveAt() != current.ActiveAt() {
		return ""
	}
	oldEvent, newEvent := before.Event(), current.Event()
	if (oldEvent == nil) != (newEvent == nil) || (oldEvent != nil && oldEvent.Seq != newEvent.Seq) {
		return ""
	}
	return "done"
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
		r := &Row{Name: name, Branch: branch, PR: s.PR(), Tab: s.Handle(), Registered: true, Worktree: wt}
		r.Issue = worker.HeldID(s.Task(), branch, name)
		r.Bundle = s.Bundle()
		if s.IsExternal() {
			r.HostState = ExternalHost
		}
		if _, ev, ok := strings.Cut(s.Seen(), "\t"); ok {
			r.Evidence, _, _ = strings.Cut(ev, "\t") // an event's seq trails the evidence
		}
		if worker.IsUnverifiable(s.State()) { // an agent the host lists below overrides the host state
			r.HostState, r.Evidence = worker.LivenessUnverifiable, s.Reason()
		}
		if ev := s.Event(); ev != nil && ev.Kind != worker.EventHeartbeat {
			r.Event = ev
			r.Evidence = strutil.FirstNonEmpty(ev.Body, r.Evidence)
		}
		r.Outcome, _ = s.Outcome()
		r.Kind, r.KindSource, r.Tier, r.Model, r.TierReason = s.Kind(), s.KindSource(), s.Tier(), s.Model(), s.TierReason()
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
// It records an unavailable host on rep and sets rep.Host and rep.Agents when it answers.
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
		rep.Host, rep.Agents = e.HostName, a
	}
	return agents, ok
}

// forgeState is what the forge source answered: open PRs by head branch, open
// issues carrying the round or claimed labels, and closed issues with the round label.
type forgeState struct {
	prs           map[string]*tracker.PR
	labelled      map[tracker.IssueKey]bool
	claimLabelled map[tracker.IssueKey]bool
	stale         []tracker.IssueKey
	// ok says the PR list was read; labelsOK says the label lists were too.
	ok, labelsOK bool
}

// readForge is the forge source. An unavailable forge is recorded on rep and
// leaves ok false; nothing else in Status needs it to answer.
func (e Env) readForge(ctx context.Context, rep *Report) forgeState {
	fs := forgeState{prs: map[string]*tracker.PR{}, labelled: map[tracker.IssueKey]bool{}, claimLabelled: map[tracker.IssueKey]bool{}, ok: e.Forge != nil}
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
		open, _ := e.Forge.List(ctx, tracker.ListFilter{State: "open"})
		for _, is := range open {
			if backlog.HasClaimLabel(is.Labels) {
				fs.claimLabelled[is.Key] = true
			}
		}
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
					if is.State == "closed" && slices.Contains(is.Labels, e.Label) {
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
		// The name first: panes folded into one tab share it, and the first
		// agent in the snapshot there is not necessarily this slot's.
		name := host.AgentName(slot, tab)
		for i, a := range agents {
			if a.Name == name {
				return i
			}
		}
		for i, a := range agents {
			if a.Tab == tab {
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
	for _, w := range git.ParseWorktrees(out) {
		path := filepath.Clean(w.Path)
		if filepath.Dir(path) == dir {
			wts = append(wts, worktree{path: path, name: filepath.Base(path), branch: w.Branch})
		}
	}
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
func (e Env) issueClosed(ctx context.Context, notes *statusNotes, n tracker.IssueKey) bool {
	is, err := e.Forge.Get(ctx, n, false)
	if err != nil {
		notes.warn(fmt.Sprintf("issue %s state: %v", n.Ref(), err))
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

// evBefore reports whether the event stamp ts is older than the state-change
// stamp at. A stamp that does not parse counts as older: it proves nothing.
func evBefore(ts, at string) bool {
	t, err := stamp.Parse(ts)
	if err != nil {
		return true
	}
	a, err := stamp.Parse(at)
	return err == nil && t.Before(a)
}
