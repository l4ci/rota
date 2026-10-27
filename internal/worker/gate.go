package worker

import (
	"context"
	"errors"
	"fmt"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/flakeledger"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/labels"
	"github.com/l4ci/rota/internal/land"
	"github.com/l4ci/rota/internal/ledger"
	"github.com/l4ci/rota/internal/overlap"
	"github.com/l4ci/rota/internal/rotastate"
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
//     changed in the note; a conflict sends it back uncounted (see staleReason). With a recorded PR and an origin remote this is judged
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
	// GateVacuousTests refuses tests independent of the non-test change.
	GateVacuousTests   = "vacuous-tests"
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
	// CLI exits 4 with it unless --no-verify was passed. The verdict is also
	// the refusal's blockedBy key.
	GateNoVerify = "no-verify"
	// GateNotClosing: the PR body has no closing keyword for the slot's issue,
	// so the merge would leave it open and drifted; the CLI exits 4 with it,
	// blockedBy closes. An issue labelled PartialSliceLabel is exempt, but a PR
	// the forge says closes one is refused with the same verdict (FinalSlice).
	GateNotClosing = "closes"
	// GateClaimLost fences a holder whose issue was reclaimed elsewhere.
	GateClaimLost = "claim-lost"
	// GateBestOfUnpicked: the PR is an attempt of a best-of:2 issue and no
	// `rota round pick` names it; the CLI exits 4 with it, blockedBy best-of-unpicked.
	GateBestOfUnpicked = "best-of-unpicked"
	// GateReviewMissing: the review depth ship.review resolves for the branch
	// needs a recorded verdict the branch lacks; the CLI exits 4 with it,
	// blockedBy review-missing.
	GateReviewMissing = "review-missing"
	// GateStaleAttempt: a commit on the PR branch is stamped with an attempt
	// the slot no longer holds, so a worker whose assignment was reclaimed or
	// transferred pushed it; the CLI exits 4 with it, blockedBy stale-attempt.
	GateStaleAttempt = "stale-attempt"
	// GateOutcomeOpen: the slot's worker reported a partial or failed outcome
	// and no relay or transfer has answered it; the CLI exits 4 with it,
	// blockedBy outcome-open.
	GateOutcomeOpen = "outcome-open"
	// GateRunning: another gate for this PR is running or queued, or a NoWait
	// run yielded to other landing work. The CLI exits 4, blockedBy gate-running,
	// before this run takes the land lock.
	GateRunning = "gate-running"
	// GateForge: the origin names no forge and forge.provider names none, so
	// the gate cannot tell where the PR lives (tracker.ResolveProvider, an
	// actor); the CLI exits 4 with it, blockedBy forge.
	GateForge = "forge"
	// GateAlreadyMerged: the PR merged while this gate waited for the land lock.
	// A success that changes nothing.
	GateAlreadyMerged = "already-merged"
)

// AttemptTrailer is the commit trailer a worker stamps its attempt id with.
const AttemptTrailer = "Rota-Attempt"

// PartialSliceLabel marks an issue whose PR lands only a slice of it, so the PR
// body must not close it.
const PartialSliceLabel = labels.PartialSlice

// noVerifyRefusal is the message and hint of the refusal for an empty
// test.full under a local verify, shared by the gate and the train.
func noVerifyRefusal(what string) (msg, hint string) {
	return fmt.Sprintf("%s refused — test.full is empty (read from config key %s), so nothing would verify the merged tree; nothing landed", what, config.TestFullKey),
		"set it with `rota config set test.full <command>`, or pass --no-verify to merge unverified on purpose"
}

// GateOpts are the flags of `rota worker gate`.
type GateOpts struct {
	// NoWait is the autopilot's landing mode: defer if any gate or train is
	// running or queued, without entering the gate or waiting on the land lock.
	NoWait    bool
	Slot      string
	Base      string
	CheckOnly bool
	NoVerify  bool
	// FinalSlice marks the PR as the last slice of a partial-slice issue, the
	// one PR allowed to close it (--final-slice). Without it a PR the forge
	// says closes a partial-slice issue is refused (stepCloses).
	FinalSlice bool
	// Prune makes a pass that releases an adopted slot also remove its
	// worktree and branch.
	Prune bool
	// Round reads the round lease for the gate's ledger entries; the gate makes
	// one when nil. A caller that writes more entries after the gate shares it.
	Round *RoundMemo
	// LandVerified marks a landing step of a merge train (see Train): the
	// train merged the PRs together and verified that tree once, so this gate
	// only lands. It does not verify (no NO-VERIFY note either: the train
	// reports its own) and does not refuse a branch behind the base only
	// because an earlier train member landed.
	LandVerified bool
	// HoldsLandLock says the caller already holds the repository's land lock
	// (the train does, through its landing steps), so the gate does not retake
	// it. Without it a gate takes the lock itself unless it is CheckOnly.
	HoldsLandLock bool
	// waited is set by Gate when the land lock was busy: the PR may have merged
	// meanwhile, so the gate reads it again (stepWaitedMerged).
	waited bool
	// Verdict is the review-verdict gate (B3), run after provenance, also under
	// CheckOnly. A non-nil error (a recorded FAIL) stops the gate with verdict
	// verdict-blocked and is returned as is. branch is the worker branch.
	Verdict func(branch, head string) error
	// CheckReviews makes the gate refuse a branch lacking the review verdicts
	// its resolved review depth needs, read with verdict.Recorded at the
	// checked tip (the full sha). Unset skips that check.
	CheckReviews bool
	// ReviewsOnly asks only whether the head has the review verdicts its depth
	// needs: a read-only run (CheckOnly with CheckReviews) that skips the stages
	// judging the branch itself (freshness, provenance, closes, claim, the
	// recorded verdict). It ends review-missing, fresh, or the verdict of a
	// stage before them (the forge, the slot, the PR). The autopilot asks it
	// before it gates, since the gate's freshness check runs first and reads a
	// done awaiting review as stale.
	ReviewsOnly bool
	// Approval is the merge-approval gate (B1), run after provenance and right
	// before the merge, never under CheckOnly. Its thread comes from the
	// resolved target. A refusal or an error stops the gate with verdict
	// approval-required; the refusal lands in GateResult.Refusal
	// and its error, or an adapter's own, is returned as is. Nil skips approval.
	Approval *MergeApproval
	// ItemBody reads the body of an item (#899), whose `## Try it` steps the
	// gate runs on the scratch merge after test.full and test.e2e. found is
	// false for an item the backend does not know. nil leaves the behaviour
	// run out: callers that did not wire it.
	ItemBody func(issue string) (body string, found bool, err error)
	// BehaviourDir turns a CheckOnly gate into the train's behaviour step
	// (#915): once the checks pass, the item's Try it steps run in this
	// directory, the train's scratch tree with the member merged. A failing step
	// ends the gate with verify-failed, as at the gate's own merge.
	BehaviourDir string
}

