package landing

import (
	"fmt"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/flakeledger"
	"github.com/l4ci/rota/internal/testledger"
	"slices"
	"strings"
	"time"
)

// GateResult is the gate's answer. Err is the message for a non-success
// verdict, Hint what to do about it.
type GateResult struct {
	// IntegrationMembers are the member issues of an integration PR that
	// landed (#3096): the post-gate bookkeeping ends each one.
	IntegrationMembers []string
	Slot               string
	// TargetSlot is the pool slot the gate target resolved to, "" when it
	// resolved to none (an external PR, a queued record whose slot moved on).
	// Slot echoes the argument, which may be a PR number.
	TargetSlot string
	Verdict    string
	Base       string
	// Issue is the item the gate acted on, resolved once from the target: the
	// queued record's, else the one the slot held. "" when it cannot be told.
	Issue  string
	Branch string
	SHA    string
	// MergeCommit is set to SHA where SHA names a merge commit rather than the
	// head that was checked. The report labels SHA by whether the two agree.
	MergeCommit string `json:"-"`
	// CheckedHead is the full head commit a CheckOnly gate checked, the one
	// its review verdicts were read at; an integration train merges exactly it.
	CheckedHead   string
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
	// Gaps lists every record gap the gate found, in check order, when more than
	// one refused the PR; Verdict, Err and Hint stay the first gap's.
	Gaps []GateGap
	// Refusal is the approval gate's refusal, set with verdict approval-required
	// when it refused rather than failed to run.
	Refusal *exitcode.Refusal
	Notes   []string // PROVENANCE-SKIP and NO-VERIFY lines for stderr
	// Tier is the change tier the gate verified the merge by (#2348), with the
	// reason; Skipped are the test tiers it ran a cheaper list in place of.
	Tier, TierReason string
	Skipped          []string
	// Warnings are problems the gate survived: the merge landed, but something
	// after it needs a caller's eye (a Jira issue that would not close).
	Warnings []string
	// Excluded lists the test-ledger entries that excused a failing command.
	// Expired lists the entries past their expiry, which fail the gate.
	Excluded, Expired []testledger.Entry
}

// GateGap is one record gap of a refused gate: the blockedBy key, the message
// and the fix hint of the check that found it.
type GateGap struct {
	BlockedBy, Msg, Hint string
	Verdict              string `json:"-"`
}

// OK reports a successful verdict.
func (r GateResult) OK() bool { return r.Verdict == GateFresh || r.Verdict == GatePass }

// TrainMember is one target of the train, in order.
type TrainMember struct {
	Target  string
	Branch  string
	PR      string
	Tier    string // the change tier its check-only gate put it in
	Landed  bool
	Culprit bool
	// CheckedHead is the head its check-only gate reviewed; an integration train
	// merges it, never a head pushed after the check. ResolvedIssue is the item that
	// gate resolved, also for an external PR no slot holds.
	CheckedHead, ResolvedIssue string `json:"-"`
}

// Issue is the item the member's check-only gate resolved, "" when unknown.
func (m TrainMember) Issue() string { return m.ResolvedIssue }

