package round

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/l4ci/rota/internal/blockedby"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/ledger"
	"github.com/l4ci/rota/internal/strutil"
	"github.com/l4ci/rota/internal/usagelimit"
	"github.com/l4ci/rota/internal/worker"
)

// CheckpointSubject is the subject of the commit a switch saves a slot's
// uncommitted work in.
const CheckpointSubject = "wip: checkpoint before switch"

// SwitchOpts is `rota round switch`: the slot, and the account and harness its
// next session runs under. Account "" picks the one with the most headroom;
// Kind "" keeps the slot's harness.
type SwitchOpts struct {
	Slot, Account, Kind, Model string
	// Note is appended to the brief.
	Note string
}

// Switched is what Switch did.
type Switched struct {
	Slot, Issue, Branch, Attempt             string
	Bundle                                   []string
	FromKind, ToKind, FromAccount, ToAccount string
	// Checkpoint is the sha7 of the checkpoint commit, "" when the worktree was clean.
	Checkpoint          string
	Dispatched, Changed bool
	Warnings            []string
}

// switchRun is what Switch settled before it touched anything.
type switchRun struct {
	slot      *worker.Slot
	id, wt    string
	remote    string
	placement *Placement // nil: the slot keeps its account (a repeated call)
	model     string
	brief     string
}

// Switch moves a slot that holds work onto another account, or with Kind onto
// another harness, in place: the issue, claim, attempt, bundle and branch stay.
// Every refusal comes first. Then the uncommitted work is committed by name as
// a checkpoint stamped with the slot's attempt and the branch is pushed (a
// failed push takes the commit back and leaves the worktree as found), the
// account is written to the slot, and the slot is dispatched afresh: the old
// session is killed before the new one starts. A dispatch that fails after the
// checkpoint is resumed by the same call with --account naming the new account.
func (e Env) Switch(ctx context.Context, root string, be Board, o SwitchOpts) (res Switched, err error) {
	res.Slot = o.Slot
	run, err := e.planSwitch(ctx, root, be, o, &res)
	if err != nil {
		return res, err
	}
	sha, err := e.checkpoint(ctx, run.wt, run.slot.Branch(), run.remote, run.slot.Attempt())
	if err != nil {
		return res, err
	}
	res.Checkpoint = sha
	res.Changed = sha != ""
	if run.placement != nil {
		if err := e.switchAccount(ctx, root, o.Slot, res.FromKind, res.ToKind, run.model, o.Kind != "", *run.placement); err != nil {
			return res, wrap(err)
		}
		res.Changed = true
	}
	if err := e.switchDispatch(ctx, root, run, &res, o.Note); err != nil {
		return res, err
	}
	res.Dispatched, res.Changed = true, true
	if lerr := e.Worker.Ledger().AppendErr(root, ledger.Entry{Kind: ledger.KindSwitch, Issue: run.id, Slot: o.Slot, Account: res.ToAccount, Harness: res.ToKind,
		Detail: ledger.SwitchDetail(res.FromAccount, res.FromKind, res.Checkpoint)}); lerr != nil {
		return res, fmt.Errorf("slot %s is switched to %s, but the round ledger row was not written: %w", o.Slot, res.ToAccount, lerr)
	}
	return res, nil
}

