package worker

import (
	"context"
	"errors"
	"fmt"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/rotatree"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/land"
	"github.com/l4ci/rota/internal/overlap"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/tracker"
)

// Merge gate for one worker slot's branch/PR into the cycle branch: the port
// of bin/hv-worker-gate, with the #39 fixes.
//
// Worker-owned branches buy git-native integration and bring back the failure
// class that per-branch verification structurally cannot catch: two workers
// each verify honestly, both branches are green, the merge is textually clean,
// and the cycle branch breaks (a symbol one worker widens while another adds a
// fresh call to it). So this is not a `gh pr merge` wrapper. Per slot, in order:
//
//  1. FRESHNESS: is the cycle branch an ancestor of the worker branch? If not,
//     the worker never merged what landed since it branched and its green is
//     stale. That alone is no refusal (#31): when the merge is clean and the
//     two sides changed no file in common the gate merges it itself and
//     verifies the merged tree; a conflict or a shared file bounces it (see
//     staleReason), and the CLI counts bounces per item. With a recorded PR and an origin remote this is judged
//     on the PUSHED refs after a fetch (origin/<base> vs origin/<branch>),
//     because the PR merges what was pushed, not what sits in a local worktree.
//     The PR's head branch, head SHA and target branch must match the verified
//     ones, so a re-pushed branch or a stacked PR cannot slip through. A git
//     exit code above 1 is the check itself breaking (check-broke), never
//     reported as stale.
//  2. MERGE: through the forge when the slot recorded a PR (pinned to the
//     verified head SHA; glab with auto-merge off, since a scheduled merge is
//     not a merge), else a local `git merge`. A recorded PR with no origin
//     remote is refused, never merged locally. A PR merge is confirmed: its
//     merge commit must be an ancestor of origin/<base> and of the local tree
//     before RE-VERIFY runs.
//  3. RE-VERIFY: on the MERGED tree, never on the branch. This is the only step
//     that catches the two shapes above. Verification commands come from
//     refactor.verifyCommands; when that is empty the gate reports
//     verifySkipped rather than inventing a check it cannot perform.
//
// Between 1 and 2 sits a PROVENANCE check: the PR body's `## Approvals`
// section is cross-checked against the slot's relays[] log. A relay cited as
// the maintainer, a relay round cited that was never sent, or no section at all
// while relays exist is a provenance-fail. Nothing merges on a failed citation.

// Gate verdicts. fresh (with CheckOnly) and pass succeed; the rest are exit 1.
const (
	GateFresh          = "fresh"
	GatePass           = "pass"
	GateStale          = "stale"
	GatePRMismatch     = "pr-mismatch"
	GateProvenanceFail = "provenance-fail"
	GateNotMerged      = "not-merged"
	GateNotOnBase      = "not-on-base"
	GateVerifyFailed   = "verify-failed"
	GateMergedRemotely = "merged-remotely"
	GateMergeFailed    = "merge-failed"
	GateCheckBroke     = "check-broke"
	// GateApprovalRequired: the merge-approval gate (B1) refused before the
	// merge; the CLI exits 4 with it.
	GateApprovalRequired = "approval-required"
)

// GateOpts are the flags of `rota worker gate`.
type GateOpts struct {
	Slot      string
	Base      string
	CheckOnly bool
	NoVerify  bool
	// Train marks a landing step of a merge train (see Train): the PRs were
	// merged together and verified once, so a branch behind the base only
	// because an earlier train member landed is not refused as stale.
	Train bool
	// Approve is the merge-approval gate (B1), run after provenance and right
	// before the merge, never under CheckOnly. files lists the paths the merge
	// changes. A non-nil error stops the gate with verdict approval-required
	// and is returned as is.
	Approve func(files func() ([]string, error)) error
}

// GateResult is the gate's answer. Err is the message for a non-success
// verdict, Hint what to do about it.
type GateResult struct {
	Slot          string
	Verdict       string
	Base          string
	Branch        string
	SHA           string
	PR            string
	Verified      []string
	VerifySkipped bool
	Changed       bool
	Err           string
	Hint          string
	Notes         []string // PROVENANCE-SKIP and NO-VERIFY lines for stderr
}

// OK reports a successful verdict.
func (r GateResult) OK() bool { return r.Verdict == GateFresh || r.Verdict == GatePass }