// TrainResult is the train's answer. Verdict is pass, or the verdict of the
// step that refused: a member's check verdict (stale, pr-mismatch,
// provenance-fail, ...), merge-failed, verify-failed, approval-required,
// base-moved. Culprit names the member the verdict is about, when one.
type TrainResult struct {
	Base    string
	Verdict string
	// Tier is the highest change tier of the members, which the train verified
	// by (#2348); Skipped are the test tiers it ran a cheaper list in place of.
	Tier    string
	Skipped []string
	Members []TrainMember
	Culprit string
	// Interaction names the members whose merge together makes a red test that
	// passes on each of them alone (verdict interaction); empty otherwise.
	Interaction []string
	// Unreproduced is true when the red did not reproduce on any member alone,
	// any pair or a rerun of the red prefix: a flake that blames no member.
	Unreproduced bool
	// Culprits lists members excluded for missing PR sections or review verdicts.
	Culprits []TrainCulprit
	// CulpritGate is the gate result that named the culprit, for the bookkeeping
	// that follows (its head SHA, PR and branch). Zero when none did.
	CulpritGate GateResult
	Verified    []string            // test.full commands that passed
	Flaky       []flakeledger.Entry // commands recovered on one sequential retry
	E2EVerified []string            // test.e2e commands that passed
	Landed      []string
	// AlreadyMerged lists members whose PR was on the base before this run
	// (an earlier train or a hand merge): this run landed nothing for them.
	AlreadyMerged []string
	Changed       bool
	SHA           string
	Err           string
	Hint          string
	// Refusal is the approval gate's refusal, set with verdict approval-required.
	Refusal *exitcode.Refusal
	Notes   []string
	Order   []string // one line per member, in merge order, with the reason
	// CacheHits names each verify answered from the verdict cache instead of
	// being run; Transient the members earlier named culprit that this train
	// passed with (#400).
	CacheHits []string
	Transient []string
	// Excluded lists the test-ledger entries that excused a failing command;
	// Expired the entries past their expiry, which fail the train.
	Excluded, Expired []testledger.Entry
	// Plan is the conflict map of a --plan run; nil otherwise.
	Plan *TrainPlan
	// Stop is the evidence of a red tier under OnFailStop; nil otherwise.
	Stop *TrainStop
	// Log is the verify log a cancelled train kept on disk; "" otherwise.
	Log string
	// Integration is the integration branch's record after the run; nil for a
	// plain train.
	Integration *Integration
	// VerifyTime is the time spent verifying the whole train; DiagnoseTime the
	// time spent on prefix runs that look for the culprit (#2753).
	VerifyTime, DiagnoseTime time.Duration
	// Original is the failure of the full-train run that started a diagnosis;
	// Diagnostic lists the prefix runs that failed after it. Nil/empty when no
	// tier failed.
	Original   *TrainFailure
	Diagnostic []TrainFailure
}

// TrainFailure is one failed verify run: the tier, the failed command, the
// failing tests its output names and the tree under test.
type TrainFailure struct {
	Tier    string
	Command string
	Tests   []string
	Members int    // members merged on the base for this run
	Tip     string // tip SHA of the tree under test
	// OnlyInDiagnostic names the tests (or, when none are named, the commands)
	// that failed here but not in the original run. Such a failure is not
	// confirmed as a cause: it needs a passing-alone check.
	OnlyInDiagnostic []string
}

// TrainStop is what a train that stopped on a red tier (OnFailStop) hands the
// orchestrator in place of a culprit: the failed command, the failing tests
// when its output names them, the scratch tree and the SHAs that were tested.
type TrainStop struct {
	Tier    string
	Command string   // the failed command (CI: the failed check)
	Tests   []string // go test's `--- FAIL:` names; empty when the output names none
	Scratch string   // the scratch tree path; removed when the train exits
	Base    string   // the base SHA the members were merged onto
	Members []TrainStopMember
}

// TrainStopMember is one tested member head.
type TrainStopMember struct{ Target, SHA string }

// TrainPlan is a train's dry-run answer: which member pairs conflict, the
// largest conflict-free subset in merge order and the members left out.
type TrainPlan struct {
	Conflicts []TrainConflict
	// Landable lists the targets of the largest conflict-free subset, in merge order.
	Landable []string
	Blocked  []TrainBlocked
	// Command is the train line that lands exactly Landable.
	Command string
	// NoVerify mirrors the no-verify refusal: local verification with both
	// test.full and test.e2e empty. Command carries --no-verify when it is true.
	NoVerify bool
}

// TrainConflict is a pair of members whose merge conflicts.
type TrainConflict struct {
	A, B  string // targets
	Paths []string
}

// TrainBlocked is a member left out of the landable subset, with the landable
// members it conflicts with.
type TrainBlocked struct {
	Target string
	By     []string // targets
}

// TrainCulprit is a member excluded for missing PR sections or review verdicts
// before the train builds or verifies its merge. Remaining members train normally.
type TrainCulprit struct {
	Target    string
	PR        string
	BlockedBy string
	Gate      GateResult
}