// planSwitch makes every refusal Switch can make and builds the brief.
func (e Env) planSwitch(ctx context.Context, root string, be Board, o SwitchOpts, res *Switched) (*switchRun, error) {
	if err := e.roundCfgErr(); err != nil {
		return nil, err
	}
	if o.Kind != "" && !harness.Valid(o.Kind) {
		return nil, usage("--kind must be one of %s", strings.Join(harness.Kinds(), ", "))
	}
	if o.Model != "" && !ValidModel(o.Model) {
		return nil, usage("--model %q is not a valid model id (letters, digits, . _ : / @ -)", o.Model)
	}
	if worker.IsSolo(root) {
		return nil, usage("round switch relaunches a slot's session; a solo round has none")
	}
	remote, err := e.remote(root)
	if err != nil {
		return nil, blocked(blockedby.Fetch, "%v", err)
	}
	if ok, err := e.holdsLease(ctx, root); err != nil {
		return nil, err
	} else if !ok {
		return nil, blocked(blockedby.NoRound, "this process holds no round lease: run rota round start first")
	}
	reg, err := worker.LoadRegistry(root)
	if err != nil {
		return nil, err
	}
	s := reg.Slot(o.Slot)
	if s == nil {
		return nil, &exitcode.Error{Exit: exitcode.ExitResolution, Message: fmt.Sprintf("slot %s is not in the pool", o.Slot)}
	}
	if err := fenceExternal(s, "switch", "switch its account"); err != nil {
		return nil, err
	}
	id := worker.NormID(s.Task())
	if id == "" || s.ClaimID() == "" {
		return nil, blocked(blockedby.NoSlotHolds, "slot %s holds no issue: there is nothing to switch; assign it instead", o.Slot)
	}
	res.Issue, res.Branch, res.Attempt, res.Bundle = id, s.Branch(), s.Attempt(), s.Bundle()
	if reg.BestOf(id) != nil {
		return nil, blocked(blockedby.BestOf, "%s is a best-of:2 issue: switch cannot move one attempt of a pair; pick, return or reclaim one first", id)
	}
	if err := e.refuseWhileGateRuns(ctx, root, "switch", o.Slot); err != nil {
		return nil, err
	}
	wt := s.Worktree()
	if !fsio.IsDir(wt) {
		return nil, &exitcode.Error{Exit: exitcode.ExitResolution, Message: fmt.Sprintf("slot %s worktree missing: %s", o.Slot, wt)}
	}
	if e.mergeInProgress(ctx, wt) {
		return nil, blocked(blockedby.MergeInProgress, "slot %s is in the middle of a merge: let its worker finish it, or abort it, before switching", o.Slot)
	}

	res.FromKind = harness.ResolveAccounts(s.Kind()).Kind()
	res.ToKind = res.FromKind
	if o.Kind != "" {
		res.ToKind = harness.ResolveAccounts(o.Kind).Kind()
	}
	res.FromAccount = s.AccountFor("")
	run := &switchRun{slot: s, id: id, wt: wt, remote: remote}
	if res.ToKind == res.FromKind && o.Account != "" && o.Account == res.FromAccount {
		// The slot is already on it: a repeated call, after a dispatch that failed.
		// Dispatch refuses a limited account too, so refuse it here, before the checkpoint.
		res.ToAccount = res.FromAccount
		if err := e.currentAccountLimit(ctx, root, o, res); err != nil {
			return nil, err
		}
	} else {
		p, err := e.switchPlacement(ctx, root, o, res)
		if err != nil {
			return nil, err
		}
		run.placement, res.ToAccount = &p, p.Account
	}

	run.model = o.Model
	if run.model == "" {
		run.model = s.Model()
		if res.ToKind != res.FromKind {
			run.model = e.Round.Model(res.ToKind, s.Tier())
		}
	}
	if !e.Worker.ModelAppliesTo(root, res.ToKind) {
		if o.Model != "" {
			return nil, blocked(blockedby.ModelLabel, "--model %s cannot apply: the %s launch command does not take {model}", o.Model, res.ToKind)
		}
		run.model = ""
	} else if run.model == "" && e.Worker.NeedsModel(root, res.ToKind) {
		return nil, blocked(blockedby.NoTierMap, "round.tiers.%s has no model for the %s tier: set round.tiers.%s.* or pass --model", res.ToKind, s.Tier(), res.ToKind)
	}
	warns, err := e.deliveryRefusals(ctx, root, res.ToKind, o.Slot, run.model, run.placement)
	if err != nil {
		return nil, err
	}
	res.Warnings = append(res.Warnings, warns...)
	run.brief, _, err = e.RebuildTaskBrief(ctx, root, be, worker.DispatchOpts{Slot: o.Slot, Task: id, Kind: res.ToKind, Model: run.model}, e.Round)
	if err != nil {
		return nil, err
	}
	return run, nil
}

