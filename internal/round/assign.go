package round

import (
	"context"
	"errors"
	"fmt"
	"github.com/l4ci/rota/internal/exitcode"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/roundlease"
	"github.com/l4ci/rota/internal/worker"
)

// Board is the backlog an assignment reads and marks.
type Board interface {
	backlog.Backend
	backlog.Workflow
}

// Blocked reasons of an assignment (exit 4, `blockedBy`).
const (
	BlockNoRound      = "no round"
	BlockOutOfScope   = "out of scope"
	BlockNotReady     = "not ready"
	BlockOverlap      = "overlap"
	BlockClaimed      = "claimed"
	BlockOpenPR       = "open PR"
	BlockSlotBusy     = "slot busy"
	BlockNoFreeSlot   = "no free slot"
	BlockBriefMissing = "brief missing"
	BlockNoTierMap    = "no tier map"
	// BlockCodexVersion: the installed Codex CLI is outside the supported
	// range and --accept-codex-version is not given (E1, #68).
	BlockCodexVersion = harness.BlockCodexVersion
)

// BlockedError is an assignment refused before anything was marked or sent.
type BlockedError struct {
	By        string
	Msg       string
	Readiness *Readiness
}

func (e *BlockedError) Error() string { return e.Msg }

// AssignOpts are the flags of `rota round assign`, with the config read.
type AssignOpts struct {
	ID            string
	Agent         string
	BodyFile      string // answered decisions, verbatim; "" for none
	Siblings      []string
	CheckOnly     bool
	AcceptOverlap bool
	// AcceptOpenPR lets a deliberate redo through an issue an open PR resolves.
	AcceptOpenPR bool
	HolderPID    int
	Settings     roundcfg.Settings
	Getenv       func(string) string
	// Tier, TierReason and Kind are C9: "" means round.tier, no reason, and
	// the slot's recorded kind, else claude.
	Tier, TierReason, Kind string
	// AcceptCodexVersion lets one codex assignment through a Codex CLI outside
	// the supported range, with a warning.
	AcceptCodexVersion bool
}

// Assigned is what Assign did.
type Assigned struct {
	ID, Type, Agent, Branch string
	Readiness
	Account    string
	Dispatched bool
	// Host, Brief and Worktree are set under solo (C8) in place of a dispatch:
	// the brief to launch the subagent with, and its absolute working directory.
	Host, Brief, Worktree string
	Changed               bool
	// Kind, Tier and Model are what the worker starts with; Model is "" when a
	// custom work.workerCommand has no {model} placeholder.
	Kind, Tier, Model, TierReason string
	Warnings                      []string
}

var (
	slugNonWord = regexp.MustCompile(`[^a-z0-9]+`)
)

// BranchName is `<agent>/<issue>-<slug>`: the issue number (file mode: the
// lowercased ID), then the title lowercased with non-alphanumerics collapsed
// to `-`, cut to five words and 40 characters.
func BranchName(agent, id, title string) string {
	slug := strings.Trim(slugNonWord.ReplaceAllString(strings.ToLower(title), "-"), "-")
	if words := strings.Split(slug, "-"); len(words) > 5 {
		slug = strings.Join(words[:5], "-")
	}
	if len(slug) > 40 {
		slug = strings.TrimRight(slug[:40], "-")
	}
	name := agent + "/" + strings.ToLower(id)
	if slug != "" {
		name += "-" + slug
	}
	return name
}

// briefPath is the standing worker contract the pointer names: round.brief,
// else references/worker-contract.md in the project (a source checkout), else
// rota-orchestrate/references/worker-contract.md under the first installed
// skills root (rota skills install): the project's before the user's, Claude's
// before Codex's, so a Codex-only install finds it too.
func briefPath(root string, set roundcfg.Settings, getenv func(string) string) (string, bool) {
	var cands []string
	if set.Brief != "" {
		p := set.Brief
		if !filepath.IsAbs(p) {
			p = filepath.Join(root, p)
		}
		cands = append(cands, p)
	} else {
		cands = append(cands, filepath.Join(root, "references", "worker-contract.md"))
		installed := filepath.Join("rota-orchestrate", "references", "worker-contract.md")
		skillRoots := []string{filepath.Join(root, ".claude", "skills"), filepath.Join(root, ".agents", "skills")}
		if d := getenv("CLAUDE_CONFIG_DIR"); d != "" {
			skillRoots = append(skillRoots, filepath.Join(d, "skills"))
		} else if home := getenv("HOME"); home != "" {
			skillRoots = append(skillRoots, filepath.Join(home, ".claude", "skills"))
		}
		if home := getenv("HOME"); home != "" {
			skillRoots = append(skillRoots, filepath.Join(home, ".agents", "skills"))
		}
		for _, r := range skillRoots {
			if _, err := os.Stat(filepath.Join(r, ".rota-manifest.json")); err == nil {
				cands = append(cands, filepath.Join(r, installed))
				break
			}
		}
	}
	for _, c := range cands {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			return c, true
		}
	}
	return "", false
}

