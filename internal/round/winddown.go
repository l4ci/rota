package round

import (
	"context"
	"errors"
	"fmt"
	"github.com/l4ci/rota/internal/exitcode"
	"os"
	"strings"

	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/worker"
)

// Wind-down verdicts and slot outcomes.
const (
	VerdictClean        = "clean"
	VerdictVerifyFailed = "verify-failed"
	VerdictHoldsWork    = "holds-work"

	OutcomeParked    = "parked"
	OutcomeRetained  = "retained"
	OutcomeUnchanged = "unchanged"
)

// WindDownOpts are the flags of `rota round wind-down`.
type WindDownOpts struct {
	NoVerify  bool
	HolderPID int
	Settings  roundcfg.Settings
	Getenv    func(string) string
}

// SlotOutcome is one slot's part of the summary.
type SlotOutcome struct {
	Name, Outcome, Issue, PR string
	Merged                   *bool
	Dirty, Unmerged          []string
}

// WoundDown is what WindDown did.
type WoundDown struct {
	Round         int
	Base, Verdict string
	Slots         []SlotOutcome
	Verified      []string
	VerifySkipped bool
	Drift         int
	Lease         *Lease // present unless the lease was released
	Retained      bool   // some slot still holds work
	Changed       bool
	Warnings      []string
	VerifyLog     string
}

// WindDown re-verifies the base, parks every roster slot on park/<agent> and
// releases the lease. The order is the point: a failed verify keeps the lease,
// so the repo is never freed while the base is red, and a slot that holds work
// is reported and left alone, never discarded. Parking happens either way.
func (e Env) WindDown(ctx context.Context, root string, be Board, o WindDownOpts) (res WoundDown, err error) {
	res.Base = e.Base
	cd, err := e.commonDir(ctx, root)
	if err != nil {
		return res, err
	}
	le := e.leaseEnv()
	lease, _, held, err := le.Holds(cd, o.HolderPID, o.Getenv)
	if err != nil {
		return res, err
	}
	if !held {
		return res, &exitcode.Error{Exit: exitcode.ExitResolution, Message: "this process holds no round lease: nothing to wind down", Hint: "run it from the orchestrator that ran rota round start"}
	}
	res.Round = lease.Round

	// What each slot held, before parking clears it.
	before := map[string]SlotOutcome{}
	if rep, err := e.Status(ctx, root); err == nil {
		for _, r := range rep.Rows {
			so := SlotOutcome{Name: r.Name, Issue: r.Issue, PR: r.PR}
			switch r.PRState {
			case "merged":
				t := true
				so.Merged = &t
			case "open", "closed":
				f := false
				so.Merged = &f
			}
			before[r.Name] = so
		}
		res.Warnings = append(res.Warnings, rep.Warnings...)
	}

	// 1. Re-verify the base in the project root.
	if !o.NoVerify {
		if !worker.HasVerifyCommands(root) {
			res.VerifySkipped = true
			res.Warnings = append(res.Warnings, "NO-VERIFY: test.full is empty; the base was NOT checked by a command")
		} else {
			if err := e.requireBase(ctx, root); err != nil {
				return res, err
			}
			vr, err := e.workerEnv().Verify(ctx, root, root)
			if err != nil {
				return res, err
			}
			if vr.LogPath != "" {
				os.Remove(vr.LogPath)
			}
			for _, c := range vr.Failed {
				res.Warnings = append(res.Warnings, "verify FAILED: "+c)
			}
			if !vr.OK() {
				res.Verdict = VerdictVerifyFailed
			}
			res.Verified = vr.Verified
			res.VerifyLog = tail(vr.Log, 20)
		}
	} else {
		res.VerifySkipped = true
	}

	// 2. Park every registered roster slot.
	w := e.workerEnv()
	want := map[string]bool{}
	for _, n := range o.Settings.Roster {
		want[n] = true
	}
	for _, s := range worker.LoadRegistry(root).Slots() {
		name := s.Name()
		if !want[name] {
			continue
		}
		so := before[name]
		so.Name = name
		park := "park/" + name
		wasParked := s.Branch() == park && s.Task() == ""
		claim := s.ClaimID()
		issue := s.HeldID()
		if !wasParked {
			so.Issue = firstNonEmpty(so.Issue, issue)
		} else {
			so.Issue = ""
		}
		// End the session before the checkout moves under it, but only for a
		// slot that will park: a slot holding work keeps its session.
		handle := s.Handle()
		sessionKept := false
		if handle != "" {
			if _, cerr := w.ResetTo(root, name, "", park, true); cerr == nil {
				if kerr := w.KillSlot(ctx, root, name); kerr != nil {
					sessionKept = true
					res.Warnings = append(res.Warnings, fmt.Sprintf("SESSION-KEPT %s: session still running (%v)", name, kerr))
				}
			}
		}
		rr, rerr := w.ResetTo(root, name, "", park, false)
		var we *exitcode.Error
		switch {
		case errors.As(rerr, &we) && we.Exit == exitcode.ExitRefused:
			so.Outcome, so.Dirty, so.Unmerged = OutcomeRetained, rr.Dirty, rr.Unmerged
			res.Retained = true
		case rerr != nil:
			return res, rerr
		default:
			so.Outcome = OutcomeParked
			if wasParked {
				so.Outcome = OutcomeUnchanged
			} else {
				res.Changed = true
			}
			// A parked slot has no pane: a handle left behind reads as a
			// dead-tab to reconcile once the tab closes (as reclaim does).
			// A session that could not be killed keeps its handle.
			if err := editSlot(root, name, func(s *worker.Slot) error { s.Park(!sessionKept); return nil }); err != nil {
				return res, err
			}
			if claim != "" && issue != "" && be != nil {
				if _, err := be.Release(issue, claim); err != nil {
					res.Warnings = append(res.Warnings, fmt.Sprintf("release claim %s on %s: %v", claim, issue, err))
				}
			}
		}
		res.Slots = append(res.Slots, so)
	}

	if rep, err := e.Status(ctx, root); err == nil {
		res.Drift = len(rep.Findings)
	}

	// 3. Release the lease only when the base is green and every slot is parked.
	if res.Verdict != VerdictVerifyFailed && !res.Retained {
		res.Verdict = VerdictClean
		if _, err := le.Release(cd, le.Discover(o.HolderPID, o.Getenv)); err != nil {
			return res, err
		}
		// The round is over, so its host goes with the lease: the worker verbs
		// read work.dispatch again.
		if err := worker.Update(root, func(doc *worker.Doc) { doc.ClearHost() }); err != nil {
			return res, err
		}
		res.Changed = true
		return res, nil
	}
	if res.Verdict == "" {
		res.Verdict = VerdictHoldsWork
	}
	l := lease
	res.Lease = &l
	return res, nil
}

