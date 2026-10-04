package round

import (
	"context"
	"errors"
	"fmt"
	"github.com/l4ci/rota/internal/exitcode"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/roundlease"
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
	lease, st, err := le.Read(cd)
	if err != nil {
		return res, err
	}
	holder := le.Discover(o.HolderPID, o.Getenv)
	if (st != roundlease.Live && st != roundlease.Foreign) || !holder.SameAs(lease, le.Host) {
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
		cmds := verifyCommands(root)
		if len(cmds) == 0 {
			res.VerifySkipped = true
			res.Warnings = append(res.Warnings, "NO-VERIFY: refactor.verifyCommands is empty; the base was NOT checked by a command")
		} else {
			if err := e.requireBase(ctx, root); err != nil {
				return res, err
			}
			var log strings.Builder
			for _, c := range cmds {
				out, code := e.shell(ctx, root, c)
				log.WriteString("== " + c + "\n" + out)
				if code != 0 {
					res.Warnings = append(res.Warnings, "verify FAILED: "+c)
					res.Verdict = VerdictVerifyFailed
					continue
				}
				res.Verified = append(res.Verified, c)
			}
			res.VerifyLog = tail(log.String(), 20)
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
		name := worker.Str(s, "name")
		if !want[name] {
			continue
		}
		so := before[name]
		so.Name = name
		park := "park/" + name
		wasParked := worker.Str(s, "branch") == park && worker.Str(s, "task") == ""
		claim := worker.Str(s, "claimId")
		issue := heldID(worker.Str(s, "task"), worker.Str(s, "branch"), name)
		if !wasParked {
			so.Issue = firstNonEmpty(so.Issue, issue)
		} else {
			so.Issue = ""
		}
		// End the session before the checkout moves under it, but only for a
		// slot that will park: a slot holding work keeps its session.
		handle := worker.Str(s, "handle")
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
			if err := mutateSlot(root, name, func(s *jsonx.Object) {
				s.Set("task", nil)
				s.Set("claimId", nil)
				s.Set("kind", nil)
				s.Set("tier", nil)
				s.Set("model", nil)
				s.Set("tierReason", nil)
				s.Set("pr", nil)
				s.Set("state", "idle")
				// A parked slot has no pane: a handle left behind reads as a
				// dead-tab to reconcile once the tab closes (as reclaim does).
				// A session that could not be killed keeps its handle.
				if !sessionKept {
					s.Set("handle", nil)
				}
			}); err != nil {
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
		if _, err := le.Release(cd, holder); err != nil {
			return res, err
		}
		// The round is over, so its host goes with the lease: the worker verbs
		// read work.dispatch again.
		if err := worker.Update(root, slotsDefault(), func(doc *jsonx.Object) { doc.Delete("host") }); err != nil {
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

func verifyCommands(root string) []string {
	cfg := config.Load(filepath.Join(root, ".rota", "config.json"))
	v, ok := config.Lookup(cfg, "refactor.verifyCommands")
	if !ok {
		return nil
	}
	list, _ := v.([]any)
	var out []string
	for _, c := range list {
		if t := strings.TrimSpace(fmt.Sprint(c)); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// requireBase refuses unless the project root is on the base with no tracked
// changes: the verify must read the base, not a half-edited tree.
func (e Env) requireBase(ctx context.Context, root string) error {
	cur, _, code, err := e.Git(ctx, root, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil || code != 0 {
		return &exitcode.Error{Exit: exitcode.ExitUnavailable, Message: "git rev-parse failed in " + root}
	}
	if strings.TrimSpace(cur) != e.Base {
		return &exitcode.Error{Exit: exitcode.ExitResolution, Message: fmt.Sprintf("the project root is on %s, not the base %s", strings.TrimSpace(cur), e.Base),
			Hint: "wind-down verifies the base: check it out in the project root first"}
	}
	out, _, code, err := e.Git(ctx, root, "status", "--porcelain", "--untracked-files=no")
	if err != nil || code != 0 {
		return &exitcode.Error{Exit: exitcode.ExitUnavailable, Message: "git status failed in " + root}
	}
	if strings.TrimSpace(out) != "" {
		return &exitcode.Error{Exit: exitcode.ExitResolution, Message: "the project root has uncommitted changes to tracked files", Hint: "commit or stash them: wind-down verifies the base as it is"}
	}
	return nil
}

func (e Env) shell(ctx context.Context, dir, command string) (string, int) {
	if e.Worker.Shell != nil {
		return e.Worker.Shell(ctx, dir, command)
	}
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		code = 1
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		}
	}
	return string(out), code
}

func tail(text string, n int) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