// skipsVerify says the gate runs no verify tier: --no-verify, or a train's
// landing step whose tree the train already verified.
func (o GateOpts) skipsVerify() bool { return o.NoVerify || o.LandVerified }

// GateResult is the gate's answer. Err is the message for a non-success
// verdict, Hint what to do about it.
type GateResult struct {
	Slot string
	// TargetSlot is the pool slot the gate target resolved to, "" when it
	// resolved to none (an external PR, a queued record whose slot moved on).
	// Slot echoes the argument, which may be a PR number.
	TargetSlot string
	Verdict    string
	Base       string
	// Issue is the item the gate acted on, resolved once from the target: the
	// queued record's, else the one the slot held. "" when it cannot be told.
	Issue         string
	Branch        string
	SHA           string
	PR            string
	Verified      []string
	Flaky         []flakeledger.Entry
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
	// Refusal is the approval gate's refusal, set with verdict approval-required
	// when it refused rather than failed to run.
	Refusal *exitcode.Refusal
	Notes   []string // PROVENANCE-SKIP and NO-VERIFY lines for stderr
	// Warnings are problems the gate survived: the merge landed, but something
	// after it needs a caller's eye (a Jira issue that would not close).
	Warnings []string
	// Excluded lists the test-ledger entries that excused a failing command.
	// Expired lists the entries past their expiry, which fail the gate.
	Excluded, Expired []testledger.Entry
}

// OK reports a successful verdict.
func (r GateResult) OK() bool { return r.Verdict == GateFresh || r.Verdict == GatePass }

// GateTarget is what a gate argument resolves to: a slot, or the queued record
// of a PR whose slot moved on. Both carry the branch, PR, base and relay log the
// gate reads; Name and Task belong to a slot. Issue is the item the target
// stands for: the queued record's, else the one the slot holds ("" when unknown).
//
// External is a PR no slot or review record knows (an orchestrator fix-forward
// PR, a PR after wind-down): only PR is set, and the gate reads the branch from
// the forge.
type GateTarget struct {
	Queued, External                    bool
	Name, Branch, PR, Base, Task, Issue string
	// ClaimID is the claim recorded when the slot or queued PR was assigned.
	ClaimID string
	// Bundle is the member issues the PR closes beside Issue (the lead), from
	// the slot or the queued record. An external or adopted PR has none.
	Bundle []string
	relays []any
	// Attempt and AttemptBase are the slot's current attempt and the branch
	// tip it inherited. A queued record or an external PR has neither, so the
	// attempt check skips them.
	Attempt, AttemptBase string
	// Outcome is the partial or failed outcome the worker reported and nobody
	// has answered, "" otherwise.
	Outcome string
	// Kind is the harness the slot ran, the PR's author for the cross-harness
	// review; "" for a queued record or an external PR.
	Kind string
}

// openOutcome is the slot's reported outcome when it is partial or failed.
func openOutcome(s *Slot) string {
	if k, _ := s.Outcome(); OutcomeOpen(k) {
		return k
	}
	return ""
}

func gateSlot(s *Slot) GateTarget {
	// An adopted slot is gated like an unrecorded PR: no relays are expected,
	// and the PR's head branch is read from the forge.
	if s.IsExternal() {
		return GateTarget{External: true, Name: s.Name(), Branch: s.Branch(), PR: s.PR(), Base: s.Base(), Task: s.Task(), ClaimID: s.ClaimID(), Issue: HeldID(s.Task(), s.Branch(), s.Name())}
	}
	return GateTarget{Name: s.Name(), Branch: s.Branch(), PR: s.PR(), Base: s.Base(), Task: s.Task(), ClaimID: s.ClaimID(), Issue: HeldID(s.Task(), s.Branch(), s.Name()), Bundle: s.Bundle(), relays: s.Relays(), Attempt: s.Attempt(), AttemptBase: s.AttemptBase(), Outcome: openOutcome(s), Kind: s.HarnessKind()}
}

// Relays is the relay log the gate checks the PR body's approvals against.
func (t GateTarget) Relays() []any { return t.relays }