// pointerBrief is the short brief a worker is dispatched with: where its
// contract is, which issue to read and dispute, its branch, its siblings and
// the decisions already settled. dispatch signs it.
func pointerBrief(agent, id, branch, brief string, siblings []string, decisions string, t tierBrief) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are %s. Read %s in full before anything else: it is your standing contract.\n\n", agent, brief)
	fmt.Fprintf(&b, "Then read issue %s and its whole thread yourself. Dispute the ticket before implementing if it is wrong, already decided or contradicts the code: say so instead of building it.\n\n", id)
	fmt.Fprintf(&b, "Your branch is %s; your worktree is already on it.\n", branch)
	if len(siblings) > 0 {
		fmt.Fprintf(&b, "Sibling issues running now: %s.\n", strings.Join(siblings, ", "))
	}
	b.WriteString(t.text())
	if d := strings.TrimSpace(decisions); d != "" {
		fmt.Fprintf(&b, "\nDecisions already settled (verbatim):\n\n%s\n", d)
	}
	return b.String()
}

// tierBrief is the resolved tier facts of one worker: its own tier and model,
// the tier table of its harness kind, and why the tier is above the default.
type tierBrief struct {
	Kind, Tier, Model, Default, Reason string
	Table                              map[string]string
}

func (t tierBrief) text() string {
	var b strings.Builder
	own := t.Tier
	if t.Model != "" {
		own += " (" + t.Model + ")"
	}
	fmt.Fprintf(&b, "\nYour tier is %s", own)
	if t.Reason != "" {
		if roundcfg.TierRank(t.Tier) > roundcfg.TierRank(t.Default) {
			fmt.Fprintf(&b, ", above the default %s: %s", t.Default, t.Reason)
		} else {
			fmt.Fprintf(&b, ": %s", t.Reason)
		}
	}
	b.WriteString(".\n")
	if len(t.Table) == 0 {
		fmt.Fprintf(&b, "No model tiers are set for %s: your own subagents use its default model.\n", t.Kind)
	} else {
		var rows []string
		for _, tier := range roundcfg.Tiers {
			rows = append(rows, tier+" = "+t.Table[tier])
		}
		fmt.Fprintf(&b, "Model tiers for your own subagents (%s): %s. Follow the tier rule in the contract.\n", t.Kind, strings.Join(rows, ", "))
	}
	fmt.Fprintf(&b, "Put `Worker tier: %s` in your PR body.\n", own)
	return b.String()
}

func blocked(by, format string, a ...any) *BlockedError {
	return &BlockedError{By: by, Msg: fmt.Sprintf(format, a...)}
}

func usage(format string, a ...any) error {
	return &exitcode.Error{Exit: exitcode.ExitUsage, Message: fmt.Sprintf(format, a...)}
}

func (e Env) workerEnv() worker.Env {
	w := e.Worker
	if w.Git == nil {
		w.Git = e.Git
	}
	return w
}