// GateTarget is what a gate argument resolves to: a slot, or the queued record
// of a PR whose slot moved on. Both carry the branch, PR, base and relay log the
// gate reads; Name and Task belong to a slot, Issue to a queued record.
type GateTarget struct {
	Queued                              bool
	Name, Branch, PR, Base, Task, Issue string
	relays                              []any
}

func gateSlot(s *Slot) GateTarget {
	return GateTarget{Name: s.Name(), Branch: s.Branch(), PR: s.PR(), Base: s.Base(), Task: s.Task(), relays: s.Relays()}
}

func gateQueued(q QueuedPR) GateTarget {
	return GateTarget{Queued: true, Issue: q.Issue, Branch: q.Branch, PR: q.PR, Base: q.Base, relays: q.Relays}
}

// GateTarget resolves the argument of `rota worker gate`: a slot name, or a PR (`#N`, `N`
// or its URL) resolving to the queued record of a PR whose slot moved on, else
// to a slot recording that PR. A queued record stands in for the slot.
//
// A slot that records no PR while a record queued from it exists is refused:
// the habitual `gate <slot>` would otherwise merge the slot's NEW branch.
func (r Registry) GateTarget(arg string) (GateTarget, error) {
	if sl := r.Slot(arg); sl != nil {
		if sl.PR() == "" {
			for _, q := range r.PRs() {
				if q.From == arg {
					return GateTarget{}, &exitcode.Error{Exit: exitcode.ExitUsage,
						Message: fmt.Sprintf("slot %s records no PR, but its PR %s (%s) waits in review", arg, q.PR, q.Branch),
						Hint:    fmt.Sprintf("gate the PR in review with `rota worker gate %s`", prNumText(q.PR))}
				}
			}
		}
		return gateSlot(sl), nil
	}
	if n, ok := PRRefNumber(arg); ok {
		if q := r.QueuedPR(arg); q != nil {
			return gateQueued(*q), nil
		}
		for _, sl := range r.Slots() {
			if m, ok := PRRefNumber(sl.PR()); ok && m == n {
				return gateSlot(sl), nil
			}
		}
		return GateTarget{}, fail(exitcode.ExitResolution, fmt.Sprintf("no PR in review or slot records PR #%d", n))
	}
	return GateTarget{}, fail(exitcode.ExitResolution, fmt.Sprintf("slot '%s' is not in the pool", arg))
}

// Forge is the part of tracker.Adapter the gate merges through: read the PR,
// then ask the forge to merge it pinned to the verified head. Provider
// differences (argv, JSON shape, auto-merge) live behind it in internal/tracker.
type Forge interface {
	PRView(ctx context.Context, pr int) (tracker.PRInfo, error)
	PRRequestMerge(ctx context.Context, pr int, o tracker.MergeOpts) error
	// OpenPRs lists the open PRs, so a slot that records none can be matched
	// to the PR its branch heads.
	OpenPRs(ctx context.Context) ([]tracker.PR, error)
	// ClosedNumbers, Get and RemoveLabels let a landed PR release the claim
	// label on the issues it closed.
	ClosedNumbers(body string) []int
	Get(ctx context.Context, number int, withComments bool) (tracker.Issue, error)
	RemoveLabels(ctx context.Context, number int, labels []string) error
}

// Gate runs the merge gate for a slot or a queued PR (see GateTarget). A
// passing gate of a queued PR drops its record. The registry, config and
// verification commands are read here, once; the steps below take them as
// given.
func (e Env) Gate(ctx context.Context, root string, o GateOpts) (GateResult, error) {
	res := GateResult{Slot: o.Slot, Base: o.Base}
	reg := LoadRegistry(root)
	if !reg.Exists {
		return res, fail(exitcode.ExitResolution, "no worker pool — run rota worker pool init first")
	}
	t, err := reg.GateTarget(o.Slot)
	if err != nil {
		return res, err
	}
	in := gateInput{
		cfg: config.Load(rotatree.Config(root)),
		// Read before the merge: the branch lands in root and may carry its own
		// .rota/config.json, which must not decide how it is verified.
		verifyCmds: verifyCommandsAt(root),
	}
	res, err = e.gateEnv().gate(ctx, root, o, res, in, t)
	if err == nil && t.Queued && res.Verdict == GatePass {
		if err := RemoveQueuedPR(root, t.PR); err != nil {
			return res, err
		}
	}
	return res, err
}

// gateInput is what the gate reads from disk before it starts.
type gateInput struct {
	cfg        any
	verifyCmds []string
}