// Integration is one integration branch of a round (`integrations` in the
// registry, keyed by branch): the members `worker train --integration` merged
// into it, the merges rota made itself, what the last green verify covered and
// the PR that lands it on the base.
type Integration struct {
	Name   string `json:"name"`
	Branch string `json:"branch"`
	Round  int    `json:"round"`
	Base   string `json:"base"`
	// Onto is the open integration branch this one is stacked on: it is then
	// Base, and OntoBase is where Base returns when Onto lands. Both are empty
	// for a branch on a plain base, and again once Onto has landed.
	Onto     string `json:"onto,omitempty"`
	OntoBase string `json:"ontoBase,omitempty"`
	// Members are the PRs merged into the branch, in merge order; Head is the
	// member head the last merge took.
	Members []IntegrationMember `json:"members"`
	// Start is the base commit the branch was first assembled on: every
	// first-parent commit after it is a rota merge or a fix.
	Start string `json:"start"`
	// Assembled is the tip after rota's own merges, before any fix commit.
	Assembled string `json:"assembled"`
	// RotaMerges are the first-parent commits rota made itself: member merges,
	// merges of the base and `rota worker refs` renewal commits. Every other
	// first-parent commit is a fix.
	RotaMerges []string `json:"rotaMerges"`
	// VerifiedTip is the tip the last green train verified, VerifiedBase the
	// base commit merged into it.
	VerifiedTip  string `json:"verifiedTip,omitempty"`
	VerifiedBase string `json:"verifiedBase,omitempty"`
	PR           string `json:"pr,omitempty"`
	// Items are the interaction items filed for this branch.
	Items []string `json:"items,omitempty"`
	// Landed says the forge merged the PR and the registry knows it. Settled
	// says the caller's cleanup after it (the member bookkeeping and parking the
	// member slots) finished: until then every gate of the PR replays it.
	Landed  bool `json:"landed,omitempty"`
	Settled bool `json:"settled,omitempty"`
}

// IntegrationMember is one member PR of an integration branch.
type IntegrationMember struct {
	Target string `json:"target"`
	PR     string `json:"pr"`
	Branch string `json:"branch,omitempty"`
	Issue  string `json:"issue,omitempty"`
	Head   string `json:"head"`
	// Pending is a member whose merge conflicted: an interaction item resolves
	// it on the branch, and Conflicts are the paths git named.
	Pending   bool     `json:"pending,omitempty"`
	Conflicts []string `json:"conflicts,omitempty"`
}

// OntoName is the name of the integration branch this one is stacked on.
func (it Integration) OntoName() string {
	return strings.TrimPrefix(it.Onto, fmt.Sprintf("integration/r%d-", it.Round))
}

// LandBase is the branch the integration finally lands on: its own base, or
// what a stacked one returns to when the branch under it lands.
func (it Integration) LandBase() string {
	if it.Onto != "" {
		return it.OntoBase
	}
	return it.Base
}

// TrainFlags are the flags that run this integration's train again.
func (it Integration) TrainFlags() string {
	onto := ""
	if it.Onto != "" {
		onto = " --onto " + it.OntoName()
	}
	return fmt.Sprintf("--integration %s%s --base %s", it.Name, onto, it.LandBase())
}

// Member is the member recorded for target, false when there is none.
func (it Integration) Member(target string) (IntegrationMember, bool) {
	for _, m := range it.Members {
		if m.Target == target {
			return m, true
		}
	}
	return IntegrationMember{}, false
}

// IsRotaMerge reports whether sha is a merge rota made on the branch.
func (it Integration) IsRotaMerge(sha string) bool { return slices.Contains(it.RotaMerges, sha) }

func (r TrainResult) OK() bool { return r.Verdict == GatePass }

// onlyIn lists what f shows failing that orig does not: its tests, or, when
// either run names no test, its failed commands.
func (f TrainFailure) OnlyIn(orig TrainFailure) []string {
	mine, theirs := f.Tests, orig.Tests
	if len(mine) == 0 || len(theirs) == 0 {
		mine, theirs = f.commands(), orig.commands()
	}
	var out []string
	for _, x := range mine {
		if !slices.Contains(theirs, x) {
			out = append(out, x)
		}
	}
	return out
}

// commands splits the joined failed commands back into a list.
func (f TrainFailure) commands() []string {
	if f.Command == "" {
		return nil
	}
	return strings.Split(f.Command, "; ")
}

// reproduces reports whether f shows any failure of orig: a run whose every
// failure is diagnostic-only does not, so it cannot pin the original failure
// on a member.
func (f TrainFailure) Reproduces(orig TrainFailure) bool {
	mine := f.Tests
	if len(mine) == 0 || len(orig.Tests) == 0 {
		mine = f.commands()
	}
	return len(mine) == 0 || len(f.OnlyInDiagnostic) < len(mine)
}