// Assign checks an item's readiness and, unless CheckOnly, marks it taken and
// hands it to a slot: claim, in-progress state and comment, reset onto
// `<agent>/<issue>-<slug>`, account, dispatch. Each step is skipped when
// already done, so repeating the call resumes it. A failure before dispatch
// undoes the claim and state; a failure at or after dispatch keeps them,
// because the pane may already hold the brief.
func (e Env) Assign(ctx context.Context, root string, be Board, o AssignOpts) (res Assigned, err error) {
	set := o.Settings
	it, err := be.Get(o.ID)
	if err != nil {
		return res, err
	}
	if it.Closed {
		return res, fmt.Errorf("%w: %s is closed", backlog.ErrNotFound, o.ID)
	}
	id := it.ID
	res.ID, res.Type = id, it.Type
	if o.Agent != "" && !contains(set.Roster, o.Agent) {
		return res, usage("--agent %s is not in round.roster (%s)", o.Agent, strings.Join(set.Roster, ", "))
	}
	if o.Tier != "" && !roundcfg.ValidTier(o.Tier) {
		return res, usage("--tier must be one of %s", strings.Join(roundcfg.Tiers, ", "))
	}
	if o.Kind != "" && !harness.Valid(o.Kind) {
		return res, usage("--kind must be one of %s", strings.Join(harness.Kinds, ", "))
	}
	tier := o.Tier
	if tier == "" {
		tier = set.Tier
	}
	reason := strings.TrimSpace(o.TierReason)
	if roundcfg.TierRank(tier) > roundcfg.TierRank(set.Tier) && reason == "" {
		return res, usage("--tier %s is above the default tier %s: say why with --tier-reason", tier, set.Tier)
	}
	if o.BodyFile != "" {
		if _, err := os.Stat(o.BodyFile); err != nil {
			return res, usage("--body-file %s: %v", o.BodyFile, err)
		}
	}

	// 1. This process holds the round's lease.
	cd, err := e.commonDir(ctx, root)
	if err != nil {
		return res, err
	}
	le := e.leaseEnv()
	lease, st, err := le.Read(cd)
	if err != nil {
		return res, err
	}
	holder := le.Discover(o.HolderPID, o.Getenv)
	if (st != roundlease.Live && st != roundlease.Foreign) || !holder.SameAs(lease, le.Host) {
		return res, blocked(BlockNoRound, "this process holds no round lease: run rota round start first")
	}

	// 2. The slot: the named one, else the first idle roster slot, else the
	// first whose PR can be queued. queue marks a slot that holds another
	// issue but is done with a PR: it is parked right before the claim, so a
	// refusal on the way moves nothing.
	reg := worker.LoadRegistry(root)
	var slot *worker.Slot
	resuming, queue := false, false
	if o.Agent != "" {
		slot = reg.Slot(o.Agent)
		if slot == nil {
			return res, &exitcode.Error{Exit: exitcode.ExitResolution, Message: fmt.Sprintf("slot %s is not provisioned: run rota round start", o.Agent)}
		}
	} else {
		for _, name := range set.Roster {
			s := reg.Slot(name)
			if s != nil && heldID(s.Task(), s.Branch(), name) == strings.ToUpper(id) {
				slot, resuming = s, true
				break
			}
		}
		for _, name := range set.Roster {
			if slot != nil {
				break
			}
			if s := reg.Slot(name); s != nil && heldID(s.Task(), s.Branch(), name) == "" {
				slot = s
			}
		}
		for _, name := range set.Roster {
			if slot != nil {
				break
			}
			if s := reg.Slot(name); s != nil {
				if ok, _ := e.parkable(ctx, s); ok {
					slot = s
				}
			}
		}
		if slot == nil {
			return res, blocked(BlockNoFreeSlot, "every roster slot is busy")
		}
	}
	agent := slot.Name()
	res.Agent = agent
	if h := heldID(slot.Task(), slot.Branch(), agent); h != "" {
		if h != strings.ToUpper(id) {
			ok, why := e.parkable(ctx, slot)
			if !ok {
				return res, blocked(BlockSlotBusy, "%s", busyMsg(agent, h, why))
			}
			queue = true
		} else {
			resuming = true
		}
	}
	res.Branch = BranchName(agent, id, it.Title)

	// Kind, tier and model (C9): the model is the tier's entry for the kind.
	kind := o.Kind
	if kind == "" {
		kind = slot.Kind()
	}
	hz, err := worker.Harness(kind)
	if err != nil {
		return res, err
	}
	kind = hz.Kind()
	// An unset codex tier map is allowed: the default codex command drops
	// --model and Codex picks its own. A custom work.codexCommand holding
	// {model} would fail at dispatch, after the claim, so it is refused here.
	model := set.Model(kind, tier)
	if model == "" && worker.NeedsModel(root, kind) {
		return res, blocked(BlockNoTierMap, "round.tiers.%s has no model for the %s tier: set round.tiers.%s.*", kind, tier, kind)
	}
	res.Kind, res.Tier, res.TierReason = kind, tier, reason
	res.Model = model
	if !worker.ModelAppliesTo(root, kind) {
		res.Model = ""
		res.Warnings = append(res.Warnings, "tier model not applied: "+hz.CommandKey()+" has no {model} placeholder")
	}

	// 3. The scope allows it.
	scope, slate := SlateOf(root)
	if scope == "" {
		scope = set.Scope
	}
	if !resuming {
		ok, err := InScope(root, be, scope, slate, id)
		if err != nil {
			return res, err
		}
		if !ok {
			return res, blocked(BlockOutOfScope, "%s is outside the round's scope (%s)", id, scope)
		}
	}

	if q := reg.QueuedIssue(id); q != nil && !resuming {
		return res, blocked(BlockClaimed, "%s has PR %s in review (from %s): rota round transfer %s --to <slot> picks it up", id, q.PR, q.From, id)
	}

	if !resuming && !o.AcceptOpenPR {
		openPR, err := e.openPRIssues(ctx, be)
		if err != nil {
			return res, err
		}
		if n := openPR[it.Number]; n != 0 {
			return res, blocked(BlockOpenPR, "%s has open PR #%d: not ready; --accept-open-pr assigns it again for a deliberate redo", id, n)
		}
	}

	// 4. Readiness.
	tracked := e.trackedFiles(ctx, root)
	inFlight := e.InFlightItems(ctx, root, be, tracked, set.SharedPaths)
	var settled string
	if o.BodyFile != "" {
		b, _ := os.ReadFile(o.BodyFile)
		settled = string(b)
	}
	r, err := AssessBrief(be, id, tracked, set.SharedPaths, inFlight, o.AcceptOverlap, settled)
	if err != nil {
		return res, err
	}
	res.Readiness = r
	if o.CheckOnly {
		return res, nil
	}
	if !r.Ready() {
		by := BlockNotReady
		if len(r.Checks) == 3 && r.Checks[0].OK && r.Checks[1].OK && !r.Checks[2].OK {
			by = BlockOverlap
		}
		blk := blocked(by, "%s is not ready: %s", id, failedChecks(r))
		blk.Readiness = &r
		return res, blk
	}

	// Solo runs Claude subagents only (E3, #70): a harness that cannot be
	// given a working directory would edit the orchestrator's checkout.
	if why := hz.SoloRefusal(); why != "" && isSolo(root) {
		return res, usage("%s", why)
	}
	// A worker that cannot start (version, host, login) is refused before
	// anything is marked. dispatch runs the same preflight again.
	setup, err := e.workerEnv().Preflight(ctx, root, kind, agent, o.AcceptCodexVersion)
	if err != nil {
		var we *exitcode.Error
		if errors.As(err, &we) && we.Exit == exitcode.ExitRefused {
			if bd, ok := we.Data.(worker.BlockData); ok {
				return res, blocked(bd.BlockedBy, "%s", we.Message)
			}
		}
		return res, err
	}
	res.Warnings = append(res.Warnings, setup.Warnings...)

	// 5. The brief exists before anything is marked.
	brief, ok := briefPath(root, set, o.Getenv)
	if !ok {
		return res, blocked(BlockBriefMissing, "the worker contract (references/worker-contract.md) was not found; set round.brief")
	}

	// 6. Claim, in-progress, comment.
	rnd := registryRound(root)
	claimID := agent + "@" + strconv.Itoa(rnd)
	if queue {
		if err := e.queuePR(ctx, root, agent); err != nil {
			return res, wrap(err)
		}
	}
	won, holderID, err := be.Claim(id, claimID)
	if err != nil {
		return res, err
	}
	if !won {
		return res, blocked(BlockClaimed, "%s is claimed by %s", id, holderID)
	}
	undo := func() {
		res.Changed = false
		be.SetState(id, "none")
		be.Release(id, claimID)
		editSlot(root, agent, func(s *worker.Slot) error { s.Unbind(); return nil })
	}
	changed, err := be.SetState(id, "in-progress")
	if err != nil {
		undo()
		return res, err
	}
	res.Changed = changed || !resuming
	if !resuming {
		if _, err := be.AddComment(id, "feedback", fmt.Sprintf("In progress: agent **%s** on branch `%s`.", agent, res.Branch)); err != nil {
			undo()
			return res, err
		}
	}

	// 7. The slot onto its issue branch.
	w := e.workerEnv()
	if _, err := w.ResetTo(root, agent, id, res.Branch, false); err != nil {
		undo()
		var we *exitcode.Error
		if errors.As(err, &we) && we.Exit == exitcode.ExitRefused {
			return res, blocked(BlockSlotBusy, "%s", we.Message)
		}
		return res, err
	}
	if err := editSlot(root, agent, func(s *worker.Slot) error {
		s.Bind(worker.Binding{Task: id, ClaimID: claimID, Kind: kind, Tier: tier, Model: res.Model, TierReason: reason})
		return nil
	}); err != nil {
		undo()
		return res, err
	}

	// 8. The account is the pane's CLAUDE_CONFIG_DIR: keep the slot's own while
	// it has headroom, else pick; none usable is a refusal to start.
	// work.accounts is Anthropic's: a codex slot's CODEX_HOME is its account.
	// Under solo every subagent runs on the orchestrator's own account.
	solo := isSolo(root)
	if !solo && hz.WorkAccounts() && e.Accounts != nil && len(worker.Configured(root)) > 0 {
		name, err := e.pickAccount(ctx, root, agent)
		if err != nil {
			undo()
			return res, err
		}
		res.Account = name
	}

	// 9. Dispatch the pointer brief.
	decisions := ""
	if o.BodyFile != "" {
		b, _ := os.ReadFile(o.BodyFile)
		decisions = string(b)
	}
	text := pointerBrief(agent, id, res.Branch, brief, o.Siblings, decisions, tierBrief{Kind: kind, Tier: tier, Model: res.Model, Default: set.Tier, Reason: reason, Table: set.Models[kind]})
	if hb := latestHandoffBranch(be, id); hb != "" {
		text += fmt.Sprintf("\nAn earlier worker handed this issue back: read the latest `rota:handoff` comment on it. Its work is pushed on branch %s (origin/%s); fetch it before you start over.\n", hb, hb)
	}
	if solo {
		// No pane: mark the slot busy and hand the brief back.
		brief, wt, err := e.soloHandOff(root, agent, text, rnd)
		if err != nil {
			undo()
			return res, err
		}
		res.Host, res.Brief, res.Worktree = host.Solo, brief, wt
		res.Changed = true
		return res, nil
	}
	tmp, err := os.CreateTemp("", "rota-round-brief-")
	if err != nil {
		undo()
		return res, err
	}
	defer os.Remove(tmp.Name())
	tmp.WriteString(text)
	tmp.Close()
	if _, err := w.Dispatch(ctx, root, worker.DispatchOpts{Slot: agent, BodyFile: tmp.Name(), Task: id, Round: &rnd, Branch: res.Branch, Model: res.Model,
		Kind: kind, AcceptCodexVersion: o.AcceptCodexVersion}); err != nil {
		return res, err
	}
	res.Dispatched, res.Changed = true, true
	return res, nil
}