// gateEnv is the slice of Env the gate touches.
type gateEnv struct {
	ctx    context.Context
	git    git.Runner
	forge  func(provider, dir string, cfg any) (Forge, error)
	getenv func(string) string
	sleep  func(time.Duration)
	shell  func(ctx context.Context, dir, command string) (string, int)
}

func (e Env) gateEnv() gateEnv {
	e = e.withDefaults()
	return gateEnv{ctx: e.context(), git: e.Git, forge: e.Forge, getenv: e.Getenv, sleep: e.Sleep, shell: e.Shell}
}

// runGit runs git and trims one trailing newline from stdout, like $(...).
func (e gateEnv) runGit(dir string, args ...string) (string, int) {
	res, err := e.git(e.ctx, dir, args...)
	if err != nil {
		return "", 127
	}
	return strings.TrimRight(res.Stdout, "\n"), res.ExitCode
}

// gateStep is one stage of the gate. done ends the gate: the verdict is
// already in g.res, or err says why it could not run.
type gateStep func(g *gate) (done bool, err error)

// gateSteps run in order; see the numbered stages in the file comment.
var gateSteps = []gateStep{
	(*gate).stepForge,
	(*gate).stepAdoptPR,
	(*gate).stepRemote,
	(*gate).stepRefs,
	(*gate).stepFreshness,
	(*gate).stepPRMatches,
	(*gate).stepProvenance,
	(*gate).stepMerge,
	(*gate).stepVerify,
}

func (e gateEnv) gate(ctx context.Context, root string, o GateOpts, res GateResult, in gateInput, t GateTarget) (GateResult, error) {
	e.ctx = ctx
	res.Branch, res.PR = t.Branch, t.PR
	g := &gate{e: e, ctx: ctx, root: root, res: &res, o: o, in: in, target: t, branch: t.Branch, pr: t.PR}
	for _, step := range gateSteps {
		if done, err := step(g); done || err != nil {
			return res, err
		}
	}
	return res, nil
}

// stepForge picks the provider and opens its forge.
func (g *gate) stepForge() (bool, error) {
	g.provider = g.e.detectProvider(g.root, g.pr)
	g.cliName = tracker.CLIName(g.provider)
	g.label = config.Label(g.in.cfg, "inProgress")
	var err error
	if g.forge, err = g.e.forge(g.provider, g.root, g.in.cfg); err != nil {
		return g.broke(fmt.Sprintf("cannot reach the %s forge: %v", g.provider, err))
	}
	return false, nil
}

// stepAdoptPR looks a PR up by head when the slot recorded none. A worker can
// open its PR without the slot recording it. A local merge would then land on
// the local base only, leave the PR open and still report a pass, so look the
// PR up by head before taking that path.
func (g *gate) stepAdoptPR() (bool, error) {
	if g.pr != "" || g.target.Queued || g.branch == "" {
		return false, nil
	}
	adopted, msg := g.openPRForBranch()
	if msg != "" {
		return g.broke(msg)
	}
	if adopted == "" {
		return false, nil
	}
	g.pr, g.res.PR = adopted, adopted
	g.res.Notes = append(g.res.Notes, fmt.Sprintf("PR-ADOPTED %s — the slot recorded no PR; open PR %s is headed by %s", g.o.Slot, adopted, g.branch))
	if !g.o.CheckOnly {
		UpdateSlot(g.root, g.o.Slot, func(s *Slot) { s.SetPR(adopted) })
	}
	return false, nil
}

// stepRemote decides whether the gate reads origin/* refs. A PR merges what was
// PUSHED, so with a PR and an origin remote the gate reads origin/* refs.
// Without a PR the local merge takes the local branch, and local refs are the
// right ones. A recorded PR with no origin remote is refused: a local merge
// would leave the PR open and bypass its review, CI and branch protection while
// reporting MERGED.
func (g *gate) stepRemote() (bool, error) {
	if g.pr == "" {
		return false, nil
	}
	g.prNum = prNumText(g.pr)
	if _, code := g.e.runGit(g.root, "remote", "get-url", "origin"); code != 0 {
		return g.broke(fmt.Sprintf("slot %s has PR %s but this repo has no 'origin' remote; refusing a local merge that would bypass the PR", g.o.Slot, g.pr))
	}
	if g.prNum == "" {
		return g.broke(fmt.Sprintf("cannot read a PR number from '%s'", g.pr))
	}
	g.remote = true
	return false, nil
}