// switchPlacement picks the account the slot moves to among the logins of the
// target harness: never the one it runs under now on the same harness, and
// with --account only that one. A named account at its limit is refused as
// limited; no usable account is the quota (or no-login) refusal.
func (e Env) switchPlacement(ctx context.Context, root string, o SwitchOpts, res *Switched) (Placement, error) {
	cfg := e.Worker.Config(root)
	home, _ := e.Worker.Home()
	all := harness.AccountsFor(cfg, res.ToKind, home).Entries
	var names []string
	for _, a := range all {
		names = append(names, a.Name)
	}
	list := slices.DeleteFunc(slices.Clone(all), func(a harness.AccountEntry) bool {
		return (o.Account != "" && a.Name != o.Account) || (res.ToKind == res.FromKind && a.Name == res.FromAccount)
	})
	switch {
	case len(all) == 0:
		return Placement{}, blocked(blockedby.Quota, "no %s account is configured in round.accounts: there is no other account to switch slot %s to", res.ToKind, o.Slot)
	case o.Account != "" && !slices.Contains(names, o.Account):
		return Placement{}, usage("--account %s is not a %s account in round.accounts (%s)", o.Account, res.ToKind, strings.Join(names, ", "))
	case len(list) == 0:
		return Placement{}, blocked(blockedby.Quota, "slot %s already runs on the only %s account, %s: name another harness with --kind", o.Slot, res.ToKind, res.FromAccount)
	}
	ms := e.readMeters(ctx, root, []string{res.ToKind})
	entries := e.poolEntries(ctx, ms, res.ToKind, list, e.Round, 0, e.accountLoad(root, e.Round, o.Slot))
	if o.Account != "" {
		if err := limitedRefusal(o.Account, entries[0]); err != nil {
			return Placement{}, err
		}
	}
	choice, ok := worker.ChoosePool(entries, nil, 0)
	if !ok {
		return Placement{}, noAccount(entries, "the "+res.ToKind+" harness", cfg)
	}
	return Placement{Kind: res.ToKind, Account: choice.Entry.Name, Headroom: choice.Entry.Meter.Headroom}, nil
}

// currentAccountLimit is the `limited` check for the account the slot already
// runs under, which a repeated call names instead of placing.
func (e Env) currentAccountLimit(ctx context.Context, root string, o SwitchOpts, res *Switched) error {
	cfg := e.Worker.Config(root)
	home, _ := e.Worker.Home()
	all := harness.AccountsFor(cfg, res.ToKind, home).Entries
	list := slices.DeleteFunc(slices.Clone(all), func(a harness.AccountEntry) bool { return a.Name != o.Account })
	if len(list) == 0 {
		return nil // not a configured login: Dispatch decides
	}
	ms := e.readMeters(ctx, root, []string{res.ToKind})
	return limitedRefusal(o.Account, e.poolEntries(ctx, ms, res.ToKind, list, e.Round, 0, e.accountLoad(root, e.Round, o.Slot))[0])
}

// limitedRefusal is the `limited` refusal for a named account whose pool entry
// is at its usage limit or cooling; nil otherwise.
func limitedRefusal(account string, pe worker.PoolEntry) error {
	if !strings.HasPrefix(pe.Skip, "limited") && pe.Meter.Verdict != usagelimit.VerdictCooling {
		return nil
	}
	why := "account " + account + " is at its usage limit"
	if pe.Meter.ResetsAt != nil {
		why += "; it resets at " + pe.Meter.ResetsAt.UTC().Format("2006-01-02 15:04 MST")
	} else if pe.Skip != "" {
		why += " (" + pe.Skip + ")"
	}
	return blocked(blockedby.Limited, "%s: pick another with --account, or leave it out", why)
}

