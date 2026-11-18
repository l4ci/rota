package worker

import (
	"context"
	"errors"
	"fmt"
	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/labels"
	"github.com/l4ci/rota/internal/landing"
	"github.com/l4ci/rota/internal/rotastate"
	"github.com/l4ci/rota/internal/testledger"
	"github.com/l4ci/rota/internal/tracker"
	"slices"
	"strings"
	"time"
)

// Merge gate for one worker slot's branch/PR verifies the merged tree before
// landing it on the cycle branch, with the #39 fixes.
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

// AttemptTrailer is the commit trailer a worker stamps its attempt id with.
const AttemptTrailer = "Rota-Attempt"

// PartialSliceLabel marks an issue whose PR lands only a slice of it, so the PR
// body must not close it.
const PartialSliceLabel = labels.PartialSlice

// noVerifyRefusal is the message and hint of the refusal for an empty
// test.full under a local verify, shared by the gate and the train.
func noVerifyRefusal(what string) (msg, hint string) {
	return fmt.Sprintf("%s refused — test.full is empty (read from config key %s), so nothing would verify the merged tree; nothing landed", what, config.TestFullKey),
		"set it with `rota config set test.full '[\"<command>\"]'`, or pass --no-verify to merge unverified on purpose"
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
	// PRBase is the branch the PR is expected to target when that is not Base:
	// a member of a stacked integration branch (#3098) merges onto the branch
	// under it but its PR targets the base that branch lands on.
	PRBase string
	// FinalSlice marks the PR as the last slice of a partial-slice issue, the
	// one PR allowed to close it (--final-slice). Without it a PR the forge
	// says closes a partial-slice issue is refused (stepCloses).
	FinalSlice bool
	// Prune makes a pass that releases an adopted slot also remove its
	// worktree and branch.
	Prune bool
	// Round reads the round lease for the gate's ledger entries; the gate makes
	// one when nil. A caller that writes more entries after the gate shares it.
	Round *landing.RoundMemo
	// LandVerified marks a landing step of a merge train (see Train): the
	// train merged the PRs together and verified that tree once, so this gate
	// only lands. It does not verify (no NO-VERIFY note either: the train
	// reports its own) and does not refuse a branch behind the base only
	// because an earlier train member landed.
	LandVerified bool
	// LandVerifiedTip binds an integration PR's LandVerified to the tip its
	// train verified (#3096): a head pinned anywhere else refuses the merge,
	// since the skipped verify never saw it.
	LandVerifiedTip string
	// LandVerifiedBase is the base commit that verify merged: a base that moved
	// since refuses the merge for the same reason.
	LandVerifiedBase string
	// HoldsLandLock says the caller already holds the repository's land lock
	// (the train does, through its landing steps), so the gate does not retake
	// it. Without it a gate takes the lock itself unless it is CheckOnly.
	HoldsLandLock bool
	// The review-verdict gate (B3) runs after provenance, also under CheckOnly.
	// It reads the verdict store itself (verdict.BlockingGate); a blocking
	// record stops the gate with verdict verdict-blocked and is returned as is.
	// NoVerdict is the only way past it, for a run another gate already judged.
	NoVerdict bool
	// VerdictRepo is the sub-repo name keying the store in an umbrella, else "".
	VerdictRepo string
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
	// Observe is ReviewsOnly with no writes at all, for a read-only autopilot
	// preview: it judges the remote refs as last fetched instead of fetching,
	// and reads local verdicts only, without recovering PR verdict reports
	// into the store. Execution's own ReviewsOnly run does both.
	Observe bool
	// Approval is the merge-approval gate (B1), run after provenance and right
	// before the merge, never under CheckOnly. Its thread comes from the
	// resolved target. A refusal or an error stops the gate with verdict
	// approval-required; the refusal lands in GateResult.Refusal
	// and its error, or an adapter's own, is returned as is. Nil skips approval.
	Approval interface {
		Check(string, func() ([]string, error), func() (landing.ApprovalThread, error)) (*exitcode.Refusal, error)
	}
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
	// in is the gate input a train loaded before any member landed. Set, the
	// gate uses it instead of reading root again, which by then may carry a
	// landed member's .rota/config.json. Only the train sets it.
	in *gateInput
}

// skipsVerify says the gate runs no verify tier: --no-verify, or a train's
// landing step whose tree the train already verified.
func (o GateOpts) skipsVerify() bool { return o.NoVerify || o.LandVerified }

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
	return GateTarget{Name: s.Name(), Branch: s.Branch(), PR: s.PR(), Base: s.Base(), Task: s.Task(), ClaimID: s.ClaimID(), Issue: HeldID(s.Task(), s.Branch(), s.Name()), Bundle: s.Bundle(), relays: s.Relays(), Attempt: s.Attempt(), AttemptBase: s.AttemptBase(), Outcome: openOutcome(s), Kind: s.Kind()}
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
			t := gateQueued(*q)
			t.relays = dedupeRelays(append(slices.Clone(t.relays), r.LoggedRelays(q.PR)...))
			return t, nil
		}
		for _, sl := range r.Slots() {
			if m, ok := PRRefNumber(sl.PR()); ok && m == n {
				t := gateSlot(sl)
				t.relays = dedupeRelays(append(slices.Clone(t.relays), r.LoggedRelays(sl.PR())...))
				return t, nil
			}
		}
		return GateTarget{}, fail(exitcode.ExitResolution, fmt.Sprintf("no PR in review or slot records PR #%d", n))
	}
	return GateTarget{}, fail(exitcode.ExitResolution, fmt.Sprintf("slot '%s' is not in the pool", arg))
}