// stepRefs resolves the refs the later steps judge: fetched origin refs for a
// remote gate, the local branch otherwise.
func (g *gate) stepRefs() (bool, error) {
	e, root, o := g.e, g.root, g.o
	if _, code := e.runGit(root, "rev-parse", "--verify", "--quiet", o.Base); code != 0 {
		return true, fail(exitcode.ExitResolution, fmt.Sprintf("base branch '%s' does not exist", o.Base))
	}
	if !g.remote {
		g.headRef, g.baseRef = g.branch, o.Base
		var code int
		// The commit the gates below judge is the commit that lands.
		if g.verified, code = e.runGit(root, "rev-parse", "--verify", "--quiet", g.branch); code != 0 || g.verified == "" {
			return true, fail(exitcode.ExitResolution, fmt.Sprintf("worker branch '%s' does not exist", g.branch))
		}
		return false, nil
	}
	if _, code := e.runGit(root, "fetch", "origin", "-q"); code != 0 {
		return g.broke("git fetch origin failed")
	}
	g.headRef, g.baseRef = "origin/"+g.branch, "origin/"+o.Base
	if _, code := e.runGit(root, "rev-parse", "--verify", "--quiet", g.headRef+"^{commit}"); code != 0 {
		return g.broke(fmt.Sprintf("%s does not exist — has the worker pushed %s?", g.headRef, g.branch))
	}
	if _, code := e.runGit(root, "rev-parse", "--verify", "--quiet", g.baseRef+"^{commit}"); code != 0 {
		return g.broke(g.baseRef + " does not exist")
	}
	return false, nil
}

// stepFreshness is stage 1. Three-way on the exit code: `&& FRESH || STALE`
// would call a check that itself errors STALE.
func (g *gate) stepFreshness() (bool, error) {
	e, o := g.e, g.o
	switch _, code := e.runGit(g.root, "merge-base", "--is-ancestor", g.baseRef, g.headRef); code {
	case 0:
		return false, nil
	case 1:
	default:
		return g.broke(fmt.Sprintf("git merge-base --is-ancestor %s %s exited %d", g.baseRef, g.headRef, code))
	}
	behind, c := e.runGit(g.root, "rev-list", "--count", g.headRef+".."+g.baseRef)
	if c != 0 {
		behind = "?"
	}
	var why, brokeMsg string
	if !o.Train { // a train already merged every PR cleanly, in order, in its scratch tree
		why, brokeMsg = g.staleReason(g.in.cfg)
	}
	if brokeMsg != "" {
		return g.broke(brokeMsg)
	}
	if why != "" {
		g.res.SHA, _ = e.runGit(g.root, "rev-parse", "--short=7", g.headRef) // bounce accounting keys on the head
		g.verdict(GateStale, fmt.Sprintf("STALE %s %s — %s commit(s) landed on %s since it branched; %s", o.Slot, g.branch, behind, o.Base, why),
			fmt.Sprintf("bounce: tell slot %s to `git merge %s`, resolve and re-verify, then re-gate", o.Slot, o.Base))
		return true, nil
	}
	if !o.Train {
		g.res.Notes = append(g.res.Notes, fmt.Sprintf("STALE-MERGE %s — %s commit(s) landed on %s since %s branched; none touch its files and the merge is clean, merging as is", o.Slot, behind, o.Base, g.branch))
	}
	return false, nil
}

// stepPRMatches checks the PR is the thing that was just verified: open, aimed
// at the gate's base (a PR stacked on another worker's branch merges THERE, not
// here), and headed by the pushed commit.
func (g *gate) stepPRMatches() (bool, error) {
	if !g.remote {
		return false, nil
	}
	var code int
	if g.verified, code = g.e.runGit(g.root, "rev-parse", g.headRef); code != 0 || g.verified == "" {
		return g.broke(fmt.Sprintf("git rev-parse %s failed (exit %d)", g.headRef, code))
	}
	info, ok := g.prInfo()
	if !ok {
		return g.broke(fmt.Sprintf("could not read PR %s from %s", g.prNum, g.provider))
	}
	mismatch := func(msg string) (bool, error) {
		g.verdict(GatePRMismatch, "error: "+msg, "")
		return true, nil
	}
	// OPEN, MERGED or CLOSED on both forges.
	switch {
	case info.State != "OPEN":
		return mismatch(fmt.Sprintf("PR %s is %s, not open", g.prNum, info.State))
	case info.Base != g.o.Base:
		return mismatch(fmt.Sprintf("PR %s targets '%s', the gate's base is '%s' — stacked PR?", g.prNum, info.Base, g.o.Base))
	case info.Head != g.branch:
		return mismatch(fmt.Sprintf("PR %s is headed by '%s', slot %s verified '%s'", g.prNum, info.Head, g.o.Slot, g.branch))
	case info.HeadSHA != g.verified:
		return mismatch(fmt.Sprintf("PR %s head is %s, the verified %s is %s — pushed since?", g.prNum, info.HeadSHA, g.headRef, g.verified))
	}
	return false, nil
}

