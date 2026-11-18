package worker

import (
	"context"
	"errors"
	"fmt"
	"github.com/l4ci/rota/internal/landing"
	"strings"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/exitcode"
	gatepolicy "github.com/l4ci/rota/internal/gate"
	"github.com/l4ci/rota/internal/landfreeze"
	"github.com/l4ci/rota/internal/rotastate"
	"github.com/l4ci/rota/internal/stamp"
)

// FreezeOpts chooses one mutation. With none, Freeze returns the latest record.
type FreezeOpts struct {
	RepairIssue, RepairPR string
	Verify, Clear         bool
	Reason                string
}

// Freeze changes recovery state under the same lock as landing. Verification
// cannot clear a failure recorded by a concurrent landing.
func (e Env) Freeze(ctx context.Context, root string, o FreezeOpts) (r *landfreeze.Record, err error) {
	e = e.WithDefaults()
	if err := landfreeze.ValidateRepair(o.RepairIssue, o.RepairPR); err != nil {
		return nil, fail(exitcode.ExitUsage, err.Error())
	}
	mutate := o.RepairIssue != "" || o.RepairPR != "" || o.Verify || o.Clear
	if !mutate {
		r, err := landfreeze.Load(ctx, e.Git, root)
		if err != nil && !errors.Is(err, rotastate.ErrNotRepo) {
			return nil, fail(exitcode.ExitRefused, landfreeze.UnreadableLine(err))
		}
		return r, err
	}
	err = e.withLandLock(ctx, root, "freeze recovery", func(bool) error {
		return e.updateFreeze(ctx, root, func(rec *landfreeze.Record, readErr error) (*landfreeze.Record, error) {
			r = rec
			out, err := e.freezeRecovery(ctx, root, o, r, readErr)
			if err == nil {
				r = out
			}
			return out, err
		})
	})
	return r, err
}

// freezeRecovery applies one mutation to the freeze record and returns the
// record to save.
func (e Env) freezeRecovery(ctx context.Context, root string, o FreezeOpts, r *landfreeze.Record, readErr error) (*landfreeze.Record, error) {
	if readErr != nil {
		if !o.Clear {
			return nil, fail(exitcode.ExitRefused, landfreeze.UnreadableLine(readErr))
		}
		// Discard the damaged snapshot only through the audited clear path.
		r = &landfreeze.Record{Active: true, Base: "unknown"}
	}
	if r == nil || !r.Active {
		return nil, fail(exitcode.ExitResolution, "no active landing freeze")
	}
	if o.RepairIssue != "" || o.RepairPR != "" {
		r.RepairIssue, r.RepairPR = o.RepairIssue, o.RepairPR
		return r, nil
	}
	if o.Clear {
		if strings.TrimSpace(o.Reason) == "" {
			return nil, fail(exitcode.ExitUsage, "freeze clear requires --reason <text>")
		}
		// Prefer the frozen base over an unrelated worktree's HEAD. A
		// deleted base or damaged record still has an audited escape hatch.
		sha, code := "", 1
		if readErr == nil {
			sha, code = e.runGit(ctx, root, "rev-parse", "--verify", "--end-of-options", r.Base+"^{commit}")
		}
		if code != 0 {
			sha, code = e.runGit(ctx, root, "rev-parse", "HEAD")
		}
		if code != 0 {
			return nil, fail(exitcode.ExitResolution, "cannot resolve clearance HEAD")
		}
		note := o.Reason
		if readErr != nil {
			r.SHA = sha
			note += "; replaced unreadable record: " + readErr.Error()
		}
		if err := gatepolicy.Clear(root, config.AutonomyLevel(e.Config(root)), e.Now(), gatepolicy.FreezeClear, "rota round freeze", r.Base, gatepolicy.Confirm{Given: true, Note: note}, nil); err != nil {
			return nil, err
		}
		return e.markCleared(ctx, root, r, sha, o.Reason), nil
	}
	sha, err := e.recoveryHead(ctx, root, r.Base)
	if err != nil {
		return nil, err
	}
	in, err := e.GateInput(root)
	if err != nil {
		return nil, err
	}
	if msg := LedgerExpiry(in.ledger, e.Now()); msg != "" {
		return nil, fail(exitcode.ExitRefused, msg)
	}
	verify, onCI, msg, err := e.fullTier(ctx, root, "freeze-recovery", in)
	if err != nil {
		return nil, err
	}
	if msg != "" {
		return nil, fail(exitcode.ExitRefused, msg)
	}
	if !onCI && len(in.cfg.Full) == 0 {
		return nil, fail(exitcode.ExitRefused, "freeze recovery requires test.full; skipped verification cannot clear a freeze")
	}
	vr, err := verify(root)
	if err != nil {
		return nil, err
	}
	if !vr.OK() {
		return nil, fail(exitcode.ExitFailed, "base verification failed; freeze kept\n"+vr.detail())
	}
	vr, err = e.runE2E(ctx, in.cfg.E2E, root, in.ledger)
	if err == nil {
		err = e.recordFlakes(root, nil, nil, vr)
	}
	if err != nil {
		return nil, err
	}
	if !vr.OK() {
		return nil, fail(exitcode.ExitFailed, "base test.e2e failed; freeze kept\n"+vr.detail())
	}
	after, err := e.recoveryHead(ctx, root, r.Base)
	if err != nil {
		return nil, err
	}
	if after != sha {
		return nil, fail(exitcode.ExitRefused, "base moved during verification; freeze kept")
	}
	return e.markCleared(ctx, root, r, sha, "verified recovery"), nil
}

