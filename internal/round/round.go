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
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/escalation"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/limits"
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
	StalledSlot          = "stalled"
	ClaimMismatch        = "claim-mismatch"
	// LeaseStale is declared in lease.go.
)

// Source names, as `unavailable` lists them.
const (
	SourceHost  = "host"
	SourceForge = "forge"
)

// DefaultLabel is the issue label that means an agent is on the issue.
const DefaultLabel = "in-progress"

// DefaultNeedsHuman is the label of an issue handed to the human (C10).
const DefaultNeedsHuman = "needs-human"

// Forge is the part of the tracker adapter a round reads and, for the
// label repair, writes. tracker.Adapter satisfies it.
type Forge interface {
	OpenPRs(ctx context.Context) ([]tracker.PR, error)
	PRState(ctx context.Context, pr int) (string, error)
	List(ctx context.Context, f tracker.ListFilter) ([]tracker.Issue, error)
	AddLabels(ctx context.Context, number int, labels []string, autoCreate bool) error
}

// Env is what a round touches outside its own memory. A nil Snapshot or Forge
// means that source is unavailable; tests fill every field with fakes.
type Env struct {
	Git worker.GitFunc
	// Snapshot lists the host's live agents; HostName is "herdr" or "tmux", or
	// "solo" with no Snapshot when the round has no terminal host.
	Snapshot func(ctx context.Context) ([]host.Agent, error)
	HostName string
	Forge    Forge
	// Base is the branch slots are cut from when a slot records none.
	Base  string
	Label string
	// HostErr and ForgeErr say why Snapshot or Forge is nil.
	HostErr, ForgeErr string
	// Now dates timed-out escalations; nil means time.Now.
	Now func() time.Time
	// Lease reads the orchestrator lease; the zero value is the real process
	// table and host name.
	Lease roundlease.Env
	// Worker is the worker env assign resets and dispatches with; the zero
	// value is a Git-only env. Accounts, when set, balances slots across
	// work.accounts at assignment.
	Worker   worker.Env
	Accounts *worker.Accounts
	// Board is the backlog's claim side; claim-mismatch drift needs it, and
	// without it (file mode) that kind is skipped.
	Board Board
	// StallMinutes is round.stallMinutes; 0 turns the stalled check off.
	StallMinutes int
	// NeedsHuman is issues.labels.needsHuman; "" means DefaultNeedsHuman.
	NeedsHuman string
}

// Row is one line of `rota round status`.
type Row struct {
	Name       string
	Agent      string
	Issue      string
	Branch     string
	PR         string
	PRState    string
	HostState  string
	Tab        string
	Registered bool
	Drift      []string
	// Escalations are the ids of the slot's open escalations.
	Escalations []string
	// Kind, Tier, Model and TierReason are the slot's C9 fields.
	Kind, Tier, Model, TierReason string
}

// Finding is one drift. Repair names what Reconcile(apply) would do and is
// empty for kinds that are never repaired.
type Finding struct {
	Kind, Slot, Issue, Detail, Repair string
}

// Report is the assembled state.
type Report struct {
	Rows        []Row
	Findings    []Finding
	Unavailable []string
	Warnings    []string
	Host        string
	// Escalations are the open ones (pending or timed-out), read from the
	// registry without a forge call; `rota round escalate check` looks for answers.
	Escalations []escalation.Report
	// Limits are the usage-limit entries still waiting (D3), read from the
	// registry like escalations.
	Limits []limits.Entry

	views map[string]*view
}

// view is a row plus what repairs need.
type view struct {
	worktree string
	base     string
	openPR   *tracker.PR
}

var (
	reIssueBranch = regexp.MustCompile(`^[^/]+/(\d+)-`)
	reIssueTask   = regexp.MustCompile(`^#?(\d+)$`)
	rePRNumber    = regexp.MustCompile(`^(?:#|.*/(?:pull|merge_requests)/)?(\d+)/?$`)
)

type worktree struct{ path, name, branch string }