// stepProvenance runs the approvals check. Under CheckOnly it also ends the
// gate with verdict fresh.
func (g *gate) stepProvenance() (bool, error) {
	failMsg, brokeMsg := g.checkProvenance()
	if brokeMsg != "" {
		return g.broke(brokeMsg)
	}
	if failMsg != "" {
		g.res.SHA, _ = g.e.runGit(g.root, "rev-parse", "--short=7", g.headRef)
		g.verdict(GateProvenanceFail, failMsg, "")
		return true, nil
	}
	if g.o.CheckOnly {
		// the checked tip: origin/<branch> when a PR is recorded
		g.res.SHA, _ = g.e.runGit(g.root, "rev-parse", "--short=7", g.headRef)
		g.res.Verdict = GateFresh
		return true, nil
	}
	return false, nil
}

// stepMerge is stage 2: the approval gate, then the merge itself.
func (g *gate) stepMerge() (bool, error) {
	cur, _ := g.e.runGit(g.root, "rev-parse", "--abbrev-ref", "HEAD")
	if cur != g.o.Base {
		return true, fail(exitcode.ExitResolution, fmt.Sprintf("gate must run with %s checked out (currently on %s)", g.o.Base, cur))
	}
	if g.o.Approve != nil {
		if err := g.o.Approve(g.changedFiles); err != nil {
			g.res.Verdict = GateApprovalRequired
			return true, err
		}
	}
	if g.remote {
		if g.mergeRemote() {
			return true, nil
		}
	} else if done := g.mergeLocal(); done {
		return true, nil
	}
	g.res.Changed = true
	g.res.SHA, _ = g.e.runGit(g.root, "rev-parse", "--short=7", "HEAD")
	return false, nil
}

// changedFiles lists the paths the merge changes, for the approval gate.
func (g *gate) changedFiles() ([]string, error) {
	out, code := g.e.runGit(g.root, "diff", "--name-only", g.baseRef+"..."+g.verified)
	if code != 0 {
		return nil, fmt.Errorf("git diff --name-only %s...%s exited %d", g.baseRef, g.verified, code)
	}
	var list []string
	for _, l := range strings.Split(out, "\n") {
		if l != "" {
			list = append(list, l)
		}
	}
	return list, nil
}

// mergeLocal merges the local branch into the base. done is true when it ends
// the gate with a verdict.
func (g *gate) mergeLocal() (done bool) {
	run := func(args ...string) (git.Result, error) { return g.e.git(g.e.ctx, g.root, args...) }
	err := land.MergeLocal(run, g.verified, fmt.Sprintf("merge: %s into %s", g.branch, g.o.Base))
	if err == nil {
		return false
	}
	// Only a real conflict is called one. Anything else (no committer
	// identity, a hook, a locked index) is reported with git's own words,
	// so it is not mistaken for work to resolve with the slot.
	var me *land.MergeError
	switch {
	case errors.As(err, new(*land.ConflictError)):
		g.verdict(GateMergeFailed, fmt.Sprintf("error: merge of %s into %s conflicted — resolve with the slot that owns the context", g.branch, g.o.Base), "")
	case errors.As(err, &me):
		g.verdict(GateMergeFailed, fmt.Sprintf("error: merge of %s into %s failed (exit %d): %s", g.branch, g.o.Base, me.Code, strings.TrimSpace(me.Out)), "")
	default:
		g.verdict(GateMergeFailed, fmt.Sprintf("error: merge of %s into %s failed (exit 127): %s", g.branch, g.o.Base, err), "")
	}
	return true
}

