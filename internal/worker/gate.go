package worker

import (
	"context"
	"errors"
	"fmt"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/land"
	"github.com/l4ci/rota/internal/overlap"
	"github.com/l4ci/rota/internal/strutil"
	"github.com/l4ci/rota/internal/testledger"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/verdict"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
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
//     stale. That alone is no refusal (#31): when the merge is clean the gate
//     merges it itself and verifies the merged tree, naming any file both sides
//     changed in the note; a conflict bounces it (see staleReason), and the CLI counts bounces per item. With a recorded PR and an origin remote this is judged
//     on the PUSHED refs after a fetch (origin/<base> vs origin/<branch>),
//     because the PR merges what was pushed, not what sits in a local worktree.
//     The PR's head branch, head SHA and target branch must match the verified
//     ones, so a re-pushed branch or a stacked PR cannot slip through. A git
//     exit code above 1 is the check itself breaking (check-broke), never
//     reported as stale.
//  2. VERIFY: on the MERGED tree, never on the branch. This is the only step
//     that catches the two shapes above. The PR head is merged into the base in
//     a scratch tree and test.full, then test.e2e, run there (or CI checks it,
//     under test.fullWhere ci) before anything lands (#494), so a red result
//     bounces the slot instead of leaving the base to fix forward. The base
//     must not have moved meanwhile (base-moved). When test.full is empty the
//     gate reports verifySkipped rather than inventing a check it cannot perform.
//     Before it, a PR headed for the forge merge is checked for mergeability
//     there: a conflict the forge sees and local git does not ends the gate as
//     merge-failed without a verify run (unknown and errors proceed).
//  3. MERGE: through the forge when the slot recorded a PR (pinned to the
//     verified head SHA; glab with auto-merge off, since a scheduled merge is
//     not a merge), else a local `git merge`. A recorded PR with no origin
//     remote is refused, never merged locally. A PR merge is confirmed: its
//     merge commit must be an ancestor of origin/<base> and of the local tree.
//     Its tree must be the verified one; when the base moved before the forge
//     merged, the landed base is verified again (VERIFY-AGAIN) and a red result
//     there is fixed forward.
//
// The pre-merge refusals (ledger expiry, no-verify, CI-config, approval) are
// shared with the train and live in premerge.go; the gate runs them as one
// stepPremerge, and stage 3 is the merge alone.
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
	// GateVerdictBlocked: a recorded FAIL verdict (B3) refused before the
	// merge; the CLI exits 4 with it, blockedBy verdict.
	GateVerdictBlocked = "verdict-blocked"
	// GateNoVerify: test.full is empty, so a merge would land unverified; the
	// CLI exits 4 with it, blockedBy no-verify, unless --no-verify was passed.
	GateNoVerify = "no-verify"
	// GateNotClosing: the PR body has no closing keyword for the slot's issue,
	// so the merge would leave it open and drifted; the CLI exits 4 with it,
	// blockedBy closes. An issue labelled PartialSliceLabel is exempt.
	GateNotClosing = "closes"
	// GateBestOfUnpicked: the PR is an attempt of a best-of:2 issue and no
	// `rota round pick` names it; the CLI exits 4 with it, blockedBy best-of-unpicked.
	GateBestOfUnpicked = "best-of-unpicked"
	// GateReviewMissing: the review depth ship.review resolves for the branch
	// needs a recorded verdict the branch lacks; the CLI exits 4 with it,
	// blockedBy review-missing.
	GateReviewMissing = "review-missing"
)

// PartialSliceLabel marks an issue whose PR lands only a slice of it, so the PR
// body must not close it.
const PartialSliceLabel = "partial-slice"

// noVerifyRefusal is the message and hint of the refusal for an empty
// test.full under a local verify, shared by the gate and the train.
func noVerifyRefusal(what string) (msg, hint string) {
	return fmt.Sprintf("%s refused — test.full is empty (read from config key %s), so nothing would verify the merged tree; nothing landed", what, config.TestFullKey),
		"set it with `rota config set test.full <command>`, or pass --no-verify to merge unverified on purpose"
}

// GateOpts are the flags of `rota worker gate`.
type GateOpts struct {
	Slot      string
	Base      string
	CheckOnly bool
	NoVerify  bool
	// Prune makes a pass that releases an adopted slot also remove its
	// worktree and branch.
	Prune bool
	// Round reads the round lease for the gate's ledger entries; the gate makes
	// one when nil. A caller that writes more entries after the gate shares it.
	Round *RoundMemo
	// Train marks a landing step of a merge train (see Train): the PRs were
	// merged together and verified once, so a branch behind the base only
	// because an earlier train member landed is not refused as stale.
	Train bool
	// HoldsLandLock says the caller already holds the repository's land lock
	// (the train does, through its landing steps), so the gate does not retake
	// it. Without it a gate takes the lock itself unless it is CheckOnly.
	HoldsLandLock bool
	// Verdict is the review-verdict gate (B3), run after provenance, also under
	// CheckOnly. A non-nil error (a recorded FAIL) stops the gate with verdict
	// verdict-blocked and is returned as is. branch is the worker branch.
	Verdict func(branch string) error
	// Recorded lists the review kinds (verdict.ReviewSpec, verdict.ReviewQuality)
	// with a verdict recorded for branch at head (the checked tip's full sha;
	// a record on an older commit is stale and does not count, as in
	// /rota-review). When set, the gate refuses a branch lacking the verdicts
	// its resolved review depth needs; nil skips that check.
	Recorded func(branch, head string) []string
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
	// AlreadyMerged says the forge reported the PR merged before this gate ran:
	// the gate landed nothing, but the ledger records the merge as remote.
	AlreadyMerged bool
	// Round is the memo the gate's ledger entries read the round lease through;
	// a caller that records more entries for the same verb reuses it.
	Round *RoundMemo
	Err   string
	Hint  string
	Notes []string // PROVENANCE-SKIP and NO-VERIFY lines for stderr
	// Excluded lists the test-ledger entries that excused a failing command.
	// Expired lists the entries past their expiry, which fail the gate.
	Excluded, Expired []testledger.Entry
}

// OK reports a successful verdict.
func (r GateResult) OK() bool { return r.Verdict == GateFresh || r.Verdict == GatePass }