func (e Env) recoveryHead(ctx context.Context, root, base string) (string, error) {
	if branch, code := e.runGit(ctx, root, "rev-parse", "--abbrev-ref", "HEAD"); code != 0 || branch != base {
		return "", fail(exitcode.ExitRefused, "check out frozen base "+base+" before verifying recovery")
	}
	if dirty, code := e.runGit(ctx, root, "status", "--porcelain"); code != 0 || dirty != "" {
		return "", fail(exitcode.ExitRefused, "recovery requires a clean base checkout")
	}
	sha, code := e.runGit(ctx, root, "rev-parse", "HEAD")
	if code != 0 {
		return "", fail(exitcode.ExitResolution, "cannot resolve base HEAD")
	}
	name, err := e.Remote(root)
	if err != nil {
		return "", fail(exitcode.ExitRefused, err.Error())
	}
	if _, code := e.runGit(ctx, root, "remote", "get-url", name); code == 0 {
		if _, code := e.runGit(ctx, root, "fetch", name, base); code != 0 {
			return "", fail(exitcode.ExitRefused, "cannot fetch frozen base; freeze kept")
		}
		remote, code := e.runGit(ctx, root, "rev-parse", name+"/"+base)
		if code != 0 || remote != sha {
			return "", fail(exitcode.ExitRefused, "update the base checkout to "+name+"/"+base+" before verifying recovery")
		}
	}
	return sha, nil
}

// markCleared stamps r as cleared by the operator; updateFreeze saves it.
func (e Env) markCleared(ctx context.Context, root string, r *landfreeze.Record, sha, reason string) *landfreeze.Record {
	who, _ := e.runGit(ctx, root, "config", "user.email")
	if who == "" {
		who, _ = e.runGit(ctx, root, "config", "user.name")
	}
	if who == "" {
		who = "unknown"
	}
	r.Active = false
	r.ClearedBy, r.ClearedSHA, r.ClearedAt, r.Reason = who, sha, stamp.Format(e.Now()), reason
	return r
}