// stepVerify is stage 3: re-verify on the merged tree.
func (g *gate) stepVerify() (bool, error) {
	res, o := g.res, g.o
	if o.NoVerify {
		res.Verdict, res.VerifySkipped = GatePass, true
		return true, nil
	}
	vr, err := runVerifyCmds(g.ctx, g.e.shell, g.in.verifyCmds, g.root)
	if err != nil {
		return true, err
	}
	if vr.NoCommands {
		res.Verdict, res.VerifySkipped = GatePass, true
		res.Notes = append(res.Notes, fmt.Sprintf("NO-VERIFY %s — refactor.verifyCommands is empty; merged tree was NOT gated by a command.", o.Slot),
			"set refactor.verifyCommands via rota config set to make this gate real")
		return true, nil
	}
	res.Verified = vr.Verified
	for _, c := range vr.Failed {
		res.Notes = append(res.Notes, "verify FAILED: "+c)
	}
	if !vr.OK() {
		g.verdict(GateVerifyFailed, fmt.Sprintf("GATE-FAIL %s — merged tree does not pass verification at %s\nlast lines of the verify output (full log: %s):\n%s",
			o.Slot, res.SHA, vr.LogPath, indentTail(vr.Log, 20)),
			fmt.Sprintf("fix forward on %s; the owning slot has usually moved on", o.Base))
		return true, nil
	}
	res.Verdict = GatePass
	return true, nil
}

// staleReason says why a branch behind the base must go back to its worker:
// "" when the merge is clean and the two sides changed no file in common, so
// the gate merges it itself and verifies the merged tree. A conflict needs the
// worker's context. A shared file changed on both sides can merge textually
// clean and still break (one side widens a symbol, the other adds a call), and
// that breakage would land on the base before RE-VERIFY sees it. Files matching
// round.sharedPaths are ignored, as the readiness overlap check ignores them.
// brokeMsg is set when a git check itself fails.
func (g *gate) staleReason(cfg any) (why, brokeMsg string) {
	e, root := g.e, g.root
	if _, code := e.runGit(root, "merge-base", "--is-ancestor", g.headRef, g.baseRef); code == 0 {
		return fmt.Sprintf("its work is already on %s, nothing to merge", g.o.Base), ""
	}
	switch out, code := e.runGit(root, "merge-tree", "--write-tree", "--no-messages", g.baseRef, g.headRef); code {
	case 0:
	case 1:
		return "the merge conflicts", ""
	default:
		return "", fmt.Sprintf("git merge-tree %s %s exited %d: %s", g.baseRef, g.headRef, code, out)
	}
	mb, code := e.runGit(root, "merge-base", g.baseRef, g.headRef)
	if code != 0 || mb == "" {
		return "", fmt.Sprintf("git merge-base %s %s exited %d", g.baseRef, g.headRef, code)
	}
	changed := func(ref string) ([]string, bool) {
		out, code := e.runGit(root, "diff", "--name-only", "--no-renames", mb, ref)
		if code != 0 {
			return nil, false
		}
		return strings.Split(out, "\n"), true
	}
	onBase, ok1 := changed(g.baseRef)
	onHead, ok2 := changed(g.headRef)
	if !ok1 || !ok2 {
		return "", fmt.Sprintf("git diff --name-only against %s failed", mb)
	}
	both := overlap.Both(onBase, onHead, roundcfg.SharedPaths(cfg))
	if len(both) == 0 {
		return "", ""
	}
	return "both sides changed " + strings.Join(both, ", "), ""
}

func appendFile(path, text string) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	f.WriteString(text)
}

func indentTail(text string, n int) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return "    " + strings.Join(lines, "\n    ")
}

// detectProvider reads the provider from the PR URL (a bare number carries
// none), then origin, and falls back to github, which is what this gate always
// assumed.
func (e gateEnv) detectProvider(root, pr string) string {
	switch {
	case strings.Contains(pr, "/-/merge_requests/"):
		return "gitlab"
	case strings.Contains(pr, "/pull/"):
		return "github"
	}
	url, _ := e.runGit(root, "remote", "get-url", "origin")
	for _, u := range []string{pr, url} {
		if p := tracker.ProviderFromURL(u); p != tracker.ProviderUnknown {
			return p
		}
	}
	return "github"
}

type gate struct {
	e        gateEnv
	ctx      context.Context
	root     string
	res      *GateResult
	o        GateOpts
	in       gateInput
	target   GateTarget
	branch   string
	pr       string
	prNum    string
	provider string
	forge    Forge
	cliName  string
	label    string // issues.labels.inProgress: the claim label a merge releases
	remote   bool
	headRef  string
	baseRef  string
	verified string
}

// verdict records a non-success verdict.
func (g *gate) verdict(v, msg, hint string) {
	g.res.Verdict, g.res.Err, g.res.Hint = v, msg, hint
}