func gateQueued(q QueuedPR) GateTarget {
	return GateTarget{Queued: true, ClaimID: q.ClaimID, Issue: q.Issue, Bundle: q.Bundle, Branch: q.Branch, PR: q.PR, Base: q.Base, relays: q.Relays}
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

// GateTargetAny resolves targets for the gate and train, which also gate any
// open PR of the repo: a PR argument that no slot or review record owns resolves
// to an External target instead of being refused. A slot name still must exist.
func (r Registry) GateTargetAny(arg string) (GateTarget, error) {
	t, err := r.GateTarget(arg)
	if err == nil || r.Slot(arg) != nil {
		return t, err
	}
	var ee *exitcode.Error
	if _, ok := PRRefNumber(arg); ok && errors.As(err, &ee) && ee.Exit == exitcode.ExitResolution {
		// A returned slot's PR is external to the pool but keeps its relay log.
		return GateTarget{External: true, PR: arg, relays: r.LoggedRelays(arg)}, nil
	}
	return t, err
}

// Forge is the part of tracker.Forge the gate merges through: read the PR,
// then ask the forge to merge it pinned to the verified head. Provider
// differences (argv, JSON shape, auto-merge) live behind it in internal/tracker.
// A local forge (forge.provider local) has none: the gate merges directly and
// never looks for a PR.
type Forge interface {
	PRView(ctx context.Context, pr int) (tracker.PRInfo, error)
	PRRequestMerge(ctx context.Context, pr int, o tracker.MergeOpts) error
	// PRMergeable asks the forge whether the PR merges cleanly, before the verify.
	PRMergeable(ctx context.Context, pr int) (tracker.Mergeability, error)
	// OpenPRs lists the open PRs, so a slot that records none can be matched
	// to the PR its branch heads.
	OpenPRs(ctx context.Context) ([]tracker.PR, error)
	// ClosedNumbers names the issues a PR body closes; what to do with them is
	// the issue source's job (Issues).
	ClosedNumbers(body string) []int
	// CommitChecks reads the CI checks on a pushed commit (test.fullWhere ci).
	CommitChecks(ctx context.Context, sha string) ([]tracker.CheckRun, error)
	// AddMRNote posts a comment on the PR (the behaviour run's transcript).
	AddMRNote(ctx context.Context, number int, body string) (string, error)
}

// closingReporter is the optional Forge capability of reading what the forge
// itself says merging a PR closes (GitHub closingIssuesReferences), which sees
// a closing keyword anywhere in the body. known is false for a forge that
// cannot report it.
type closingReporter interface {
	ClosingRefs(ctx context.Context, pr int) (nums []int, known bool, err error)
}

// Issues is the part of tracker.IssueTracker the gate reads: the issue a PR
// must close (or mark partial), its labels for the review depth, and the claim
// label a landed PR releases. It is the issue source (issues.source), not the
// forge; a file source has none.
type Issues interface {
	Get(ctx context.Context, key tracker.IssueKey, withComments bool) (tracker.Issue, error)
	RemoveLabels(ctx context.Context, key tracker.IssueKey, labels []string) error
	// ClosesIssue is the tracker's own closing convention: a keyword in the PR
	// body (GitHub, GitLab) or the issue key in the branch or title.
	ClosesIssue(key tracker.IssueKey, c tracker.ClosingSource) bool
}

// Gate runs the merge gate for a slot or a queued PR (see GateTarget). A
// passing gate of a queued PR drops its record. The environment defaults are
// applied here, once; everything below takes Env as given.
func (e Env) Gate(ctx context.Context, root string, o GateOpts) (GateResult, error) {
	e = e.WithDefaults()
	if o.Round == nil {
		o.Round = &RoundMemo{}
	}
	if o.ReviewsOnly {
		o.CheckOnly, o.CheckReviews = true, true
	}
	if o.CheckOnly || o.HoldsLandLock { // read-only, or the caller already holds the land lock
		res, err := e.gate(ctx, root, o)
		res.Round = o.Round
		return res, err
	}
	var res GateResult
	if common, key, ok := e.gateRunKey(ctx, root, o.Slot); ok {
		other, release, err := claimGateRun(common, key)
		if err != nil {
			return res, err
		}
		if other != 0 {
			res = GateResult{Slot: o.Slot, Base: o.Base, Round: o.Round, Verdict: GateRunning,
				Err:  fmt.Sprintf("GATE %s refused — another gate for this PR is running or queued (pid %d); nothing landed", o.Slot, other),
				Hint: fmt.Sprintf("wait for pid %d to finish; if it is gone, remove the stale marker under the repo's rota state dir", other)}
			return res, nil
		}
		defer release()
	}
	err := e.withLandingRun(ctx, root, o.Slot, o.NoWait, func(waited bool) (err error) {
		o.waited = waited
		res, err = e.gate(ctx, root, o)
		return err
	})
	if errors.Is(err, errLandingBusy) {
		res = GateResult{Slot: o.Slot, Base: o.Base, Verdict: GateRunning, Err: err.Error()}
		err = nil
	}
	res.Round = o.Round
	return res, err
}

// gateRunKey resolves the PR a gate is about to work on without taking any
// lock. ok is false when the target cannot be read; gate then reports why.
func (e Env) gateRunKey(ctx context.Context, root, arg string) (common, key string, ok bool) {
	reg, err := LoadRegistry(root)
	if err != nil || !reg.Exists {
		return "", "", false
	}
	t, err := reg.GateTargetAny(arg)
	if err != nil {
		return "", "", false
	}
	if common, err = rotastate.CommonDirVia(ctx, e.Git, root); err != nil {
		return "", "", false
	}
	return common, gateRunKey(t, arg), true
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
	res.Issue, res.TargetSlot = t.Issue, t.Name
	in, err := e.GateInput(root)
	if err != nil {
		return res, err
	}
	in.cfg.WarnLegacy(e.Stderr)
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

// loadGateInput is the default of Env.GateInput: Env.Config and the ledger on disk.
//
// Read before the merge: the branch lands in root and may carry its own
// .rota/config.json or test-ledger.json, which must not decide how it is
// verified. The gate and the train both call this ahead of any merge and carry
// the value through, never re-reading root afterwards.
func (e Env) loadGateInput(root string) (gateInput, error) {
	in := gateInput{cfg: e.GateConfig(root)}
	var err error
	if _, err = in.cfg.Where(); err != nil {
		return in, err
	}
	if err = in.cfg.TestsErr(); err != nil {
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
	(*gate).stepFreeze,
	(*gate).stepBestOf,
	(*gate).stepRemote,
	(*gate).stepWaitedMerged,
	(*gate).stepSlotHead,
	(*gate).stepRefs,
	(*gate).stepAttempt,
	(*gate).stepOutcome,
	(*gate).stepFreshness,
	(*gate).stepPRMatches,
	(*gate).stepVerdictReports,
	(*gate).stepProvenance,
	(*gate).stepCloses,
	(*gate).stepClaim,
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

// reviewsOnlySteps is the table of a ReviewsOnly run: it resolves the target
// and its head as the full table does, then stops at the review verdicts.
var reviewsOnlySteps = []gateStep{
	(*gate).stepForge,
	(*gate).stepExternal,
	(*gate).stepAdoptPR,
	(*gate).stepFreeze,
	(*gate).stepRemote,
	(*gate).stepWaitedMerged,
	(*gate).stepSlotHead,
	(*gate).stepRefs,
	(*gate).stepPRMatches,
	(*gate).stepVerdictReports,
	(*gate).stepReviewDepth,
	(*gate).stepCheckOnly,
}

// gatePostSteps run after the steps above, however they ended, unless one
// returned an error: the verdict is final and these record it.
var gatePostSteps = []func(*gate) error{
	(*gate).stepClearFreeze,
	(*gate).stepRecordLedger,
	(*gate).stepDequeue,
	(*gate).stepReleaseExternal,
}

// run walks the step table, then the post steps. A step that is done ends the
// table, not the post steps; an error ends both.
func (g *gate) run() error {
	steps := gateSteps
	if g.o.ReviewsOnly {
		steps = reviewsOnlySteps
	}
	for _, step := range steps {
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
		gateLedger(g.e, g.o.Round, g.root, g.resolved, *g.res)
	}
	return nil
}

// stepDequeue drops the review record of a PR that merged: a pass, or a verdict
// (merged-remotely, a red verify of the landed tree) that still left the PR on
// the base. The target may have resolved to a slot, so the record is matched by
// PR, not by how the argument resolved.
func (g *gate) stepDequeue() error {
	if g.o.CheckOnly || !(g.res.Verdict == GatePass || g.res.Changed) {
		return nil
	}
	return RemoveQueuedPR(g.root, g.target.PR)
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
	if err := g.in.cfg.sourceErr; err != nil {
		return true, fail(exitcode.ExitInternal, err.Error())
	}
	g.res.Branch, g.res.PR = g.target.Branch, g.target.PR
	var err error
	if g.provider, err = g.e.detectProvider(g.ctx, g.root, g.pr, g.in.cfg.Tracker.Provider, tracker.ForgeActor); err != nil {
		g.verdict(GateForge, fmt.Sprintf("GATE %s refused — %v; nothing landed", g.o.Slot, err),
			"set forge.provider to the forge that holds the PR, or to local to merge directly")
		return true, nil
	}
	g.cliName = tracker.CLIName(g.provider)
	g.label = g.in.cfg.InProgressLabel
	if g.provider == tracker.ProviderLocal {
		// No forge: no PR to look for, read or merge. The gate merges directly.
		g.local = true
		if g.pr != "" {
			return g.broke(fmt.Sprintf("slot %s records PR %s, but the forge is local (forge.provider is local, or auto with no origin remote); set forge.provider to the forge that holds the PR", g.o.Slot, g.pr))
		}
		return false, nil
	}
	if g.forge, err = g.e.Forge(g.provider, g.root, g.in.cfg.Tracker); err != nil {
		return g.broke(fmt.Sprintf("cannot reach the %s forge: %v", g.provider, err))
	}
	if g.issues, err = g.e.issueSide(g.forge, g.provider, g.root, g.in.cfg.Tracker); err != nil {
		return g.broke(fmt.Sprintf("cannot reach the issue source: %v", err))
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
	if g.local {
		return g.broke(fmt.Sprintf("cannot gate PR %s: the forge is local (forge.provider is local, or auto with no origin remote)", g.pr))
	}
	g.prNum = prNumText(g.pr)
	info, ok := g.prInfo()
	if !ok {
		return g.broke(fmt.Sprintf("could not read PR %s from %s", g.pr, g.provider))
	}
	g.branch, g.target.Branch, g.res.Branch = info.Head, info.Head, info.Head
	// the head branch names the issue of a PR no slot or record owns
	g.target.Issue = HeldID(g.target.Task, info.Head, g.target.Name)
	g.target.Bundle = nil // a bundle belongs to a slot's lead, not to a branch-derived issue
	g.res.Issue = g.target.Issue
	return false, nil
}

// stepAdoptPR looks a PR up by head when the slot recorded none. A worker can
// open its PR without the slot recording it. A local merge would then land on
// the local base only, leave the PR open and still report a pass, so look the
// PR up by head before taking that path.
func (g *gate) stepAdoptPR() (bool, error) {
	if g.local || g.pr != "" || g.target.Queued || g.branch == "" {
		return false, nil
	}
	adopted, msg := g.openPRForBranch()
	if msg != "" {
		return g.broke(msg)
	}
	if adopted == "" {
		return false, nil
	}
	g.pr, g.target.PR, g.res.PR = adopted, adopted, adopted
	g.res.Notes = append(g.res.Notes, fmt.Sprintf("PR-ADOPTED %s — the slot recorded no PR; open PR %s is headed by %s", g.o.Slot, adopted, g.branch))
	if !g.o.CheckOnly {
		UpdateSlot(g.root, g.o.Slot, func(s *Slot) { s.SetPR(adopted) })
	}
	return false, nil
}

// stepSlotHead follows a slot's PR to its head branch when the two disagree.
// An issue split into stacked PRs ends with a second PR from a new branch, and
// the slot still records the first (merged) one. The head is taken only when
// it leads with the issue the slot holds, so a PR of another issue's branch
// stays a mismatch. A real gate records it on the slot, so the next one starts
// from the right branch.
func (g *gate) stepSlotHead() (bool, error) {
	t := g.target
	if t.External || t.Queued || !g.remote || g.pr == "" || g.branch == "" {
		return false, nil
	}
	info, ok := g.prInfo()
	if !ok {
		return false, nil
	}
	g.viewed = &info // stepPRMatches reads the same PR next: one forge call
	if info.State != "OPEN" || info.Head == "" || info.Head == g.branch {
		return false, nil
	}
	if held := HeldID(t.Task, g.branch, t.Name); held == "" || HeldID("", info.Head, t.Name) != held {
		return false, nil
	}
	g.res.Notes = append(g.res.Notes, fmt.Sprintf("BRANCH-FOLLOWED %s — PR %s is headed by %s, not the recorded %s", t.Name, g.pr, info.Head, g.branch))
	g.branch, g.target.Branch, g.res.Branch = info.Head, info.Head, info.Head
	if !g.o.CheckOnly && t.Name != "" { // o.Slot is a PR ref when gated by number
		UpdateSlot(g.root, t.Name, func(s *Slot) { s.SetBranch(info.Head) })
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
	// Only exit 2 ("No such remote") means origin is absent. A git that cannot
	// run (its directory gone, a signal, a timeout) or fails otherwise says
	// nothing about the remote, so it is reported as what it is.
	res, err := g.e.Git(g.ctx, g.root, "remote", "get-url", "origin")
	switch {
	case err != nil:
		return g.broke(fmt.Sprintf("could not run git in %s to read the origin remote: %v", g.root, err))
	case res.ExitCode == 2:
		return g.broke(fmt.Sprintf("slot %s has PR %s but this repo has no 'origin' remote; refusing a local merge that would bypass the PR", g.o.Slot, g.pr))
	case res.ExitCode != 0:
		return g.broke(fmt.Sprintf("git remote get-url origin failed in %s (exit %d): %s", g.root, res.ExitCode, strings.TrimSpace(res.Stderr)))
	}
	if g.prNum == "" {
		return g.broke(fmt.Sprintf("cannot read a PR number from '%s'", g.pr))
	}
	g.remote = true
	return false, nil
}

// stepWaitedMerged re-reads the PR when the gate queued behind another gate or
// a train: it may have merged in the meantime, and judging it again would fail
// on a head that is already landed.
func (g *gate) stepWaitedMerged() (bool, error) {
	if !g.o.waited || g.o.CheckOnly || !g.remote {
		return false, nil
	}
	if info, ok := g.prInfo(); ok && info.State == "MERGED" {
		g.res.AlreadyMerged = true
		g.verdict(GateAlreadyMerged, fmt.Sprintf("GATE %s — PR %s merged while this gate waited for the land lock; nothing changed", g.o.Slot, g.pr), "")
		return true, nil
	}
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

// stepAttempt refuses a PR carrying a commit stamped (AttemptTrailer) with an
// attempt the slot no longer holds: a worker whose assignment was reclaimed or
// transferred pushed it, with or without --attempt on its verbs. It judges the
// commits after the branch tip the current attempt inherited, which keep their
// earlier trailers, so a transfer never turns the new holder's PR away. It
// reads the slot, not the queued record, and runs under CheckOnly too.
//
// Anything it cannot tell passes: a slot with no attempt (assigned before the
// fence), an unstamped commit, an inherited tip that is not an ancestor of the
// head (a rebase rewrote it) or a log that fails.
func (g *gate) stepAttempt() (bool, error) {
	t := g.target
	if t.Queued || t.External || t.Attempt == "" {
		return false, nil
	}
	// The base's own commits (a merge of main into the branch brings other
	// workers' stamped commits) are nobody's attempt here: exclude them too.
	rng := []string{g.headRef, "^" + g.baseRef}
	if t.AttemptBase != "" {
		if _, code := g.git(g.root, "merge-base", "--is-ancestor", t.AttemptBase, g.headRef); code != 0 {
			// A rebase rewrote the inherited tip: the inherited trailers cannot be
			// told from the stale ones, so judge nothing.
			return false, nil
		}
		rng = append(rng, "^"+t.AttemptBase)
	}
	out, code := g.git(g.root, append([]string{"log", "--format=%h %(trailers:key=" + AttemptTrailer + ",valueonly,separator=%x20)"}, rng...)...)
	if code != 0 {
		return false, nil
	}
	for _, line := range strings.Split(out, "\n") {
		sha, stamps, _ := strings.Cut(strings.TrimSpace(line), " ")
		for _, id := range strings.Fields(stamps) {
			if id != t.Attempt {
				g.res.SHA = sha
				g.verdict(GateStaleAttempt, fmt.Sprintf("GATE %s refused — commit %s on %s was pushed by attempt %s, which the slot no longer holds; nothing landed", g.o.Slot, sha, g.branch, id),
					"the assignment was reclaimed or transferred: the holder's own commits are not affected, so have it check that branch, or reassign the issue")
				return true, nil
			}
		}
	}
	return false, nil
}

// stepOutcome refuses a PR whose worker reported partial or failed through
// `worker done --outcome`: it goes to review or relay, never straight to the
// gate. A relay or a transfer is a new brief and clears the outcome; so does the
// worker's next `worker done`. It reads the slot, so a queued PR and an
// external one have no outcome, and it runs under CheckOnly too.
func (g *gate) stepOutcome() (bool, error) {
	if g.target.Queued || g.target.External || g.target.Outcome == "" {
		return false, nil
	}
	g.verdict(GateOutcomeOpen, fmt.Sprintf("GATE %s refused — the worker reported %s; nothing landed", g.o.Slot, g.target.Outcome),
		fmt.Sprintf("read the summary (`rota round status`), then relay slot %s to finish the rest or `rota round transfer` it; a later `worker done --outcome done` lifts this", g.o.Slot))
	return true, nil
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
	if !o.LandVerified { // a train already merged every PR cleanly, in order, in its scratch tree
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
	if !o.LandVerified {
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
	info, ok := g.viewed, true
	g.viewed = nil
	if info == nil {
		var v tracker.PRInfo
		if v, ok = g.prInfo(); ok {
			info = &v
		}
	}
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
	}
	// The forge can lag a push: origin has the commit and the PR still names the
	// old head. Read again a bounded number of times before calling it a mismatch.
	for i := 1; info.HeadSHA != g.verified && i < gateHeadTries; i++ {
		g.e.Sleep(gateHeadWait)
		v, ok := g.prInfo()
		if !ok {
			return g.broke(fmt.Sprintf("could not re-read PR %s from %s", g.prNum, g.provider))
		}
		info = &v
		if info.State != "OPEN" || info.Head != g.branch {
			break
		}
	}
	if info.HeadSHA != g.verified {
		g.verdict(GatePRMismatch,
			fmt.Sprintf("error: PR %s head is %s, the verified %s is %s — pushed since, or %s has not caught up with the push (read %d times)", g.prNum, info.HeadSHA, g.headRef, g.verified, g.provider, gateHeadTries),
			fmt.Sprintf("once the PR shows the pushed head, run `rota worker gate %s` again without --request-id: the same id replays this refusal", g.o.Slot))
		return true, nil
	}
	return false, nil
}

// gateHeadTries and gateHeadWait bound the wait for the forge's PR head to reach
// the commit origin already holds.
const (
	gateHeadTries = 5
	gateHeadWait  = 2 * time.Second
)

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
	return g.checkRequiredPRSections()
}

// stepBestOf refuses an attempt of a best-of:2 issue until a `rota round pick`
// names its PR, before any fetch, freshness check or bounce. It runs under
// CheckOnly too, so a train member is refused before anything merges. The
// refusal clears itself once the orchestrator picks.
func (g *gate) stepBestOf() (bool, error) {
	issue := g.target.Issue
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
	msg := fmt.Sprintf("GATE %s refused — %s is best-of:2 and no pick names %s; nothing landed", g.o.Slot, tracker.RefOf(rec.Issue), what)
	if rec.Pick != "" {
		msg = fmt.Sprintf("GATE %s refused — %s is best-of:2 and the pick chose %s; this attempt lost; nothing landed", g.o.Slot, tracker.RefOf(rec.Issue), rec.Pick)
	}
	g.verdict(GateBestOfUnpicked, msg, fmt.Sprintf("compare both attempts and run `rota round pick %s --pr <N> --reason-file <f>`", rec.Issue))
	return true, nil
}

// refList is the keys' refs joined by ", ".
func refList(keys []tracker.IssueKey) string {
	refs := make([]string, len(keys))
	for i, k := range keys {
		refs[i] = k.Ref()
	}
	return strings.Join(refs, ", ")
}

// NeedsCross reports whether a branch at depth needs a review-cross verdict:
// its cross requirement names a harness and the depth reviews anything.
func NeedsCross(res ReviewDepth, depth config.ReviewDepth) bool {
	return res.Cross != "" && res.Cross != CrossNone && depth != config.DepthNone
}

// assignedHarness is the harness of the latest assign ledger entry for issue,
// the author of a PR whose slot moved on; "" when the ledger has none, so the
// caller falls back to the issue's label and the configured default.
func assignedHarness(root, issue string) string {
	if issue == "" {
		return ""
	}
	es, _ := ledger.Load(root)
	if e := ledger.LatestAssign(es, ledger.AssignQuery{Issue: issue, RequireHarness: true}); e != nil {
		return e.Harness
	}
	return ""
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
	cross, err := g.in.cfg.CrossReview()
	if err != nil {
		return g.broke(fmt.Sprintf("%v; run rota config check", err))
	}
	lenses, err := g.in.cfg.Lenses()
	if err != nil {
		return g.broke(fmt.Sprintf("%v; run rota config check", err))
	}
	src := ReviewSources{Policy: policy, Git: g.e.Git, Dir: g.root, Span: g.baseRef + "..." + g.headRef, Cross: cross, Author: g.target.Kind, Lenses: lenses}
	if src.Author == "" {
		src.Author = assignedHarness(g.root, g.target.Issue)
	}
	hasSpec := verdict.HasSpec(g.target.Issue)
	if key, ok := tracker.ParseKey(g.target.Issue); ok {
		if g.issues != nil {
			src.Issue, src.Issues = key, g.issues
			for _, id := range g.target.Bundle {
				if m, ok := tracker.ParseKey(id); ok {
					src.Members = append(src.Members, m)
				}
			}
		}
	}
	if g.pr != "" {
		src.PRBody = g.prBody
	}
	res, err := ResolveReviewDepth(g.ctx, src)
	if err != nil {
		return g.broke(err.Error())
	}
	depth, why := res.Depth, res.Why
	g.res.Notes = append(g.res.Notes, fmt.Sprintf("REVIEW-DEPTH %s — %s (%s)", g.o.Slot, depth, why))
	if res.Cross != "" {
		g.res.Notes = append(g.res.Notes, fmt.Sprintf("REVIEW-CROSS %s — %s (%s)", g.o.Slot, res.Cross, res.CrossWhy))
	}
	for _, l := range res.Lenses {
		g.res.Notes = append(g.res.Notes, fmt.Sprintf("REVIEW-LENS %s — %s (%s)", g.o.Slot, l.Name, l.Why))
	}
	if !g.o.CheckReviews {
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
	have := verdict.Recorded(g.root, g.branch, strings.TrimSpace(head))
	missing := verdict.MissingReviews(depth, hasSpec, have)
	// A depth of none reviews nothing, so it asks for no cross review either.
	crossMissing := ""
	if NeedsCross(res, depth) {
		if r, ok := verdict.CrossAt(g.root, g.branch, strings.TrimSpace(head)); !ok || r.Harness != res.Cross {
			crossMissing = fmt.Sprintf("%s verdict from %s (%s)", verdict.ReviewCross, res.Cross, res.CrossWhy)
		}
	}
	// Each triggered lens needs its own record at head (depth none triggers none).
	var lensMissing []string
	for _, l := range res.Lenses {
		kind := verdict.LensKind(l.Name)
		if _, ok := verdict.LensAt(g.root, g.branch, kind, strings.TrimSpace(head)); !ok {
			lensMissing = append(lensMissing, fmt.Sprintf("%s verdict (%s)", kind, l.Why))
		}
	}
	if len(missing) == 0 && crossMissing == "" && len(lensMissing) == 0 {
		return false, nil
	}
	g.res.SHA, _ = g.git(g.root, "rev-parse", "--short=7", g.headRef)
	var needs []string
	if len(missing) > 0 {
		needs = append(needs, fmt.Sprintf("review depth %s (%s) needs a recorded %s verdict", depth, why, strings.Join(missing, " and ")))
	}
	hint := fmt.Sprintf("run /rota-review on %s (it records the verdicts), or loosen ship.review", g.branch)
	if crossMissing != "" {
		needs = append(needs, "the cross-harness review needs a recorded "+crossMissing)
		hint = fmt.Sprintf("run /rota-review on %s (it records the verdicts and runs the cross-harness reviewer), or add the review:none label to the issue to skip the cross-harness review", g.branch)
	}
	if len(lensMissing) > 0 {
		needs = append(needs, "the review lenses need a recorded "+strings.Join(lensMissing, " and "))
		hint += fmt.Sprintf("; the review lenses need a reviewer per triggered lens recorded by /rota-review on %s, or a looser review.lenses", g.branch)
	}
	g.verdict(GateReviewMissing, fmt.Sprintf("GATE %s refused — %s; nothing landed", g.o.Slot, strings.Join(needs, ", and ")), hint)
	return true, nil
}

// stepVerdict refuses a branch with a recorded FAIL verdict (B3), the rule the
// ship paths apply. It runs under CheckOnly too, so a train member is refused
// before anything merges.
func (g *gate) stepVerdict() (bool, error) {
	if g.o.Verdict == nil {
		return false, nil
	}
	head, _ := g.git(g.root, "rev-parse", "--short", g.headRef) // the head the gate merges
	if err := g.o.Verdict(g.branch, head); err != nil {
		g.res.Verdict = GateVerdictBlocked
		return true, err
	}
	return false, nil
}

// stepCheckOnly ends a CheckOnly gate with verdict fresh.
func (g *gate) stepCheckOnly() (bool, error) {
	if g.o.CheckOnly {
		if g.o.BehaviourDir != "" {
			if done, err := g.behaviourRun(g.o.BehaviourDir); done || err != nil {
				return true, err
			}
		}
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
	if g.in.cfg.where != WhereCI || g.o.skipsVerify() {
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
		SkipLedger:   g.o.CheckOnly || g.o.skipsVerify(),
		SkipNoVerify: g.o.skipsVerify(),
		Files:        g.changedFiles,
	}
	if g.ci != nil {
		run.CIChanged = func() ([]string, error) {
			return g.e.ciDiffFiles(g.ctx, g.root, g.baseRef, g.verified)
		}
	}
	if g.o.Approval != nil {
		run.Approve = func(files func() ([]string, error)) (*exitcode.Refusal, error) {
			return g.o.Approval.Check(g.o.Slot+" into "+g.o.Base, files, func() (ApprovalThread, error) {
				return TargetApprovalThread(g.target)
			})
		}
	}
	r, approval, ref, err := run.run()
	switch {
	case approval:
		g.res.Verdict = GateApprovalRequired
		g.res.Refusal = ref
		if ref != nil {
			err = ref.Err
		}
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
	if g.remote && !g.o.skipsVerify() {
		return g.forgeRefuses(), nil
	}
	return false, nil
}

// stepVerifyFirst verifies the merge result in a scratch tree, before anything
// lands (stage 2).
func (g *gate) stepVerifyFirst() (bool, error) {
	if g.o.skipsVerify() {
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
	if done, err := g.vacuousRun(baseSHA, sha); done || err != nil {
		return true, err
	}
	if done, err := g.behaviourRun(dir); done || err != nil {
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
	w := g.tierWalk(nil, func(string, VerifyResult) (bool, error) {
		g.verdict(GateVerifyFailed, fmt.Sprintf("GATE-FAIL %s — the merge result of %s does not pass CI; nothing landed\n%s", o.Slot, g.branch, vr.detail()),
			fmt.Sprintf("send %s back to fix it, then re-gate", o.Slot))
		return true, nil
	})
	if ended, _, err := w.take(tierFull, vr); ended || err != nil {
		return true, err
	}
	g.ciRun = &vr
	return false, nil
}

// verifyLocal runs test.full, then test.e2e, on the scratch merge in dir.
func (g *gate) verifyLocal(dir, _ string) (bool, error) {
	return g.verifyTiers(tierFull, dir, false)
}

// verifyTiers walks the tiers from first in dir through the shared walk,
// ending at the first that fails. landed says the tree is already on the base:
// a red result is then fixed forward, else nothing landed and the slot goes
// back. An empty test.full leaves a NO-VERIFY note; test.e2e passing after it
// clears VerifySkipped. done is true when it ends the gate with a verdict.
func (g *gate) verifyTiers(first, dir string, landed bool) (bool, error) {
	w := g.tierWalk(func(_ string, cmds []string) (VerifyResult, error) {
		vr, err := g.e.runGateVerify(g.ctx, cmds, dir, g.in.ledger)
		if err == nil {
			prs := []string{}
			if g.res.PR != "" {
				prs = append(prs, g.res.PR)
			}
			err = g.e.recordFlakes(g.root, g.o.Round, prs, vr)
		}
		return vr, err
	}, g.redTier(landed))
	ended, err := w.walk(g.in.cfg, first)
	if w.fullEmpty {
		g.res.VerifySkipped = true
	}
	if w.e2eGreen {
		g.res.VerifySkipped = false
	}
	return ended, err
}

// tierWalk builds the gate's walk: it records into the result and stops on a
// run that gave no answer (only a CI run can). Only run and red differ.
func (g *gate) tierWalk(run func(string, []string) (VerifyResult, error), red func(string, VerifyResult) (bool, error)) *tierWalk {
	res := g.res
	return &tierWalk{
		subject: g.o.Slot,
		run:     run,
		stop: func(vr VerifyResult) bool {
			v, msg, hint := vr.stopVerdict(g.o.Slot)
			if v == "" {
				return false
			}
			g.verdict(v, msg, hint)
			return true
		},
		red:   red,
		notes: &res.Notes, verified: &res.Verified, excluded: &res.Excluded, flaky: &res.Flaky,
	}
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
	return git.ChangedPaths(g.ctx, g.e.Git, g.root, g.baseRef+"..."+g.verified)
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
	if o.skipsVerify() {
		res.Verdict, res.VerifySkipped = GatePass, true
		if !o.LandVerified {
			res.Notes = append(res.Notes, noVerifyFlagNote(o.Slot))
		}
		return true, nil
	}
	if g.ciRun != nil {
		if !g.confirmCITree() {
			return true, nil
		}
		if done, err := g.verifyTiers(tierE2E, g.root, true); done || err != nil {
			return true, err
		}
		res.Verdict = GatePass
		return true, nil
	}
	if tree := g.landedTree(); tree != g.verifiedTree {
		res.Notes = append(res.Notes, fmt.Sprintf("VERIFY-AGAIN %s — the landed tree %s differs from the verified scratch merge %s (%s moved before the merge); verifying the landed %s", o.Slot, strutil.ShortSHA(tree), strutil.ShortSHA(g.verifiedTree), o.Base, o.Base))
		res.Verified, res.Excluded, res.VerifySkipped = nil, nil, false
		if done, err := g.verifyTiers(tierFull, g.root, true); done || err != nil {
			return true, err
		}
	}
	res.Verdict = GatePass
	return true, nil
}

// redTier is the gate's answer to a failed tier. landed says the tree is
// already on the base: a red result is then fixed forward, else nothing landed
// and the slot goes back. It always ends the gate with a verdict.
func (g *gate) redTier(landed bool) func(string, VerifyResult) (bool, error) {
	return func(tier string, vr VerifyResult) (bool, error) {
		res, o := g.res, g.o
		e2e := tier == tierE2E
		what := "verification"
		if e2e {
			what = "test.e2e"
		}
		detail := vr.detail()
		if e2e {
			detail = fmt.Sprintf("last lines of the e2e output (full log: %s):\n%s", vr.LogPath, indentTail(vr.Log, 20))
		}
		if landed {
			if err := g.freezeFailure(vr); err != nil {
				return true, err
			}
			g.verdict(GateVerifyFailed, fmt.Sprintf("GATE-FAIL %s — merged tree does not pass %s at %s\n%s", o.Slot, what, res.SHA, detail),
				fmt.Sprintf("fix forward on %s; the owning slot has usually moved on", o.Base))
			return true, nil
		}
		g.verdict(GateVerifyFailed, fmt.Sprintf("GATE-FAIL %s — the merge of %s into %s does not pass %s at %s; nothing landed\n%s", o.Slot, g.branch, o.Base, what, res.SHA, detail),
			fmt.Sprintf("send %s back to fix it, then re-gate", o.Slot))
		return true, nil
	}
}

// pairwisePaths is the shared footprint check for train ordering, conflict
// attribution, preview pruning and stale-merge notes. Only the gate filters
// round.sharedPaths; the train passes nil so it sees every shared path.
func pairwisePaths(a, b, shared []string) []string {
	return overlap.Both(a, b, shared)
}

// pairwiseMergeConflict distinguishes a real conflict from a broken git probe.
// A non-zero failedCode means the probe failed; output is kept for diagnostics.
// Callers choose whether to prune disjoint paths first: the gate probes even
// disjoint footprints, since renames can conflict without a shared path.
func pairwiseMergeConflict(runGit func(string, ...string) (string, int), root, a, b string) (conflict bool, output string, failedCode int) {
	out, code := runGit(root, "merge-tree", "--write-tree", "--no-messages", a, b)
	switch code {
	case 0:
		return false, "", 0
	case 1:
		return true, "", 0
	default:
		return false, out, code
	}
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
	conflict, out, code := pairwiseMergeConflict(g.git, root, g.baseRef, g.headRef)
	if code != 0 {
		return "", nil, fmt.Sprintf("git merge-tree %s %s exited %d: %s", g.baseRef, g.headRef, code, out)
	}
	if conflict {
		return "the merge conflicts", nil, ""
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
	return "", pairwisePaths(onBase, onHead, g.in.cfg.SharedPaths), ""
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

// detectProvider is the forge the PR URL names (a bare number carries none),
// else tracker.ResolveProvider's answer for kind under forge.provider
// (configured), reading origin through Env.Git.
func (e Env) detectProvider(ctx context.Context, root, pr, configured string, kind tracker.ForgeKind) (string, error) {
	if p, ok := tracker.ParsePRURL(pr); ok {
		return p.Provider, nil
	}
	r, err := tracker.ResolveProvider(ctx, kind, "", configured, root, tracker.GitExec(e.Git))
	return r.Provider, err
}

// issueSide is the issue source the gate reads beside forge, built from
// issues.source: none for a file source; the forge adapter itself when the
// source is that forge's own tracker (github or gitlab matching the forge's
// provider, forge, or an unmigrated legacy config); else the tracker the source
// names (Env.Issues).
func (e Env) issueSide(forge Forge, forgeProvider, dir string, s tracker.Settings) (Issues, error) {
	switch s.Source {
	case config.SourceFile, "":
		return nil, nil
	case config.SourceForge, config.SourceIssues, forgeProvider:
		if is, ok := forge.(Issues); ok {
			return is, nil
		}
	}
	return e.Issues(dir, s)
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
	resolved    GateTarget
	sectionBody *string // successful PR body read, reused by the section check

	// viewed is the PR stepSlotHead read, for stepPRMatches to reuse once.
	viewed   *tracker.PRInfo
	branch   string
	pr       string
	prNum    string
	provider string
	forge    Forge
	issues   Issues // the issue source; nil for a file source
	local    bool   // the forge is local: no PR, direct merge
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
		if tracker.IsCLIMissing(err) {
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
	info, err := g.prView()
	return info.Body, err
}

func (g *gate) prView() (tracker.PRInfo, error) {
	n, ok := PRRefNumber(g.pr)
	if !ok {
		return tracker.PRInfo{}, fmt.Errorf("no PR number in %q", g.pr)
	}
	return g.forge.PRView(g.ctx, n)
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
		if tracker.IsCLIMissing(err) {
			g.res.Notes = append(g.res.Notes, fmt.Sprintf("PROVENANCE-SKIP %s — no %s to read approvals from", g.o.Slot, g.cliName))
			return "", ""
		}
		return "", fmt.Sprintf("could not read the body of %s to check its approvals: %v", g.pr, err)
	}
	g.sectionBody = &body
	relays, offered, support, err := g.supportApprovals()
	if err != nil {
		// Relay citations fall back to the local relay log with a skip note.
		// An offer citation cannot: the local log never holds one, so it
		// fails, naming the read error (#1883).
		g.res.Notes = append(g.res.Notes, fmt.Sprintf("PROVENANCE-SKIP %s — could not read the issue comments of %s (%v); checked against the local relay log only", g.o.Slot, g.pr, err))
		if msg := CheckApprovals(body, g.target.relays); msg != "" {
			return fmt.Sprintf("PROVENANCE-FAIL %s: %s", g.o.Slot, msg), ""
		}
		if msg := CheckApprovalsOffer(body, g.target.relays, false); msg != "" {
			return fmt.Sprintf("PROVENANCE-FAIL %s: %s (issue read failed: %v)", g.o.Slot, msg, err), ""
		}
		return "", ""
	}
	check := CheckApprovals
	if support {
		check = func(body string, relays []any) string { return CheckApprovalsOffer(body, relays, offered) }
	}
	if msg := check(body, relays); msg != "" {
		return fmt.Sprintf("PROVENANCE-FAIL %s: %s", g.o.Slot, msg), ""
	}
	return "", ""
}

// supportApprovals is the slot's local relay log plus, for a PR whose issue
// carries the support label under round.support, the relays and the offer the
// other machine left as issue comments (#1827). Only writer-trusted authors
// count. support says the PR is one, so the offer citation is enforced; any
// other PR returns the local log alone, with no extra read.
func (g *gate) supportApprovals() (relays []any, offered, support bool, err error) {
	relays = g.target.relays
	cfg := g.in.cfg
	key, ok := tracker.ParseKey(g.target.Issue)
	if cfg.Support == "" || cfg.SupportLabel == "" || g.issues == nil || !ok {
		return relays, false, false, nil
	}
	is, err := g.issues.Get(g.ctx, key, true)
	if err != nil {
		return nil, false, false, err
	}
	if !slices.Contains(is.Labels, cfg.SupportLabel) {
		return relays, false, false, nil
	}
	trust := NewMarkerTrust(g.ctx, g.issues)
	relays = append(slices.Clone(relays), RelaysFromComments(g.ctx, trust, is.Comments)...)
	return relays, HasSupportOffer(trust, is.Comments), true, nil
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
	var state, body, title, head string
	for i := 0; i < 5; i++ {
		info, ok := g.prInfo()
		if !ok {
			g.broke(fmt.Sprintf("could not re-read PR %s after merging", g.prNum))
			return "", true
		}
		state, sha, body, title, head = info.State, info.MergeSHA, info.Body, info.Title, info.Head
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
	src := tracker.ClosingSource{Body: body, Title: title, Branch: head}
	if g.closeMerged(src) {
		g.releaseClaimLabels(src)
	}
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

// closeMerged closes linked milestone issues. On other bases, it closes the
// slot's issue and bundle on trackers implementing MergeCloser (Jira, Linear).
// A failed close is a warning because the PR has already landed. It returns
// whether every close succeeded, allowing the caller to sweep claim labels;
// milestone closes also release each successful issue's labels independently.
func (g *gate) closeMerged(src tracker.ClosingSource) bool {
	if id, ok := strings.CutPrefix(g.o.Base, "milestone/"); ok && id != "" && g.issues != nil {
		return g.closeOnMilestone(src, labels.OnMilestonePrefix+id)
	}
	mc, ok := g.issues.(tracker.MergeCloser)
	if !ok {
		return true
	}
	closed := true
	for _, key := range g.bundleKeys() { // the lead, then each member the PR names
		if !g.issues.ClosesIssue(key, src) {
			continue
		}
		is, err := g.issues.Get(g.ctx, key, false)
		if err == nil && slices.Contains(is.Labels, PartialSliceLabel) {
			continue
		}
		if err == nil {
			err = mc.CloseAfterMerge(g.ctx, key, g.pr)
		}
		if err != nil {
			g.res.Warnings = append(g.res.Warnings, fmt.Sprintf("ISSUE-OPEN %s — %s merged but %s could not be closed, so it keeps its claim for `rota round reconcile`: %v", g.o.Slot, g.pr, key.Ref(), err))
			closed = false
		}
	}
	return closed
}

func tailLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