// requireBase refuses unless the project root is on the base with no tracked
// changes: the verify must read the base, not a half-edited tree.
func (e Env) requireBase(ctx context.Context, root string) error {
	res, err := e.Git(ctx, root, "rev-parse", "--abbrev-ref", "HEAD")
	cur, code := res.Stdout, res.ExitCode
	if err != nil || code != 0 {
		return &exitcode.Error{Exit: exitcode.ExitUnavailable, Message: "git rev-parse failed in " + root}
	}
	if strings.TrimSpace(cur) != e.Base {
		return &exitcode.Error{Exit: exitcode.ExitResolution, Message: fmt.Sprintf("the project root is on %s, not the base %s", strings.TrimSpace(cur), e.Base),
			Hint: "wind-down verifies the base: check it out in the project root first"}
	}
	res, err = e.Git(ctx, root, "status", "--porcelain", "--untracked-files=no")
	out, code := res.Stdout, res.ExitCode
	if err != nil || code != 0 {
		return &exitcode.Error{Exit: exitcode.ExitUnavailable, Message: "git status failed in " + root}
	}
	if strings.TrimSpace(out) != "" {
		return &exitcode.Error{Exit: exitcode.ExitResolution, Message: "the project root has uncommitted changes to tracked files", Hint: "commit or stash them: wind-down verifies the base as it is"}
	}
	return nil
}

func tail(text string, n int) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