func (runner gateRun) stepFreeze(result landing.GateResult, issue, pr string) (struct{}, landing.GateResult, bool, error) {
	g := runner.operation(result, GateTarget{Issue: issue, PR: pr}, gateForgeResult{})
	finish := func(done bool, err error) (struct{}, landing.GateResult, bool, error) {
		return struct{}{}, *g.res, done, err
	}

	r, err := landfreeze.Load(g.ctx, g.e.Git, g.root)
	if err != nil {
		g.verdict(landing.GateFreeze, landfreeze.UnreadableLine(err), landfreeze.ClearHint)
		return finish(true, nil)
	}
	if r.Allows(g.target.Issue, g.pr) && (!r.Frozen() || r.Binds(g.o.Base)) {
		return finish(false, nil)
	}
	g.verdict(landing.GateFreeze, r.Line(), "designate a repair with rota round freeze --repair-issue <id> or --repair-pr <pr>, then verify recovery")
	return finish(true, nil)
}

func (g *gateRun) freezeFailure(vr VerifyResult) error {
	pr := g.pr
	if pr == "" {
		pr = g.branch
	}
	return g.e.freezeLandedFailure(g.ctx, g.root, g.o.Base, []string{pr}, vr.Failed)
}

// freezeLandedFailure is called only while the landing lock is held.
func (e Env) freezeLandedFailure(ctx context.Context, root, base string, prs, checks []string) error {
	return e.updateFreeze(ctx, root, func(r *landfreeze.Record, readErr error) (*landfreeze.Record, error) {
		if readErr != nil {
			return nil, readErr
		}
		if r == nil || !r.Active {
			r = &landfreeze.Record{Base: base, CreatedAt: stamp.Format(e.Now())}
		}
		r.Active = true
		r.SHA, _ = e.runGit(ctx, root, "rev-parse", "HEAD")
		if r.SHA == "" {
			return nil, fmt.Errorf("cannot record landing freeze: missing base SHA")
		}
		r.PRs = appendNew(r.PRs, prs...)
		r.Checks = appendNew(r.Checks, checks...)
		return r, nil
	})
}

func (runner gateRun) stepClearFreeze(result landing.GateResult) (struct{}, landing.GateResult, bool, error) {
	g := runner.operation(result, GateTarget{}, gateForgeResult{})
	finish := func(done bool, err error) (struct{}, landing.GateResult, bool, error) {
		return struct{}{}, *g.res, done, err
	}
	if g.o.CheckOnly || g.o.skipsVerify() || g.res.Verdict != landing.GatePass || g.res.VerifySkipped {
		return finish(false, nil)
	}
	return finish(false, g.e.clearVerifiedFreeze(g.ctx, g.root, g.o.Base))
}

func (e Env) clearVerifiedFreeze(ctx context.Context, root, base string) error {
	return e.updateFreeze(ctx, root, func(r *landfreeze.Record, readErr error) (*landfreeze.Record, error) {
		if readErr != nil || !r.Binds(base) {
			return nil, readErr
		}
		sha, code := e.runGit(ctx, root, "rev-parse", "HEAD")
		if code != 0 {
			return nil, fmt.Errorf("cannot resolve verified base SHA")
		}
		return e.markCleared(ctx, root, r, sha, "verified repair landing"), nil
	})
}

// updateFreeze is the one Path, Read, apply, Save sequence over the freeze
// record. The caller holds the land lock. fn sees the record (nil when none
// exists) and the read error, so a caller may tolerate a damaged record. It
// returns the record to save, or nil to leave the file alone; an unhandled
// read error is its to return.
func (e Env) updateFreeze(ctx context.Context, root string, fn func(r *landfreeze.Record, readErr error) (*landfreeze.Record, error)) error {
	p, err := landfreeze.Path(ctx, e.Git, root)
	if err != nil {
		return err
	}
	r, readErr := landfreeze.Read(p)
	out, err := fn(r, readErr)
	if err != nil || out == nil {
		return err
	}
	return landfreeze.Save(p, out)
}