// checkpoint commits the worktree's dirty paths by name, stamped with the
// attempt, and pushes the branch without force. It returns the commit's sha7,
// "" when there was nothing to commit (the branch is still pushed). A commit or
// push that fails puts HEAD and the index back as they were, and the worktree
// files are never touched.
func (e Env) checkpoint(ctx context.Context, wt, branch, remote, attempt string) (string, error) {
	cur, errOut, code := e.gitOut(ctx, wt, "symbolic-ref", "--short", "-q", "HEAD")
	if code > 1 {
		return "", unavailable("could not read HEAD in %s (git exit %d): %s", wt, code, errOut)
	}
	if branch == "" || cur != branch {
		return "", unavailable("worktree %s is on %q, not on the slot's branch %q: nothing was switched", wt, cur, branch)
	}
	dirty, err := e.dirtyPaths(ctx, wt)
	if err != nil {
		return "", err
	}
	sha := ""
	var index string
	if len(dirty) > 0 {
		// The index as it is, so a rollback restores what was staged and what was not.
		if index, errOut, code = e.gitOut(ctx, wt, "write-tree"); code != 0 {
			return "", unavailable("could not read the index in %s: %s", wt, errOut)
		}
		restore := func() { e.gitOut(ctx, wt, "read-tree", index) }
		if _, errOut, code := e.gitOut(ctx, wt, append([]string{"add", "--"}, dirty...)...); code != 0 {
			restore()
			return "", unavailable("could not stage the changes in %s: %s", wt, errOut)
		}
		msg := CheckpointSubject
		if attempt != "" {
			msg += "\n\n" + worker.AttemptTrailer + ": " + attempt
		}
		// --no-verify: a salvage of unfinished work must not wait on hooks that
		// check finished work.
		if _, errOut, code := e.gitOut(ctx, wt, "commit", "-q", "--no-verify", "-m", msg); code != 0 {
			restore()
			return "", unavailable("checkpoint commit in %s was rejected: %s", wt, errOut)
		}
		sha, _, _ = e.gitOut(ctx, wt, "rev-parse", "--short=7", "HEAD")
	}
	if _, errOut, code := e.gitOut(ctx, wt, "push", "-q", "-u", remote, branch); code != 0 {
		if sha == "" {
			return "", unavailable("git push %s %s failed, nothing was committed: %s", remote, branch, errOut)
		}
		// Soft reset puts HEAD back; read-tree puts the index back. Either can fail,
		// and then the worktree is not as found.
		_, resetErr, resetCode := e.gitOut(ctx, wt, "reset", "-q", "--soft", "HEAD~1")
		var treeErr string
		treeCode := 0
		if resetCode == 0 {
			_, treeErr, treeCode = e.gitOut(ctx, wt, "read-tree", index)
		}
		if resetCode != 0 || treeCode != 0 {
			return "", unavailable("git push %s %s failed (%s) and the rollback failed too: the checkpoint commit %s is still on the branch and the index may be changed (reset: %s; read-tree: %s); inspect `git status` in %s",
				remote, branch, strings.TrimSpace(errOut), sha, strings.TrimSpace(resetErr), strings.TrimSpace(treeErr), wt)
		}
		return "", unavailable("git push %s %s failed, worktree left as found: %s", remote, branch, errOut)
	}
	return sha, nil
}