func (g *gate) broke(msg string) (bool, error) {
	g.verdict(GateCheckBroke, fmt.Sprintf("CHECK-BROKE %s — %s", g.o.Slot, msg), "")
	return true, nil
}

// prInfo reads the PR; ok is false when the forge could not be read.
func (g *gate) prInfo() (tracker.PRInfo, bool) {
	n, err := strconv.Atoi(g.prNum)
	if err != nil {
		return tracker.PRInfo{}, false
	}
	info, err := g.forge.PRView(g.ctx, n)
	return info, err == nil
}

// openPRForBranch finds the open PR headed by the gate's branch. Without an
// origin remote or a forge CLI there is no remote to hold one, and the local
// merge stands. Any other failure to ask is a check-broke message: guessing
// "none" would fail open into a local merge of a branch whose PR may be open.
func (g *gate) openPRForBranch() (url, brokeMsg string) {
	if _, code := g.e.runGit(g.root, "remote", "get-url", "origin"); code != 0 {
		return "", ""
	}
	prs, err := g.forge.OpenPRs(g.ctx)
	if err != nil {
		if tracker.IsKind(err, tracker.KindUnavailable) && strings.Contains(err.Error(), "is not installed") {
			return "", ""
		}
		return "", fmt.Sprintf("slot %s records no PR and the open PRs of %s could not be listed to look for one headed by %s: %v", g.o.Slot, g.provider, g.branch, err)
	}
	for _, p := range prs {
		if p.Branch == g.branch {
			return p.URL, ""
		}
	}
	return "", ""
}

// prBody is the PR/MR description. An error means it could not be read.
func (g *gate) prBody() (string, error) {
	n, ok := PRRefNumber(g.pr)
	if !ok {
		return "", fmt.Errorf("no PR number in %q", g.pr)
	}
	info, err := g.forge.PRView(g.ctx, n)
	return info.Body, err
}

// checkProvenance cross-checks the PR body's approvals against the slot's
// relay log. It returns the PROVENANCE-FAIL message, or a check-broke message
// when the PR body cannot be read; both "" for pass or skip.
//
// Only a missing forge CLI may skip. Reading the body failing for any other
// reason (not authenticated, rate limited, PR gone, unparseable reply) used
// to be a skip too, in the Python gate as well, so the gate merged without
// having looked at the approvals. That fails open; here it is check-broke.
func (g *gate) checkProvenance() (failMsg, brokeMsg string) {
	if g.pr == "" {
		g.res.Notes = append(g.res.Notes, fmt.Sprintf("PROVENANCE-SKIP %s — no recorded PR (or no %s) to read approvals from", g.o.Slot, g.cliName))
		return "", ""
	}
	body, err := g.prBody()
	if err != nil {
		if tracker.IsKind(err, tracker.KindUnavailable) && strings.Contains(err.Error(), "is not installed") {
			g.res.Notes = append(g.res.Notes, fmt.Sprintf("PROVENANCE-SKIP %s — no %s to read approvals from", g.o.Slot, g.cliName))
			return "", ""
		}
		return "", fmt.Sprintf("could not read the body of %s to check its approvals: %v", g.pr, err)
	}
	if msg := checkApprovals(body, g.target.relays); msg != "" {
		return fmt.Sprintf("PROVENANCE-FAIL %s: %s", g.o.Slot, msg), ""
	}
	return "", ""
}

// mergeRemote merges through the forge and confirms it landed. done is true
// when it ends the gate with a verdict.
func (g *gate) mergeRemote() (done bool) {
	// Pinned to the verified SHA so a push after the check is refused. The
	// adapter turns auto-merge off where the forge would otherwise schedule a
	// merge that reports success and merges nothing.
	n, _ := strconv.Atoi(g.prNum)
	if err := land.RequestForge(g.ctx, g.forge, n, g.verified, false); err != nil {
		g.verdict(GateMergeFailed, fmt.Sprintf("error: %s merge failed for %s:\n%s", g.cliName, g.pr, tailLines(err.Error(), 20)), "")
		return true
	}
	sha, done := g.awaitMerged()
	if done {
		return true
	}
	return g.confirmLanded(sha)
}

