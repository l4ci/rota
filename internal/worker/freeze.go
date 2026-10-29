package worker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/exitcode"
	gatepolicy "github.com/l4ci/rota/internal/gate"
	"github.com/l4ci/rota/internal/landfreeze"
	"github.com/l4ci/rota/internal/rotastate"
)

const GateFreeze = "freeze"

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
		p, err := landfreeze.Path(ctx, e.Git, root)
		if err != nil {
			return err
		}
		r, err = landfreeze.Read(p)
		readErr := err
		if readErr != nil {
			if !o.Clear {
				return fail(exitcode.ExitRefused, landfreeze.UnreadableLine(readErr))
			}
			// Discard the damaged snapshot only through the audited clear path.
			r = &landfreeze.Record{Active: true, Base: "unknown"}
		}
		if r == nil || !r.Active {
			return fail(exitcode.ExitResolution, "no active landing freeze")
		}
		if o.RepairIssue != "" || o.RepairPR != "" {
			r.RepairIssue, r.RepairPR = o.RepairIssue, o.RepairPR
			return landfreeze.Save(p, r)
		}
		if o.Clear {
			if strings.TrimSpace(o.Reason) == "" {
				return fail(exitcode.ExitUsage, "freeze clear requires --reason <text>")
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
				return fail(exitcode.ExitResolution, "cannot resolve clearance HEAD")
			}
			note := o.Reason
			if readErr != nil {
				r.SHA = sha
				note += "; replaced unreadable record: " + readErr.Error()
			}
			if err := gatepolicy.Clear(root, e.Config(root), e.Now(), "freeze-clear", "rota round freeze", r.Base, gatepolicy.Confirm{Given: true, Note: note}, nil); err != nil {
				return err
			}
			return e.clearFreeze(ctx, root, p, r, sha, o.Reason)
		}
		sha, err := e.recoveryHead(ctx, root, r.Base)
		if err != nil {
			return err
		}
		in, err := e.GateInput(root)
		if err != nil {
			return err
		}
		if msg := LedgerExpiry(in.ledger, e.Now()); msg != "" {
			return fail(exitcode.ExitRefused, msg)
		}
		verify, onCI, msg, err := e.fullTier(ctx, root, "freeze-recovery", in)
		if err != nil {
			return err
		}
		if msg != "" {
			return fail(exitcode.ExitRefused, msg)
		}
		if !onCI && len(in.cfg.Full) == 0 {
			return fail(exitcode.ExitRefused, "freeze recovery requires test.full; skipped verification cannot clear a freeze")
		}
		vr, err := verify(root)
		if err != nil {
			return err
		}
		if !vr.OK() {
			return fail(exitcode.ExitFailed, "base verification failed; freeze kept\n"+vr.detail())
		}
		vr, err = e.runE2E(ctx, in.cfg.E2E, root, in.ledger)
		if err == nil {
			err = e.recordFlakes(root, nil, nil, vr)
		}
		if err != nil {
			return err
		}
		if !vr.OK() {
			return fail(exitcode.ExitFailed, "base test.e2e failed; freeze kept\n"+vr.detail())
		}
		after, err := e.recoveryHead(ctx, root, r.Base)
		if err != nil {
			return err
		}
		if after != sha {
			return fail(exitcode.ExitRefused, "base moved during verification; freeze kept")
		}
		return e.clearFreeze(ctx, root, p, r, sha, "verified recovery")
	})
	return r, err
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
	if _, code := e.runGit(ctx, root, "remote", "get-url", "origin"); code == 0 {
		if _, code := e.runGit(ctx, root, "fetch", "origin", base); code != 0 {
			return "", fail(exitcode.ExitRefused, "cannot fetch frozen base; freeze kept")
		}
		remote, code := e.runGit(ctx, root, "rev-parse", "origin/"+base)
		if code != 0 || remote != sha {
			return "", fail(exitcode.ExitRefused, "update the base checkout to origin/"+base+" before verifying recovery")
		}
	}
	return sha, nil
}

func (e Env) clearFreeze(ctx context.Context, root, p string, r *landfreeze.Record, sha, reason string) error {
	who, _ := e.runGit(ctx, root, "config", "user.email")
	if who == "" {
		who, _ = e.runGit(ctx, root, "config", "user.name")
	}
	if who == "" {
		who = "unknown"
	}
	r.Active = false
	r.ClearedBy, r.ClearedSHA, r.ClearedAt, r.Reason = who, sha, e.Now().UTC().Format(time.RFC3339), reason
	return landfreeze.Save(p, r)
}

func (g *gate) stepFreeze() (bool, error) {
	r, err := landfreeze.Load(g.ctx, g.e.Git, g.root)
	if err != nil {
		g.verdict(GateFreeze, landfreeze.UnreadableLine(err), landfreeze.ClearHint)
		return true, nil
	}
	if r.Allows(g.target.Issue, g.pr) && (!r.Frozen() || r.Binds(g.o.Base)) {
		return false, nil
	}
	g.verdict(GateFreeze, r.Line(), "designate a repair with rota round freeze --repair-issue <id> or --repair-pr <pr>, then verify recovery")
	return true, nil
}

func (g *gate) freezeFailure(vr VerifyResult) error {
	pr := g.pr
	if pr == "" {
		pr = g.branch
	}
	return g.e.freezeLandedFailure(g.ctx, g.root, g.o.Base, []string{pr}, vr.Failed)
}

// freezeLandedFailure is called only while the landing lock is held.
func (e Env) freezeLandedFailure(ctx context.Context, root, base string, prs, checks []string) error {
	p, err := landfreeze.Path(ctx, e.Git, root)
	if err != nil {
		return err
	}
	r, err := landfreeze.Read(p)
	if err != nil {
		return err
	}
	if r == nil || !r.Active {
		r = &landfreeze.Record{Base: base, CreatedAt: e.Now().UTC().Format(time.RFC3339)}
	}
	r.Active = true
	r.SHA, _ = e.runGit(ctx, root, "rev-parse", "HEAD")
	if r.SHA == "" {
		return fmt.Errorf("cannot record landing freeze: missing base SHA")
	}
	r.PRs = appendNew(r.PRs, prs...)
	r.Checks = appendNew(r.Checks, checks...)
	return landfreeze.Save(p, r)
}

func (g *gate) stepClearFreeze() error {
	if g.o.CheckOnly || g.o.skipsVerify() || g.res.Verdict != GatePass || g.res.VerifySkipped {
		return nil
	}
	return g.e.clearVerifiedFreeze(g.ctx, g.root, g.o.Base)
}

func (e Env) clearVerifiedFreeze(ctx context.Context, root, base string) error {
	p, err := landfreeze.Path(ctx, e.Git, root)
	if err != nil {
		return err
	}
	r, err := landfreeze.Read(p)
	if err != nil || !r.Binds(base) {
		return err
	}
	sha, code := e.runGit(ctx, root, "rev-parse", "HEAD")
	if code != 0 {
		return fmt.Errorf("cannot resolve verified base SHA")
	}
	return e.clearFreeze(ctx, root, p, r, sha, "verified repair landing")
}