// Status assembles the round. It fails only when git does; an unavailable
// host or forge is reported in the result.
func (e Env) Status(ctx context.Context, root string) (*Report, error) {
	if e.Label == "" {
		e.Label = DefaultLabel
	}
	rep := &Report{views: map[string]*view{}}
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	wts, err := e.worktrees(ctx, root)
	if err != nil {
		return nil, err
	}
	reg := worker.LoadRegistry(root)

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
	var slotObj = map[string]*jsonx.Object{}
	for _, s := range reg.Slots() {
		name := worker.Str(s, "name")
		if name == "" || seen[name] {
			continue
		}
		slotObj[name] = s
		branch, wt := worker.Str(s, "branch"), worker.Str(s, "worktree")
		if w, ok := byWT[name]; ok { // the checkout is the truth about the branch
			branch, wt = w.branch, w.path
		}
		r := &Row{Name: name, Branch: branch, PR: worker.Str(s, "pr"), Tab: worker.Str(s, "handle"), Registered: true}
		r.Issue = issueOf(worker.Str(s, "task"), branch, name)
		r.Kind, r.Tier, r.Model, r.TierReason = worker.Str(s, "kind"), worker.Str(s, "tier"), worker.Str(s, "model"), worker.Str(s, "tierReason")
		add(r, &view{worktree: wt, base: firstNonEmpty(worker.Str(s, "base"), e.Base)})
	}
	sort.Slice(wts, func(i, j int) bool { return wts[i].name < wts[j].name })
	for _, w := range wts {
		if seen[w.name] {
			continue
		}
		r := &Row{Name: w.name, Branch: w.branch}
		r.Issue = issueOf("", w.branch, w.name)
		add(r, &view{worktree: w.path, base: e.Base})
	}

	// Host.
	var agents []host.Agent
	hostOK := false
	if e.HostName == host.Solo {
		// No panes to ask: solo is not an unavailable host, and the tab drift
		// kinds (dead-tab, unclaimed-tab, stalled) have nothing to read.
		rep.Host = host.Solo
	} else if e.Snapshot == nil {
		rep.unavailable(SourceHost, firstNonEmpty(e.HostErr, "no host available"))
	} else if a, err := e.Snapshot(ctx); err != nil {
		rep.unavailable(SourceHost, err.Error())
	} else {
		agents, hostOK = a, true
		rep.Host = e.HostName
	}
	claimed := map[int]bool{}
	alive := map[string]bool{} // slots the host shows an agent or window for
	for _, r := range rows {
		if !hostOK {
			break
		}
		wt := rep.views[r.Name].worktree
		// A parked slot owns no agent: one still running in its worktree is a
		// leftover, reported as unclaimed rather than absorbed by the slot.
		if r.Tab == "" && (r.Branch == "" || r.Branch == "park/"+r.Name) {
			wt = ""
		}
		if i := matchAgent(agents, r.Tab, wt); i >= 0 {
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
		wtDir := filepath.Join(root, ".worktrees") + string(os.PathSeparator)
		for i, a := range agents {
			if claimed[i] || !strings.HasPrefix(filepath.Clean(a.Cwd)+string(os.PathSeparator), wtDir) {
				continue
			}
			name := firstNonEmpty(a.Name, a.Tab)
			rows = append(rows, &Row{Name: name, Agent: a.Name, Tab: a.Tab, HostState: a.Status})
			rep.add(Finding{Kind: UnclaimedTab, Slot: name, Detail: fmt.Sprintf("live agent in %s matches no slot", a.Cwd)})
		}
	}

	// Forge: open PRs by head branch, then the labelled issues.
	prs, labelled := map[string]*tracker.PR{}, map[int]bool{}
	forgeOK := e.Forge != nil
	if !forgeOK {
		rep.unavailable(SourceForge, firstNonEmpty(e.ForgeErr, "no forge available"))
	} else if list, err := e.Forge.OpenPRs(ctx); err != nil {
		forgeOK = false
		rep.unavailable(SourceForge, err.Error())
	} else {
		for i := range list {
			prs[list[i].Branch] = &list[i]
		}
	}
	labelsOK := false
	if forgeOK {
		if issues, err := e.Forge.List(ctx, tracker.ListFilter{State: "open", Labels: []string{e.Label}}); err != nil {
			rep.unavailable(SourceForge, err.Error())
		} else {
			labelsOK = true
			for _, is := range issues {
				labelled[is.Number] = true
			}
		}
	}

	held := map[string]bool{}
	for _, r := range rows {
		v := rep.views[r.Name]
		if v == nil { // unclaimed tab
			continue
		}
		parked := r.Branch == "" || r.Branch == "park/"+r.Name || r.Branch == v.base
		if r.Issue != "" {
			held[r.Issue] = true
		}
		if !r.Registered {
			rep.add(Finding{Kind: UnregisteredWorktree, Slot: r.Name, Detail: fmt.Sprintf("%s has no slot in .rota/workers.json", v.worktree), Repair: "register the slot"})
		}
		if forgeOK && !parked {
			if pr := prs[r.Branch]; pr != nil {
				v.openPR = pr
				r.PRState = "open"
				if r.PR == "" {
					r.PR = pr.URL
					if r.Registered {
						rep.add(Finding{Kind: PRUnrecorded, Slot: r.Name, Issue: r.Issue, Detail: fmt.Sprintf("open PR #%d has branch %s as head, slot records none", pr.Number, r.Branch), Repair: "record pr"})
					}
				}
			} else if n, ok := prNumber(r.PR); ok {
				st, err := e.Forge.PRState(ctx, n)
				if err != nil {
					rep.Warnings = append(rep.Warnings, fmt.Sprintf("PR #%d state: %v", n, err))
				} else {
					r.PRState = st
					if st == "merged" || st == "closed" {
						rep.add(Finding{Kind: PRStale, Slot: r.Name, Issue: r.Issue, Detail: fmt.Sprintf("PR #%d is %s and the slot still holds %s", n, st, r.Branch)})
					}
				}
			} else if ahead := e.ahead(ctx, root, v.base, r.Branch); ahead > 0 {
				rep.add(Finding{Kind: BranchNoPR, Slot: r.Name, Issue: r.Issue, Detail: fmt.Sprintf("%s is %d commit(s) ahead of %s with no PR", r.Branch, ahead, v.base)})
			}
		}
		if labelsOK && !parked && r.Issue != "" && r.PRState != "merged" && r.PRState != "closed" {
			if n, _ := strconv.Atoi(r.Issue); !labelled[n] {
				rep.add(Finding{Kind: LabelMissing, Slot: r.Name, Issue: r.Issue, Detail: fmt.Sprintf("slot holds #%s, which lacks %s", r.Issue, e.Label), Repair: "add " + e.Label})
			}
		}
	}
	if labelsOK {
		var orphans []int
		for n := range labelled {
			if !held[strconv.Itoa(n)] {
				orphans = append(orphans, n)
			}
		}
		sort.Ints(orphans)
		for _, n := range orphans {
			rep.add(Finding{Kind: LabelOrphan, Issue: strconv.Itoa(n), Detail: fmt.Sprintf("#%d has %s and no slot holds it", n, e.Label)})
		}
	}

	now := time.Now
	if e.Now != nil {
		now = e.Now
	}
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
				Escalated: waiting[r.Name], ActiveAt: worker.Str(s, "activeAt"), Minutes: e.StallMinutes,
			}, now())
			if st.Stalled {
				rep.add(Finding{Kind: StalledSlot, Slot: r.Name, Issue: r.Issue,
					Detail: fmt.Sprintf("nothing moved for %d min (last: %s)", int(st.Idle.Minutes()), st.Signal)})
			}
		}
	}
	e.claimFindings(ctx, rep, rows, slotObj, labelled, labelsOK)

	e.leaseFinding(ctx, root, rep)

	for _, r := range rows {
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

// matchAgent finds the agent a row owns: by recorded tab, else by working in
// the row's worktree. -1 when none.
func matchAgent(agents []host.Agent, tab, wt string) int {
	if tab != "" {
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
		c := filepath.Clean(a.Cwd)
		if c == wt || strings.HasPrefix(c, wt+string(os.PathSeparator)) {
			return i
		}
	}
	return -1
}

// issueOf is the issue a slot holds: its task when that is a number, else the
// number leading the branch `<agent>/<issue>-<slug>`. A parked slot with no
// task holds none.
func issueOf(task, branch, name string) string {
	if m := reIssueTask.FindStringSubmatch(strings.TrimSpace(task)); m != nil {
		return m[1]
	}
	if branch == "park/"+name {
		return ""
	}
	if m := reIssueBranch.FindStringSubmatch(branch); m != nil {
		return m[1]
	}
	return ""
}

// SlotIssue is the issue number a slot holds (issueOf), "" when it holds none.
func SlotIssue(task, branch, name string) string { return issueOf(task, branch, name) }

// PRNumber reads the number from a recorded PR (a bare number, #n or a URL).
func PRNumber(pr string) (int, bool) { return prNumber(pr) }

func prNumber(pr string) (int, bool) {
	m := rePRNumber.FindStringSubmatch(strings.TrimSpace(pr))
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	return n, err == nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// worktrees lists the checkouts directly under <root>/.worktrees/.
func (e Env) worktrees(ctx context.Context, root string) ([]worktree, error) {
	out, errOut, code, err := e.Git(ctx, root, "worktree", "list", "--porcelain")
	if err != nil || code != 0 {
		return nil, &worker.Error{Exit: worker.ExitUnavailable, Message: "git worktree list failed: " + strings.TrimSpace(errOut)}
	}
	dir := filepath.Join(root, ".worktrees")
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
	ref := base
	if _, _, code, err := e.Git(ctx, root, "rev-parse", "--verify", "-q", "origin/"+base); err == nil && code == 0 {
		ref = "origin/" + base
	}
	out, _, code, err := e.Git(ctx, root, "rev-list", "--count", ref+".."+branch)
	if err != nil || code != 0 {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(out))
	return n
}