// awaitMerged waits for the forge to report the PR merged and returns its merge
// commit. The tracker's word is not enough, and the merge commit can take a
// moment to appear. State not merged is its own verdict (a scheduled auto-merge
// reports success and merges nothing). A fast-forward or rebase merge has no
// merge commit on either provider: state merged with an empty one falls back to
// the pinned verified SHA, which must then be on the base.
func (g *gate) awaitMerged() (sha string, done bool) {
	wait := 2 * time.Second
	if v := g.e.getenv("ROTA_GATE_SHA_WAIT"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			wait = time.Duration(f * float64(time.Second))
		}
	}
	var state, body string
	for i := 0; i < 5; i++ {
		info, ok := g.prInfo()
		if !ok {
			g.broke(fmt.Sprintf("could not re-read PR %s after merging", g.prNum))
			return "", true
		}
		state, sha, body = info.State, info.MergeSHA, info.Body
		if state == "MERGED" && sha != "" {
			break
		}
		if state != "MERGED" {
			g.e.sleep(wait)
		}
	}
	if state != "MERGED" {
		g.verdict(GateNotMerged, fmt.Sprintf("NOT-MERGED %s — PR %s is %s after the merge call; nothing is on %s", g.o.Slot, g.prNum, state, g.o.Base), "")
		return "", true
	}
	g.releaseClaimLabels(body)
	if sha == "" {
		sha = g.verified
	}
	return sha, false
}

// confirmLanded checks the merge commit is on origin/<base> and fast-forwards
// the local base to it. Past this point the PR IS merged: local trouble is
// reported distinctly so nobody retries an already-merged PR.
func (g *gate) confirmLanded(sha string) (done bool) {
	e, o := g.e, g.o
	if _, code := e.runGit(g.root, "fetch", "origin", "-q"); code != 0 {
		g.broke("git fetch origin failed after merging")
		return true
	}
	switch _, code := e.runGit(g.root, "merge-base", "--is-ancestor", sha, g.baseRef); code {
	case 0:
	case 1:
		g.verdict(GateNotOnBase, fmt.Sprintf("NOT-ON-BASE %s — merge commit %s is not an ancestor of %s (merged into another branch?)", o.Slot, sha, g.baseRef), "")
		return true
	default:
		g.broke(fmt.Sprintf("git merge-base --is-ancestor %s %s exited %d", sha, g.baseRef, code))
		return true
	}
	g.res.Changed = true
	if len(sha) >= 7 {
		g.res.SHA = sha[:7] // the merge that landed on origin, for the merged-remotely verdicts
	}
	if _, code := e.runGit(g.root, "merge", "--ff-only", g.baseRef); code != 0 {
		g.verdict(GateMergedRemotely, fmt.Sprintf("MERGED-REMOTELY %s — PR %s is on %s but local %s could not fast-forward (diverged); do not re-merge, reconcile %s by hand", o.Slot, g.prNum, g.baseRef, o.Base, o.Base), "")
		return true
	}
	if _, code := e.runGit(g.root, "merge-base", "--is-ancestor", sha, "HEAD"); code != 0 {
		g.verdict(GateMergedRemotely, fmt.Sprintf("MERGED-REMOTELY %s — PR %s is on %s but %s is not in the local %s; do not re-merge, reconcile %s by hand", o.Slot, g.prNum, g.baseRef, sha, o.Base, o.Base), "")
		return true
	}
	return false
}

// releaseClaimLabels drops the in-progress label from every issue the merged PR
// closes. The forge closes them but leaves the label, so the label would stop
// meaning "being worked on". An issue still open (the PR landed on a branch
// other than the default, or the forge has not closed it yet) keeps its label.
// Best effort: the PR is already merged, so a failure is a note, and
// `rota round reconcile --apply` clears whatever this missed.
func (g *gate) releaseClaimLabels(body string) {
	if g.label == "" {
		return
	}
	for _, n := range g.forge.ClosedNumbers(body) {
		is, err := g.forge.Get(g.ctx, n, false)
		if err != nil {
			g.res.Notes = append(g.res.Notes, fmt.Sprintf("LABEL-KEPT %s — cannot read #%d to release %s: %v", g.o.Slot, n, g.label, err))
			continue
		}
		if is.State != "closed" || !slices.Contains(is.Labels, g.label) {
			continue
		}
		if err := g.forge.RemoveLabels(g.ctx, n, []string{g.label}); err != nil {
			g.res.Notes = append(g.res.Notes, fmt.Sprintf("LABEL-KEPT %s — cannot remove %s from #%d: %v", g.o.Slot, g.label, n, err))
		}
	}
}

func tailLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