func (e Env) pickAccount(ctx context.Context, root, agent string) (string, error) {
	cur := ""
	if s := worker.LoadRegistry(root).Slot(agent); s != nil {
		cur = s.Account()
	}
	if cur != "" {
		for _, m := range e.Accounts.Meters(ctx, root) {
			if m.Name == cur && m.Verdict != worker.VerdictCooling {
				return cur, nil
			}
		}
	}
	name, ok := e.Accounts.Pick(ctx, root, nil)
	if !ok {
		return "", &exitcode.Error{Exit: exitcode.ExitUnavailable, Message: "no work.accounts account has headroom: every configured account is cooling down"}
	}
	if _, _, err := e.Accounts.Assign(ctx, root, agent, name); err != nil {
		return "", err
	}
	return name, nil
}

func failedChecks(r Readiness) string {
	var out []string
	for _, c := range r.Checks {
		if !c.OK {
			out = append(out, c.Name+" ("+strings.Join(c.Detail, "; ")+")")
		}
	}
	return strings.Join(out, ", ")
}

func contains(l []string, s string) bool {
	for _, v := range l {
		if v == s {
			return true
		}
	}
	return false
}

func intOf(v any) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case interface{ Int64() (int64, error) }:
		i, _ := t.Int64()
		return int(i)
	}
	return 0
}