// switchAccount writes the account to the slot and, on a harness change, its
// new kind and model, dropping the old harness's account so its home or
// config dir cannot reach the next launch.
func (e Env) switchAccount(ctx context.Context, root, slot, fromKind, toKind, model string, byFlag bool, p Placement) error {
	if toKind != fromKind {
		fromHz, err := worker.AccountHarness(fromKind)
		if err != nil {
			return err
		}
		if err := editSlot(root, slot, func(s *worker.Slot) error {
			s.SetAccountFor(fromHz.Kind(), "", "")
			return nil
		}); err != nil {
			return err
		}
	}
	hz, err := worker.AccountHarness(toKind)
	if err != nil {
		return err
	}
	if _, err := e.applyPlacement(ctx, root, slot, hz, p); err != nil {
		return err
	}
	return editSlot(root, slot, func(s *worker.Slot) error {
		source := s.KindSource()
		if byFlag {
			source = KindFromFlag
		}
		s.SetLaunch(toKind, source, model)
		return nil
	})
}

// switchDispatch relaunches the slot on the rebuilt brief plus what the
// switch did. A task dispatch resets the slot's PR and relay log for a new
// task; this is the same task, so both come back once it is sent.
func (e Env) switchDispatch(ctx context.Context, root string, run *switchRun, res *Switched, note string) error {
	s := run.slot
	pr, relays, seen := s.PR(), s.Relays(), s.ReviewSeen()
	text := run.brief + e.continuation(run, res, note)
	path, cleanup, err := fsio.WriteTemp(e.Worker.Getenv("TMPDIR"), "rota-round-brief-", []byte(text))
	if err != nil {
		return wrap(err)
	}
	defer cleanup()
	rnd := e.currentRound(ctx, root)
	if _, err := e.Worker.Dispatch(ctx, root, worker.DispatchOpts{Slot: res.Slot, BodyFile: path, Task: run.id, Round: &rnd,
		Branch: s.Branch(), Model: run.model, Kind: res.ToKind}); err != nil {
		return err
	}
	if err := editSlot(root, res.Slot, func(sl *worker.Slot) error {
		if pr != "" {
			sl.SetPR(pr)
		}
		if seen != "" {
			sl.SetReviewSeen(seen)
		}
		fresh := sl.Relays()
		sl.ResetRelays()
		sl.AppendRelays(relays)
		sl.AppendRelays(fresh)
		return nil
	}); err != nil {
		return wrap(err)
	}
	// The brief is the slot's signed message from the orchestrator: a PR may cite it.
	return wrap(worker.LogRelay(root, res.Slot, rnd, e.now(), text))
}

// continuation is the brief's tail: what the switch did and how to read the
// branch before going on.
func (e Env) continuation(run *switchRun, res *Switched, note string) string {
	s := run.slot
	base := run.remote + "/" + strutil.FirstNonEmpty(s.Base(), e.Base)
	from, to := accountLabel(res.FromAccount), accountLabel(res.ToAccount)
	var b strings.Builder
	fmt.Fprintf(&b, "\nThis is a switched session: the slot moved from %s on %s to %s on %s (rota round switch). You continue the same assignment, attempt and branch %s; the earlier session is gone.\n",
		res.FromKind, from, res.ToKind, to, s.Branch())
	if res.Checkpoint != "" {
		fmt.Fprintf(&b, "Its uncommitted work is checkpoint commit %s (`%s`), already pushed to %s/%s.\n", res.Checkpoint, CheckpointSubject, run.remote, s.Branch())
	} else {
		b.WriteString("The worktree had no uncommitted work, so there is no checkpoint commit.\n")
	}
	fmt.Fprintf(&b, "Before anything else, read the branch: `git log %s..HEAD`, `git diff %s...HEAD --stat`, `git status` and `rota proof show %s`. Proof rows recorded before the switch name an older HEAD: record them again.\n", base, base, run.id)
	if res.Checkpoint != "" {
		fmt.Fprintf(&b, "Then continue from the checkpoint, or drop it with `git revert --no-edit %s`: it is already pushed, so never reset and force-push.\n", res.Checkpoint)
	}
	if note = strings.TrimSpace(note); note != "" {
		b.WriteString("\nNote from the orchestrator:\n\n" + note + "\n")
	}
	return b.String()
}