// GateTarget is what a gate argument resolves to: a slot, or the queued record
// of a PR whose slot moved on. Both carry the branch, PR, base and relay log the
// gate reads; Name and Task belong to a slot, Issue to a queued record.
//
// External is a PR no slot or review record knows (an orchestrator fix-forward
// PR, a PR after wind-down): only PR is set, and the gate reads the branch from
// the forge.
type GateTarget struct {
	Queued, External                    bool
	Name, Branch, PR, Base, Task, Issue string
	relays                              []any
}

func gateSlot(s *Slot) GateTarget {
	// An adopted slot is gated like an unrecorded PR: no relays are expected,
	// and the PR's head branch is read from the forge.
	if s.IsExternal() {
		return GateTarget{External: true, Name: s.Name(), Branch: s.Branch(), PR: s.PR(), Base: s.Base(), Task: s.Task()}
	}
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

// GateTargetAny is GateTarget for `rota worker gate`, which also gates any open
// PR of the repo: a PR argument that no slot or review record owns resolves to an
// External target instead of being refused. A slot name still must exist, and
// the train keeps GateTarget's refusal.
func (r Registry) GateTargetAny(arg string) (GateTarget, error) {
	t, err := r.GateTarget(arg)
	if err == nil || r.Slot(arg) != nil {
		return t, err
	}
	var ee *exitcode.Error
	if _, ok := PRRefNumber(arg); ok && errors.As(err, &ee) && ee.Exit == exitcode.ExitResolution {
		return GateTarget{External: true, PR: arg}, nil
	}
	return t, err
}

// Forge is the part of tracker.Adapter the gate merges through: read the PR,
// then ask the forge to merge it pinned to the verified head. Provider
// differences (argv, JSON shape, auto-merge) live behind it in internal/tracker.
type Forge interface {
	PRView(ctx context.Context, pr int) (tracker.PRInfo, error)
	PRRequestMerge(ctx context.Context, pr int, o tracker.MergeOpts) error
	// PRMergeable asks the forge whether the PR merges cleanly, before the verify.
	PRMergeable(ctx context.Context, pr int) (tracker.Mergeability, error)
	// OpenPRs lists the open PRs, so a slot that records none can be matched
	// to the PR its branch heads.
	OpenPRs(ctx context.Context) ([]tracker.PR, error)
	// ClosedNumbers, Get and RemoveLabels let a landed PR release the claim
	// label on the issues it closed.
	ClosedNumbers(body string) []int
	Get(ctx context.Context, number int, withComments bool) (tracker.Issue, error)
	RemoveLabels(ctx context.Context, number int, labels []string) error
	// CommitChecks reads the CI checks on a pushed commit (test.fullWhere ci).
	CommitChecks(ctx context.Context, sha string) ([]tracker.CheckRun, error)
}

// Gate runs the merge gate for a slot or a queued PR (see GateTarget). A
// passing gate of a queued PR drops its record. The environment defaults are
// applied here, once; everything below takes Env as given.
func (e Env) Gate(ctx context.Context, root string, o GateOpts) (GateResult, error) {
	e = e.withDefaults()
	if o.Round == nil {
		o.Round = &RoundMemo{}
	}
	if o.CheckOnly || o.HoldsLandLock { // read-only, or the caller already holds the land lock
		res, err := e.gate(ctx, root, o)
		res.Round = o.Round
		return res, err
	}
	var res GateResult
	err := e.withLandLock(ctx, root, func() (err error) {
		res, err = e.gate(ctx, root, o)
		return err
	})
	res.Round = o.Round
	return res, err
}

// gate reads the registry and the gate input, then runs the step table.
func (e Env) gate(ctx context.Context, root string, o GateOpts) (GateResult, error) {
	res := GateResult{Slot: o.Slot, Base: o.Base}
	reg, err := LoadRegistry(root)
	if err != nil {
		return res, err
	}
	if !reg.Exists {
		return res, fail(exitcode.ExitResolution, "no worker pool — run rota worker pool init first")
	}
	t, err := reg.GateTargetAny(o.Slot)
	if err != nil {
		return res, err
	}
	in, err := e.GateInput(root)
	if err != nil {
		return res, err
	}
	g := &gate{e: e, ctx: ctx, root: root, res: &res, o: o, in: in, target: t, resolved: t, branch: t.Branch, pr: t.PR}
	err = g.run()
	return res, err
}

// gateInput is what the gate and the train read before they start: the config,
// the commands it names and the test ledger. Env.GateInput supplies it, so one
// loader decides what is read and tests hand over a value instead of a .rota
// tree. The registry is not part of it: it resolves the target, not how the
// target is verified.
type gateInput struct {
	cfg    GateConfig
	ledger testledger.Ledger // .rota/test-ledger.json
}

// loadGateInput is the disk default of Env.GateInput.
//
// Read before the merge: the branch lands in root and may carry its own
// .rota/config.json or test-ledger.json, which must not decide how it is
// verified. The gate and the train both call this ahead of any merge and carry
// the value through, never re-reading root afterwards.
func loadGateInput(root string) (gateInput, error) {
	in := gateInput{cfg: LoadGateConfig(root)}
	in.cfg.FullCommands() // the legacy-key warning, once per run
	var err error
	if _, err = in.cfg.Where(); err != nil {
		return in, err
	}
	in.ledger, err = LoadLedger(root)
	return in, err
}

// gateStep is one stage of the gate. done ends the gate: the verdict is
// already in g.res, or err says why it could not run.
type gateStep func(g *gate) (done bool, err error)

// gateSteps run in order; see the numbered stages in the file comment.
var gateSteps = []gateStep{
	(*gate).stepForge,
	(*gate).stepExternal,
	(*gate).stepAdoptPR,
	(*gate).stepBestOf,
	(*gate).stepRemote,
	(*gate).stepRefs,
	(*gate).stepFreshness,
	(*gate).stepPRMatches,
	(*gate).stepProvenance,
	(*gate).stepCloses,
	(*gate).stepVerdict,
	(*gate).stepReviewDepth,
	(*gate).stepCheckOnly,
	(*gate).stepOnBase,
	(*gate).stepCIVerifier,
	(*gate).stepPremerge,
	(*gate).stepForgeMergeable,
	(*gate).stepVerifyFirst,
	(*gate).stepMerge,
	(*gate).stepVerify,
}

// gatePostSteps run after the steps above, however they ended, unless one
// returned an error: the verdict is final and these record it.
var gatePostSteps = []func(*gate) error{
	(*gate).stepRecordLedger,
	(*gate).stepDequeue,
	(*gate).stepReleaseExternal,
}

// run walks the step table, then the post steps. A step that is done ends the
// table, not the post steps; an error ends both.
func (g *gate) run() error {
	for _, step := range gateSteps {
		done, err := step(g)
		if err != nil {
			return err
		}
		if done {
			break
		}
	}
	for _, step := range gatePostSteps {
		if err := step(g); err != nil {
			return err
		}
	}
	return nil
}

// git runs git on the gate's context.
func (g *gate) git(dir string, args ...string) (string, int) {
	return g.e.runGit(g.ctx, dir, args...)
}

// stepRecordLedger appends the verdict to the gate ledger.
func (g *gate) stepRecordLedger() error {
	if !g.o.CheckOnly {
		gateLedger(g.o.Round, g.root, g.resolved, *g.res)
	}
	return nil
}

// stepDequeue drops the record of a queued PR that passed.
func (g *gate) stepDequeue() error {
	if g.target.Queued && g.res.Verdict == GatePass {
		return RemoveQueuedPR(g.root, g.target.PR)
	}
	return nil
}

// stepReleaseExternal unregisters a merged adopted slot and keeps its checkout.
func (g *gate) stepReleaseExternal() error {
	t := g.target
	if t.External && t.Name != "" && g.res.Verdict == GatePass && !g.o.CheckOnly {
		// The PR is merged: a release that cannot finish is a note, not a failed gate.
		if rerr := g.e.ReleaseExternal(g.root, t.Name, g.o.Prune); rerr != nil {
			g.res.Notes = append(g.res.Notes, "RELEASE-KEPT "+t.Name+" — "+rerr.Error())
		}
	}
	return nil
}

// stepForge picks the provider and opens its forge.
func (g *gate) stepForge() (bool, error) {
	g.res.Branch, g.res.PR = g.target.Branch, g.target.PR
	g.provider = g.e.detectProvider(g.ctx, g.root, g.pr)
	g.cliName = tracker.CLIName(g.provider)
	g.label = g.in.cfg.InProgressLabel
	var err error
	if g.forge, err = g.e.Forge(g.provider, g.root, g.in.cfg.Tracker); err != nil {
		return g.broke(fmt.Sprintf("cannot reach the %s forge: %v", g.provider, err))
	}
	return false, nil
}

// stepExternal reads the head branch of a PR no slot owns from the forge, so
// the later steps judge it like a slot's: freshness, identity, provenance,
// approval, verify and land. Its issue, for the closes check, is the one its
// branch name leads with (HeldID); a branch with none has nothing to check.
func (g *gate) stepExternal() (bool, error) {
	if !g.target.External || g.pr == "" { // an adopted slot may record no PR yet
		return false, nil
	}
	g.prNum = prNumText(g.pr)
	info, ok := g.prInfo()
	if !ok {
		return g.broke(fmt.Sprintf("could not read PR %s from %s", g.pr, g.provider))
	}
	g.branch, g.target.Branch, g.res.Branch = info.Head, info.Head, info.Head
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
	if _, code := g.git(g.root, "remote", "get-url", "origin"); code != 0 {
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
	root, o := g.root, g.o
	if _, code := g.git(root, "rev-parse", "--verify", "--quiet", o.Base); code != 0 {
		return true, fail(exitcode.ExitResolution, fmt.Sprintf("base branch '%s' does not exist", o.Base))
	}
	if !g.remote {
		g.headRef, g.baseRef = g.branch, o.Base
		var code int
		// The commit the gates below judge is the commit that lands.
		if g.verified, code = g.git(root, "rev-parse", "--verify", "--quiet", g.branch); code != 0 || g.verified == "" {
			return true, fail(exitcode.ExitResolution, fmt.Sprintf("worker branch '%s' does not exist", g.branch))
		}
		return false, nil
	}
	if _, code := g.git(root, "fetch", "origin", "-q"); code != 0 {
		return g.broke("git fetch origin failed")
	}
	g.headRef, g.baseRef = "origin/"+g.branch, "origin/"+o.Base
	if _, code := g.git(root, "rev-parse", "--verify", "--quiet", g.headRef+"^{commit}"); code != 0 {
		return g.broke(fmt.Sprintf("%s does not exist — has the worker pushed %s?", g.headRef, g.branch))
	}
	if _, code := g.git(root, "rev-parse", "--verify", "--quiet", g.baseRef+"^{commit}"); code != 0 {
		return g.broke(g.baseRef + " does not exist")
	}
	return false, nil
}

// stepFreshness is stage 1. Three-way on the exit code: `&& FRESH || STALE`
// would call a check that itself errors STALE.
func (g *gate) stepFreshness() (bool, error) {
	o := g.o
	switch _, code := g.git(g.root, "merge-base", "--is-ancestor", g.baseRef, g.headRef); code {
	case 0:
		return false, nil
	case 1:
	default:
		return g.broke(fmt.Sprintf("git merge-base --is-ancestor %s %s exited %d", g.baseRef, g.headRef, code))
	}
	behind, c := g.git(g.root, "rev-list", "--count", g.headRef+".."+g.baseRef)
	if c != 0 {
		behind = "?"
	}
	var why, brokeMsg string
	var shared []string
	if !o.Train { // a train already merged every PR cleanly, in order, in its scratch tree
		why, shared, brokeMsg = g.staleReason()
	}
	if brokeMsg != "" {
		return g.broke(brokeMsg)
	}
	if why != "" {
		g.noteMergedRemotely()
		g.res.SHA, _ = g.git(g.root, "rev-parse", "--short=7", g.headRef) // bounce accounting keys on the head
		g.verdict(GateStale, fmt.Sprintf("STALE %s %s — %s commit(s) landed on %s since it branched; %s", o.Slot, g.branch, behind, o.Base, why),
			fmt.Sprintf("bounce: tell slot %s to `git merge %s`, resolve and re-verify, then re-gate", o.Slot, o.Base))
		return true, nil
	}
	if !o.Train {
		detail := "none touch its files and the merge is clean, merging as is"
		if len(shared) > 0 {
			detail = "both sides changed " + strings.Join(shared, ", ") + " but the merge is clean, merging and verifying the merged tree"
		}
		g.res.Notes = append(g.res.Notes, fmt.Sprintf("STALE-MERGE %s — %s commit(s) landed on %s since %s branched; %s", o.Slot, behind, o.Base, g.branch, detail))
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
	if g.verified, code = g.git(g.root, "rev-parse", g.headRef); code != 0 || g.verified == "" {
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
		g.res.AlreadyMerged = info.State == "MERGED"
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

// stepProvenance runs the approvals check.
func (g *gate) stepProvenance() (bool, error) {
	failMsg, brokeMsg := g.checkProvenance()
	if brokeMsg != "" {
		return g.broke(brokeMsg)
	}
	if failMsg != "" {
		g.res.SHA, _ = g.git(g.root, "rev-parse", "--short=7", g.headRef)
		g.verdict(GateProvenanceFail, failMsg, "")
		return true, nil
	}
	return false, nil
}

// stepBestOf refuses an attempt of a best-of:2 issue until a `rota round pick`
// names its PR, before any fetch, freshness check or bounce. It runs under
// CheckOnly too, so a train member is refused before anything merges. The
// refusal clears itself once the orchestrator picks.
func (g *gate) stepBestOf() (bool, error) {
	issue := g.target.Issue
	if !g.target.Queued {
		issue = HeldID(g.target.Task, g.target.Branch, g.target.Name)
	}
	reg, err := LoadRegistry(g.root)
	if err != nil {
		return false, err
	}
	rec := reg.BestOf(issue)
	if rec == nil || (g.pr != "" && rec.Picked(g.pr)) {
		return false, nil
	}
	what := "this branch"
	if g.pr != "" {
		what = g.pr
	}
	msg := fmt.Sprintf("GATE %s refused — #%s is best-of:2 and no pick names %s; nothing landed", g.o.Slot, rec.Issue, what)
	if rec.Pick != "" {
		msg = fmt.Sprintf("GATE %s refused — #%s is best-of:2 and the pick chose %s; this attempt lost; nothing landed", g.o.Slot, rec.Issue, rec.Pick)
	}
	g.verdict(GateBestOfUnpicked, msg, fmt.Sprintf("compare both attempts and run `rota round pick %s --pr <N> --reason-file <f>`", rec.Issue))
	return true, nil
}

// stepCloses refuses a PR whose body does not close the slot's issue (a
// closing keyword: `Closes #N`, `Fixes #N`, ...), unless that issue is labelled
// PartialSliceLabel. Without it the merge leaves the issue open with its claim
// and in-progress label, which reconcile reports as drift. It runs under
// CheckOnly too, so a train member is refused before anything merges. A slot
// with no PR, or holding no numeric issue (file backend), has nothing to check.
func (g *gate) stepCloses() (bool, error) {
	issue := g.target.Issue
	if !g.target.Queued {
		issue = HeldID(g.target.Task, g.target.Branch, g.target.Name)
	}
	n, err := strconv.Atoi(issue)
	if g.pr == "" || err != nil || n <= 0 {
		return false, nil
	}
	body, err := g.prBody()
	if err != nil {
		if tracker.IsKind(err, tracker.KindUnavailable) && strings.Contains(err.Error(), "is not installed") {
			return false, nil
		}
		return g.broke(fmt.Sprintf("could not read the body of %s to check it closes #%d: %v", g.pr, n, err))
	}
	if slices.Contains(g.forge.ClosedNumbers(body), n) {
		return false, nil
	}
	is, err := g.forge.Get(g.ctx, n, false)
	if err != nil {
		return g.broke(fmt.Sprintf("could not read #%d to check for the %s label: %v", n, PartialSliceLabel, err))
	}
	if slices.Contains(is.Labels, PartialSliceLabel) {
		return false, nil
	}
	g.res.SHA, _ = g.git(g.root, "rev-parse", "--short=7", g.headRef)
	g.verdict(GateNotClosing, fmt.Sprintf("GATE %s refused — the body of %s does not close #%d, so the merge would leave it open and claimed; nothing landed", g.o.Slot, g.pr, n),
		fmt.Sprintf("add a line `Closes #%d` to the PR body, or label #%d %s if this PR lands only part of it", n, n, PartialSliceLabel))
	return true, nil
}

// reviewKinds are the verdict kinds a review depth needs on record: full both
// reviewers, light the Standards reviewer, none nothing.
func reviewKinds(d config.ReviewDepth) []string {
	switch d {
	case config.DepthFull:
		return []string{verdict.ReviewSpec, verdict.ReviewQuality}
	case config.DepthLight:
		return []string{verdict.ReviewQuality}
	}
	return nil
}

// stepReviewDepth applies ship.review: it resolves the depth for this branch
// from the diff size against the base and the labels of the slot's issue, notes
// it, and refuses a branch that lacks the recorded verdicts that depth needs.
// A recorded FAIL was already refused by stepVerdict. An unreadable policy,
// diff or issue is a refusal too: guessing a depth could pass a risk:high
// branch as light.
func (g *gate) stepReviewDepth() (bool, error) {
	policy, err := g.in.cfg.ReviewPolicy()
	if err != nil {
		return g.broke(fmt.Sprintf("%v; run rota config check", err))
	}
	out, code := g.git(g.root, "diff", "--numstat", g.baseRef+"..."+g.headRef)
	if code != 0 {
		return g.broke(fmt.Sprintf("could not diff %s against %s to pick the review depth", g.headRef, g.baseRef))
	}
	changed := ChangedLines(out)
	var labels []string
	issue := g.target.Issue
	if !g.target.Queued {
		issue = HeldID(g.target.Task, g.target.Branch, g.target.Name)
	}
	// the issue is read only when the policy has a label to match it against
	if n, err := strconv.Atoi(issue); err == nil && n > 0 && len(policy.Labels) > 0 {
		is, err := g.forge.Get(g.ctx, n, false)
		if err != nil {
			return g.broke(fmt.Sprintf("could not read #%d for its labels to pick the review depth: %v", n, err))
		}
		labels = is.Labels
	}
	depth, why := policy.Resolve(changed, labels)
	g.res.Notes = append(g.res.Notes, fmt.Sprintf("REVIEW-DEPTH %s — %s (%s)", g.o.Slot, depth, why))
	if g.o.Recorded == nil {
		return false, nil
	}
	// stepPRMatches resolved the head when the slot has a remote; otherwise resolve it here
	head := g.verified
	if head == "" {
		var code int
		if head, code = g.git(g.root, "rev-parse", g.headRef); code != 0 || strings.TrimSpace(head) == "" {
			return g.broke(fmt.Sprintf("git rev-parse %s failed (exit %d); cannot tell whether the review verdicts are at head", g.headRef, code))
		}
	}
	have := g.o.Recorded(g.branch, strings.TrimSpace(head))
	var missing []string
	for _, k := range reviewKinds(depth) {
		if !slices.Contains(have, k) {
			missing = append(missing, k)
		}
	}
	if len(missing) == 0 {
		return false, nil
	}
	g.res.SHA, _ = g.git(g.root, "rev-parse", "--short=7", g.headRef)
	g.verdict(GateReviewMissing, fmt.Sprintf("GATE %s refused — review depth %s (%s) needs a recorded %s verdict; nothing landed", g.o.Slot, depth, why, strings.Join(missing, " and ")),
		fmt.Sprintf("run /rota-review on %s (it records the verdicts), or loosen ship.review", g.branch))
	return true, nil
}

// ChangedLines sums the added and deleted lines of `git diff --numstat` output.
// Binary files, which numstat marks with dashes, count for nothing.
func ChangedLines(numstat string) int {
	total := 0
	for _, l := range strings.Split(numstat, "\n") {
		f := strings.Fields(l)
		if len(f) < 2 {
			continue
		}
		a, errA := strconv.Atoi(f[0])
		d, errD := strconv.Atoi(f[1])
		if errA == nil && errD == nil {
			total += a + d
		}
	}
	return total
}

// stepVerdict refuses a branch with a recorded FAIL verdict (B3), the rule the
// ship paths apply. It runs under CheckOnly too, so a train member is refused
// before anything merges.
func (g *gate) stepVerdict() (bool, error) {
	if g.o.Verdict == nil {
		return false, nil
	}
	if err := g.o.Verdict(g.branch); err != nil {
		g.res.Verdict = GateVerdictBlocked
		return true, err
	}
	return false, nil
}

// stepCheckOnly ends a CheckOnly gate with verdict fresh.
func (g *gate) stepCheckOnly() (bool, error) {
	if g.o.CheckOnly {
		// the checked tip: origin/<branch> when a PR is recorded
		g.res.SHA, _ = g.git(g.root, "rev-parse", "--short=7", g.headRef)
		g.res.Verdict = GateFresh
		return true, nil
	}
	return false, nil
}

// stepOnBase refuses a gate run with another branch than the base checked out.
func (g *gate) stepOnBase() (bool, error) {
	cur, _ := g.git(g.root, "rev-parse", "--abbrev-ref", "HEAD")
	if cur != g.o.Base {
		return true, fail(exitcode.ExitResolution, fmt.Sprintf("gate must run with %s checked out (currently on %s)", g.o.Base, cur))
	}
	return false, nil
}

// stepCIVerifier builds the CI verifier of a test.fullWhere ci gate.
func (g *gate) stepCIVerifier() (bool, error) {
	if g.in.cfg.where != WhereCI || g.o.NoVerify {
		return false, nil
	}
	ci, msg := g.e.newCIVerifier(g.ctx, g.root, g.forge, g.in.cfg)
	if msg != "" {
		return g.broke(msg)
	}
	g.ci = ci
	return false, nil
}

// stepPremerge runs the pre-merge sequence the train shares (premerge.go):
// ledger expiry, no-verify, CI-config and the merge-approval gate (B1).
func (g *gate) stepPremerge() (bool, error) {
	run := premergeRun{
		Subject:      gateSubject(g.o.Slot),
		In:           g.in,
		Now:          g.e.Now(),
		SkipLedger:   g.o.CheckOnly || g.o.NoVerify,
		SkipNoVerify: g.o.NoVerify,
		Files:        g.changedFiles,
	}
	if g.ci != nil {
		run.CIChanged = func() ([]string, error) {
			return g.e.ciDiffFiles(g.ctx, g.root, g.baseRef, g.verified)
		}
	}
	run.Approve = g.o.Approve
	r, approval, err := run.run()
	switch {
	case approval:
		g.res.Verdict = GateApprovalRequired
		return true, err
	case err != nil:
		return g.broke(err.Error())
	case r != nil:
		g.res.Expired = r.Expired
		g.verdict(r.Verdict, r.Err, r.Hint)
		return true, nil
	}
	return false, nil
}

// stepForgeMergeable asks the forge about mergeability before the scratch verify.
func (g *gate) stepForgeMergeable() (bool, error) {
	if g.remote && !g.o.NoVerify {
		return g.forgeRefuses(), nil
	}
	return false, nil
}

// stepVerifyFirst verifies the merge result in a scratch tree, before anything
// lands (stage 2).
func (g *gate) stepVerifyFirst() (bool, error) {
	if g.o.NoVerify {
		return false, nil
	}
	check, what := g.verifyLocal, "the gate"
	if g.ci != nil {
		ci := g.ci
		check, what = func(dir, sha string) (bool, error) { return g.verifyOnCI(ci, sha) }, "CI"
	}
	done, err := g.verifyFirst(what, check)
	return done || err != nil, err
}

// stepMerge is stage 3: the merge itself, through the forge or locally.
func (g *gate) stepMerge() (bool, error) {
	if g.remote {
		if g.mergeRemote() {
			return true, nil
		}
	} else if done := g.mergeLocal(g.root); done {
		return true, nil
	}
	g.res.Changed = true
	if !g.remote {
		g.landed, _ = g.git(g.root, "rev-parse", "HEAD")
	}
	g.res.SHA, _ = g.git(g.root, "rev-parse", "--short=7", "HEAD")
	return false, nil
}

// gateMergeableTries bounds the wait for a forge still computing mergeability.
const gateMergeableTries = 3

// forgeRefuses asks the forge whether the PR merges cleanly before the scratch
// verify, so a conflict only the forge sees (local git follows renames the
// forge does not) costs seconds, not a verify run. done is true when the forge
// reports a conflict: merge-failed, nothing landed. Unknown after a few tries
// and any error proceed: the post-verify forge merge stays the authority.
func (g *gate) forgeRefuses() (done bool) {
	n, err := strconv.Atoi(g.prNum)
	if err != nil {
		return false
	}
	for i := 0; i < gateMergeableTries; i++ {
		m, err := g.forge.PRMergeable(g.ctx, n)
		if err != nil {
			return false
		}
		switch m.State {
		case tracker.MergeConflict:
			why := m.Reason
			if why == "" {
				why = "the merge commit cannot be cleanly created"
			}
			g.verdict(GateMergeFailed, fmt.Sprintf("error: %s merge failed for %s:\n%s reports it is not mergeable (%s); nothing was verified", g.cliName, g.pr, g.provider, why), "")
			return true
		case tracker.MergeUnknown:
			if i < gateMergeableTries-1 {
				g.e.Sleep(time.Second)
			}
		default:
			return false
		}
	}
	return false
}

// verifyFirst is RE-VERIFY moved before the merge (#494; test.fullWhere ci
// did it first): the merge result is built in a scratch tree and check
// verifies it there, so a red result bounces the slot with nothing landed
// where verifying the landed base would leave it to fix forward. The base must
// still be what the scratch tree was built on, and its tree is what must land
// (stepVerify). done is true when it ends the gate with a verdict.
func (g *gate) verifyFirst(what string, check func(dir, sha string) (bool, error)) (bool, error) {
	o := g.o
	baseSHA, code := g.git(g.root, "rev-parse", g.baseRef)
	if code != 0 {
		return g.broke(fmt.Sprintf("git rev-parse %s exited %d", g.baseRef, code))
	}
	// The train's scratch shape: a gate is a train of one. The CI verify keeps
	// its own prefix; the worktree guard ignores both.
	prefix := "rota-train-"
	if g.in.cfg.where == WhereCI {
		prefix = "rota-ci-"
	}
	dir, cleanup, err := g.e.scratchTree(g.ctx, g.root, baseSHA, prefix)
	if err != nil {
		return g.broke(err.Error())
	}
	defer cleanup()
	if g.mergeLocal(dir) {
		return true, nil
	}
	sha, _ := g.git(dir, "rev-parse", "HEAD")
	tree, _ := g.git(dir, "rev-parse", "HEAD^{tree}")
	g.res.SHA = strutil.ShortSHA(sha)
	if done, err := check(dir, sha); done || err != nil {
		return true, err
	}
	if g.remote {
		if _, code := g.git(g.root, "fetch", "origin", "-q"); code != 0 {
			return g.broke("git fetch origin failed after " + what + " verified")
		}
	}
	if cur, _ := g.git(g.root, "rev-parse", g.baseRef); cur != baseSHA {
		g.verdict(GateBaseMoved, fmt.Sprintf("BASE-MOVED %s — %s moved from %s to %s while %s verified; nothing landed", o.Slot, g.baseRef, strutil.ShortSHA(baseSHA), strutil.ShortSHA(cur), what),
			"re-run the gate on the new base")
		return true, nil
	}
	g.verifiedTree = tree
	return false, nil
}

// verifyOnCI pushes the scratch merge sha for CI to check (test.fullWhere ci).
func (g *gate) verifyOnCI(ci *ciVerifier, sha string) (bool, error) {
	o := g.o
	vr, err := ci.verify(sha, o.Slot)
	if err != nil {
		return g.broke(err.Error())
	}
	if v, msg, hint := vr.stopVerdict(o.Slot); v != "" {
		g.verdict(v, msg, hint)
		return true, nil
	}
	g.res.Verified = vr.Verified
	for _, c := range vr.Failed {
		g.res.Notes = append(g.res.Notes, "verify FAILED: "+c)
	}
	if !vr.OK() {
		g.verdict(GateVerifyFailed, fmt.Sprintf("GATE-FAIL %s — the merge result of %s does not pass CI; nothing landed\n%s", o.Slot, g.branch, vr.detail()),
			fmt.Sprintf("send %s back to fix it, then re-gate", o.Slot))
		return true, nil
	}
	g.ciRun = &vr
	return false, nil
}

// verifyLocal runs test.full, then test.e2e, on the scratch merge in dir.
func (g *gate) verifyLocal(dir, _ string) (bool, error) {
	if done, err := g.verifyTier("test.full", g.in.cfg.Full, dir, false); done || err != nil {
		return true, err
	}
	return g.verifyTier("test.e2e", g.in.cfg.E2E, dir, false)
}

// landedTree is the tree of the commit the merge produced.
func (g *gate) landedTree() string {
	tree, _ := g.git(g.root, "rev-parse", g.landed+"^{tree}")
	return tree
}

// confirmCITree checks the tree that landed is the one CI verified, or the
// forge merged something else (the base moved in between). It reads the merge
// commit itself, not the base's tip: a push to the base after the merge is not
// a mismatch. false ends the gate with the verdict set.
func (g *gate) confirmCITree() bool {
	if tree := g.landedTree(); tree != g.verifiedTree {
		g.verdict(GateBaseMoved, fmt.Sprintf("BASE-MOVED %s — it landed, but the landed tree %s differs from the tree CI verified (%s); %s changed during the merge", g.o.Slot, strutil.ShortSHA(tree), strutil.ShortSHA(g.verifiedTree), g.o.Base),
			fmt.Sprintf("do not re-merge; verify %s as it is now", g.o.Base))
		return false
	}
	return true
}

// changedFiles lists the paths the merge changes, for the approval gate.
func (g *gate) changedFiles() ([]string, error) {
	out, code := g.git(g.root, "diff", "--name-only", g.baseRef+"..."+g.verified)
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

// mergeLocal merges the verified head into the base checked out in dir: the
// gate checkout, or a scratch tree. done is true when it ends the gate with a
// verdict.
func (g *gate) mergeLocal(dir string) (done bool) {
	run := func(args ...string) (git.Result, error) { return g.e.Git(g.ctx, dir, args...) }
	err := land.MergeLocal(run, g.verified, fmt.Sprintf("merge: %s into %s", g.branch, g.o.Base), land.RecoveryGit(g.ctx, g.e.Git, dir))
	if err == nil {
		return false
	}
	msg, hint := mergeRefusal(err, mergeWords{
		Failed:   fmt.Sprintf("error: merge of %s into %s failed", g.branch, g.o.Base),
		Conflict: fmt.Sprintf("error: merge of %s into %s conflicted — resolve with the slot that owns the context", g.branch, g.o.Base),
	})
	g.verdict(GateMergeFailed, msg, hint)
	return true
}

// stepVerify is stage 3. The merge result was verified before the merge
// (verifyFirst); what landed must be that tree. A local verify whose landed
// tree differs (the base moved between the last check and the forge merge) is
// run again on the landed base, so the gate never claims a verify it did not
// do. Under test.fullWhere ci a mismatch is base-moved, and test.e2e runs here
// on the landed tree.
func (g *gate) stepVerify() (bool, error) {
	res, o := g.res, g.o
	if o.NoVerify {
		res.Verdict, res.VerifySkipped = GatePass, true
		if !o.Train {
			res.Notes = append(res.Notes, fmt.Sprintf("NO-VERIFY %s — --no-verify: merged tree was NOT gated by a command.", o.Slot))
		}
		return true, nil
	}
	if g.ciRun != nil {
		if !g.confirmCITree() {
			return true, nil
		}
		if done, err := g.verifyTier("test.e2e", g.in.cfg.E2E, g.root, true); done || err != nil {
			return true, err
		}
		res.Verdict = GatePass
		return true, nil
	}
	if tree := g.landedTree(); tree != g.verifiedTree {
		res.Notes = append(res.Notes, fmt.Sprintf("VERIFY-AGAIN %s — the landed tree %s differs from the verified scratch merge %s (%s moved before the merge); verifying the landed %s", o.Slot, strutil.ShortSHA(tree), strutil.ShortSHA(g.verifiedTree), o.Base, o.Base))
		res.Verified, res.Excluded, res.VerifySkipped = nil, nil, false
		for _, tier := range []struct {
			name string
			cmds []string
		}{{"test.full", g.in.cfg.Full}, {"test.e2e", g.in.cfg.E2E}} {
			if done, err := g.verifyTier(tier.name, tier.cmds, g.root, true); done || err != nil {
				return true, err
			}
		}
	}
	res.Verdict = GatePass
	return true, nil
}

// verifyTier runs one tier (test.full or test.e2e) in dir and records it.
// landed says the tree is already on the base: a red result is then fixed
// forward, else nothing landed and the slot goes back. An empty test.full
// leaves a NO-VERIFY note; test.e2e passing after it clears VerifySkipped.
// done is true when it ends the gate with a verdict.
func (g *gate) verifyTier(tier string, cmds []string, dir string, landed bool) (bool, error) {
	res, o := g.res, g.o
	e2e := tier == "test.e2e"
	if e2e && len(cmds) == 0 {
		return false, nil
	}
	vr, err := runVerifyCmds(g.ctx, g.e.Shell, cmds, dir, g.in.ledger, g.e.Now())
	if err != nil {
		return true, err
	}
	if vr.NoCommands {
		res.VerifySkipped = true
		res.Notes = append(res.Notes, fmt.Sprintf("NO-VERIFY %s — test.full is empty; merged tree was NOT gated by a command.", o.Slot),
			"set test.full via rota config set to make this gate real")
		return false, nil
	}
	label := "verify"
	if e2e {
		label = "e2e"
	} else {
		res.Verified = vr.Verified
	}
	res.Excluded = append(res.Excluded, vr.Excluded...)
	for _, c := range vr.Failed {
		res.Notes = append(res.Notes, label+" FAILED: "+c)
	}
	if vr.OK() {
		if e2e {
			res.VerifySkipped = false
		}
		return false, nil
	}
	what := "verification"
	if e2e {
		what = "test.e2e"
	}
	detail := vr.detail()
	if e2e {
		detail = fmt.Sprintf("last lines of the e2e output (full log: %s):\n%s", vr.LogPath, indentTail(vr.Log, 20))
	}
	if landed {
		g.verdict(GateVerifyFailed, fmt.Sprintf("GATE-FAIL %s — merged tree does not pass %s at %s\n%s", o.Slot, what, res.SHA, detail),
			fmt.Sprintf("fix forward on %s; the owning slot has usually moved on", o.Base))
		return true, nil
	}
	g.verdict(GateVerifyFailed, fmt.Sprintf("GATE-FAIL %s — the merge of %s into %s does not pass %s at %s; nothing landed\n%s", o.Slot, g.branch, o.Base, what, res.SHA, detail),
		fmt.Sprintf("send %s back to fix it, then re-gate", o.Slot))
	return true, nil
}

// staleReason says why a branch behind the base must go back to its worker:
// "" when the merge is clean, so the gate merges it itself and verifies the
// merged tree. A conflict needs the worker's context. A shared file changed on
// both sides can merge textually clean and still break (one side widens a
// symbol, the other adds a call), but RE-VERIFY runs on the merged tree and
// catches that, so shared lists those files for the verdict note instead of
// refusing. Files matching round.sharedPaths are ignored, as the readiness
// overlap check ignores them. brokeMsg is set when a git check itself fails.
func (g *gate) staleReason() (why string, shared []string, brokeMsg string) {
	root := g.root
	if _, code := g.git(root, "merge-base", "--is-ancestor", g.headRef, g.baseRef); code == 0 {
		return fmt.Sprintf("its work is already on %s, nothing to merge", g.o.Base), nil, ""
	}
	switch out, code := g.git(root, "merge-tree", "--write-tree", "--no-messages", g.baseRef, g.headRef); code {
	case 0:
	case 1:
		return "the merge conflicts", nil, ""
	default:
		return "", nil, fmt.Sprintf("git merge-tree %s %s exited %d: %s", g.baseRef, g.headRef, code, out)
	}
	mb, code := g.git(root, "merge-base", g.baseRef, g.headRef)
	if code != 0 || mb == "" {
		return "", nil, fmt.Sprintf("git merge-base %s %s exited %d", g.baseRef, g.headRef, code)
	}
	changed := func(ref string) ([]string, bool) {
		out, code := g.git(root, "diff", "--name-only", "--no-renames", mb, ref)
		if code != 0 {
			return nil, false
		}
		return strings.Split(out, "\n"), true
	}
	onBase, ok1 := changed(g.baseRef)
	onHead, ok2 := changed(g.headRef)
	if !ok1 || !ok2 {
		return "", nil, fmt.Sprintf("git diff --name-only against %s failed", mb)
	}
	return "", overlap.Both(onBase, onHead, g.in.cfg.SharedPaths), ""
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
func (e Env) detectProvider(ctx context.Context, root, pr string) string {
	switch {
	case strings.Contains(pr, "/-/merge_requests/"):
		return "gitlab"
	case strings.Contains(pr, "/pull/"):
		return "github"
	}
	url, _ := e.runGit(ctx, root, "remote", "get-url", "origin")
	for _, u := range []string{pr, url} {
		if p := tracker.ProviderFromURL(u); p != tracker.ProviderUnknown {
			return p
		}
	}
	return "github"
}

type gate struct {
	e      Env // defaulted once, by Env.Gate
	ctx    context.Context
	root   string
	res    *GateResult
	o      GateOpts
	in     gateInput
	target GateTarget
	// resolved is the target as GateTarget returned it. stepExternal rewrites
	// target.Branch to the forge head; the ledger keeps the resolved one.
	resolved GateTarget
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
	// ci is the CI verifier of a test.fullWhere ci gate, built by stepCIConfig.
	ci *ciVerifier
	// ciRun is the CI run of the merge result when test.fullWhere is ci.
	ciRun *VerifyResult
	// verifiedTree is the tree of the scratch merge verifyFirst verified: what
	// must land.
	verifiedTree string
	// landed is the commit the merge produced: the forge's merge commit, or
	// the local merge's HEAD.
	landed string
}

// verdict records a non-success verdict.
func (g *gate) verdict(v, msg, hint string) {
	g.res.Verdict, g.res.Err, g.res.Hint = v, msg, hint
}

func (g *gate) broke(msg string) (bool, error) {
	g.verdict(GateCheckBroke, fmt.Sprintf("CHECK-BROKE %s — %s", g.o.Slot, msg), "")
	return true, nil
}

// noteMergedRemotely marks the result when the forge says the PR is merged: a
// stale refusal of a PR landed elsewhere, by a squash or rebase too, whose head
// is then not on the base.
func (g *gate) noteMergedRemotely() {
	if !g.remote || g.o.CheckOnly { // a check-only gate writes no ledger entry
		return
	}
	if info, ok := g.prInfo(); ok && info.State == "MERGED" {
		g.res.AlreadyMerged = true
	}
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
	if _, code := g.git(g.root, "remote", "get-url", "origin"); code != 0 {
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
	g.landed = sha
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
	if v := g.e.Getenv("ROTA_GATE_SHA_WAIT"); v != "" {
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
			g.e.Sleep(wait)
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
	o := g.o
	if _, code := g.git(g.root, "fetch", "origin", "-q"); code != 0 {
		g.broke("git fetch origin failed after merging")
		return true
	}
	switch _, code := g.git(g.root, "merge-base", "--is-ancestor", sha, g.baseRef); code {
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
		g.res.SHA = strutil.ShortSHA(sha) // the merge that landed on origin, for the merged-remotely verdicts
	}
	if out, code := g.git(g.root, "merge", "--ff-only", g.baseRef); code != 0 {
		g.mergedRemotely(fmt.Sprintf("local %s could not fast-forward (%s)", o.Base, g.ffFailureCause(out)))
		return true
	}
	if _, code := g.git(g.root, "merge-base", "--is-ancestor", sha, "HEAD"); code != 0 {
		g.mergedRemotely(fmt.Sprintf("%s is not in the local %s", sha, o.Base))
		return true
	}
	return false
}

// mergedRemotely ends the gate on a PR that is on origin but not on the local
// base. The merged tree was never verified, and the result says so.
func (g *gate) mergedRemotely(why string) {
	g.res.VerifySkipped = true
	g.verdict(GateMergedRemotely, fmt.Sprintf("MERGED-REMOTELY %s — PR %s is on %s but %s; do not re-merge, reconcile %s by hand", g.o.Slot, g.prNum, g.baseRef, why, g.o.Base),
		fmt.Sprintf("the merged tree was NOT verified: fix the local %s, fast-forward it to %s, then run the full gate on %s", g.o.Base, g.baseRef, g.o.Base))
}

// ffFailureCause names why `merge --ff-only` failed. Uncommitted changes to
// files the merge touches are told apart from real divergence: local main can
// be purely behind and still refuse to move.
func (g *gate) ffFailureCause(mergeOut string) string {
	incoming, _ := g.git(g.root, "diff", "--name-only", "HEAD", g.baseRef)
	in := map[string]bool{}
	for _, p := range strings.Split(incoming, "\n") {
		if p != "" {
			in[p] = true
		}
	}
	status, _ := g.git(g.root, "status", "--porcelain")
	var clash []string
	for _, l := range strings.Split(status, "\n") {
		if len(l) > 3 && in[l[3:]] {
			clash = append(clash, l[3:])
		}
	}
	if len(clash) > 0 {
		return "local changes would be overwritten: " + strings.Join(clash, ", ")
	}
	if counts, code := g.git(g.root, "rev-list", "--left-right", "--count", "HEAD..."+g.baseRef); code == 0 {
		if f := strings.Fields(counts); len(f) == 2 && f[0] != "0" {
			return fmt.Sprintf("diverged: %s ahead, %s behind %s", f[0], f[1], g.baseRef)
		}
	}
	if m := tailLines(mergeOut, 3); m != "" {
		return "merge refused: " + m
	}
	return "merge refused, cause not found: no overlapping local changes, not ahead of " + g.baseRef
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