// ResolveGateTargets loads the registry at root and resolves each arg as
// GateTargetAny does. An arg that does not resolve is left out of the map; an
// unreadable registry is an error.
func ResolveGateTargets(root string, args []string) (map[string]GateTarget, error) {
	reg, err := LoadRegistry(root)
	if err != nil {
		return nil, err
	}
	out := make(map[string]GateTarget, len(args))
	for _, a := range args {
		if gt, err := reg.GateTargetAny(a); err == nil {
			out[a] = gt
		}
	}
	return out, nil
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
	// ClosingRefs reads what the forge says merging the PR closes; known is
	// false for a forge that cannot report it (GitLab).
	ClosingRefs(ctx context.Context, pr int) (nums []int, known bool, err error)
	// PRSetBody rewrites the PR body (the stacked-branch refresh).
	PRSetBody(ctx context.Context, pr int, body string) error
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
func (e Env) Gate(ctx context.Context, root string, o GateOpts) (landing.GateResult, error) {
	e = e.WithDefaults()
	if o.Round == nil {
		o.Round = &landing.RoundMemo{}
	}
	if o.Observe {
		o.ReviewsOnly = true
	}
	if o.ReviewsOnly {
		o.CheckOnly, o.CheckReviews = true, true
	}
	if o.CheckOnly || o.HoldsLandLock { // read-only, or the caller already holds the land lock
		res, err := e.gate(ctx, root, o)
		res.Round = o.Round
		return res, err
	}
	var res landing.GateResult
	if common, key, ok := e.gateRunKey(ctx, root, o.Slot); ok {
		other, release, err := claimGateRun(e.procs(), common, key)
		if err != nil {
			return res, err
		}
		if other != 0 {
			res = landing.GateResult{Slot: o.Slot, Base: o.Base, Round: o.Round, Verdict: landing.GateRunning,
				Err:  fmt.Sprintf("GATE %s refused — another gate for this PR is running or queued (pid %d); nothing landed", o.Slot, other),
				Hint: fmt.Sprintf("wait for pid %d to finish; if it is gone, remove the stale marker under the repo's rota state dir", other)}
			return res, nil
		}
		defer release()
	}
	land := func() error {
		return e.withLandingRun(ctx, root, o.Slot, o.NoWait, func(bool) (err error) {
			res, err = e.gate(ctx, root, o)
			return err
		})
	}
	rec, err := e.integrationLanding(root, o)
	switch {
	case err != nil:
		res = landing.GateResult{Slot: o.Slot, Base: o.Base, Round: o.Round, Verdict: landing.GateCheckBroke,
			Err: checkBrokeMessage(o.Slot, "cannot read the integration records: "+err.Error())}
		return res, nil
	case rec != nil:
		// The integration lock before the land lock, as the train takes it:
		// no train of this branch runs while its PR lands. The record is read
		// again under both, and the verify skip decided there, so neither a
		// train nor another landing can move what it was decided on.
		err = e.WithIntegrationLock(ctx, root, rec.Name, func() error {
			return e.withLandingRun(ctx, root, o.Slot, o.NoWait, func(bool) error {
				return e.landIntegration(ctx, root, o, rec.Branch, &res)
			})
		})
	default:
		err = land()
	}
	if errors.Is(err, errLandingBusy) {
		res = landing.GateResult{Slot: o.Slot, Base: o.Base, Verdict: landing.GateRunning, Err: err.Error()}
		err = nil
	}
	res.Round = o.Round
	if rec != nil && err == nil && (res.Verdict == landing.GatePass || res.Verdict == landing.GateAlreadyMerged) {
		res.Notes = append(res.Notes, e.restackDependents(ctx, root, *rec)...)
	}
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
func (e Env) gate(ctx context.Context, root string, o GateOpts) (landing.GateResult, error) {
	res := landing.GateResult{Slot: o.Slot, Base: o.Base}
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
	var in gateInput
	if o.in != nil {
		in = *o.in
	} else if in, err = e.GateInput(root); err != nil {
		return res, err
	}
	in.cfg.WarnLegacy(e.Stderr)
	g := gateRun{e: e, ctx: ctx, root: root, o: o, in: in, resolved: t}
	return g.run(t, res)
}

// rem is the configured git remote the gate fetches from and compares against.
func (g gateRun) rem() string { return g.in.cfg.RemoteName() }

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

// gateHeadTries and gateHeadWait bound the wait for the forge's PR head to reach
// the commit origin already holds.
const (
	gateHeadTries = 5
	gateHeadWait  = 2 * time.Second
)

func indentTail(text string, n int) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return "    " + strings.Join(lines, "\n    ")
}

func tailLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func (r gitRunner) Run(args ...string) (git.Result, error) { return r.e.Git(r.ctx, r.dir, args...) }
