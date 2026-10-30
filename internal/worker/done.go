package worker

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/ledger"
	"github.com/l4ci/rota/internal/proof"
	"github.com/l4ci/rota/internal/stamp"
	"github.com/l4ci/rota/internal/strutil"
)

// DoneRequest is `rota worker done` with its flags parsed. Root is the nearest
// project root (config and proof); PoolRoot holds the gitignored registry.
type DoneRequest struct {
	Root, PoolRoot, Slot string
	// Attempt is the id from the worker's brief; "" applies no fence.
	Attempt string
	// Base is the ref {files} is diffed against; "" means the base branch.
	Base string
	// Kind and Summary are the validated outcome: "" for a plain done.
	Kind    string
	Summary []string
	// Targeted is a small task's own proof command, standing in for test.fast.
	Targeted string
	// RequireRed turns the missing-RED and unchecked-diff warnings into refusals.
	RequireRed bool
}

// DoneDeps is what done needs from outside package worker: the proof store, the
// backlog and {files} expansion all hang off the cli's context. Env carries git,
// the clock and config.
type DoneDeps struct {
	Env      Env
	Accounts *Accounts
	// TierForms are the rows `proof record -- rota test run fast` can store.
	TierForms []string
	// ExpandFiles turns test.fast lines into the shell lines that run and that
	// proof records, {files} expanded against base in dir.
	ExpandFiles func(dir, base string, lines []string) ([]string, error)
	// ChangedFiles lists dir's files that differ from base.
	ChangedFiles func(dir, base string) ([]string, error)
	// ProofRows reads the proof rows of item id.
	ProofRows func(id string) ([]proof.Row, error)
	// Closed reports whether a bundle member's item is closed; false when it
	// cannot be read.
	Closed func(id string) bool
	// Acceptance checks explicit criteria on tracker items this branch's PR
	// closes. Partial and failed reports skip it.
	Acceptance func(dir, branch string, ids []string) error
	// RunTargeted proves each of ids with command at head: a PASS row or a run.
	// It returns the expanded command, "" when expansion failed.
	RunTargeted func(dir, command, base, head string, ids []string) (string, error)
}

// DoneResult is what done reports, set field by field as the sequence gets
// there, so a refusal still carries the facts gathered before it. Warnings are
// for the caller to print.
type DoneResult struct {
	Slot, Item string
	// Changed is false when the slot was already done.
	Changed        bool
	Unread         int
	Dirty          bool
	Unpushed       bool
	ProofSkipped   bool
	Targeted       bool
	ProofCommand   string
	Head           string
	Missing        []string
	NoRedUnchecked bool
	NoRed          bool
	Warnings       []string
}

// Done is the worker's last action: the attempt fence, unread mail, a settled
// pushed branch, the test.fast (or targeted) proof at HEAD for every open
// bundle issue, acceptance marks for a completed outcome, the RED check, then
// the slot's done event, state and ledger row.
// A refusal returns the partial result beside the exit-4 error.
func Done(req DoneRequest, dp DoneDeps) (*DoneResult, error) {
	res := &DoneResult{}
	err := done(req, dp, res)
	var nd noData
	if errors.As(err, &nd) {
		return nil, nd.error
	}
	return res, err
}

// noData wraps a failure that happened before done had facts to report, or
// that the verb has always reported bare: Done returns no result with it.
type noData struct{ error }

func done(req DoneRequest, dp DoneDeps, res *DoneResult) error {
	env := dp.Env.WithDefaults()
	reg, err := LoadRegistry(req.PoolRoot)
	if err != nil {
		return noData{err}
	}
	if !reg.Exists {
		return noData{fail(exitcode.ExitResolution, "no worker pool — run rota worker pool init first")}
	}
	sl := reg.Slot(req.Slot)
	if sl == nil {
		return noData{fail(exitcode.ExitResolution, fmt.Sprintf("slot '%s' is not in the pool", req.Slot))}
	}
	if err := FenceAttempt(sl, req.Attempt); err != nil {
		return noData{err}
	}
	id := HeldID(sl.Task(), sl.Branch(), sl.Name())
	if id == "" {
		return noData{fail(exitcode.ExitResolution, fmt.Sprintf("slot %s holds no item", req.Slot))}
	}
	res.Slot, res.Item = req.Slot, id
	// Mail first: a relay written after the last test run can change what
	// the worker must do, and the proof below would be of the old work.
	if err := unreadMailRefusal(sl); err != nil {
		res.Unread = len(sl.Unread())
		return err
	}
	dir := doneDir(sl, req.Root)
	// Done is the worker's last action: a late edit or commit after it moves
	// the head a review or the gate has already bound to.
	if req.Kind != OutcomeFailed {
		remote, err := env.Remote(req.Root)
		if err != nil {
			return fail(exitcode.ExitRefused, err.Error())
		}
		if err := settledBranch(env, dir, remote, sl.Branch(), res); err != nil {
			return err
		}
	}
	bundle := func() []string { return OpenBundle(sl.BundleIssues(), dp.Closed) }
	if err := doneProof(env, req, dp, sl, dir, bundle, res); err != nil {
		return err
	}
	kind := req.Kind
	if kind == "" {
		kind, _ = sl.Outcome() // A plain done preserves a standing partial/failed.
	}
	if !OutcomeOpen(kind) && dp.Acceptance != nil {
		if err := dp.Acceptance(dir, sl.Branch(), bundle()); err != nil {
			return err
		}
	}
	if req.Kind != OutcomeFailed {
		if err := doneRed(env, req, dp, id, dir, bundle, res); err != nil {
			return err
		}
	}
	return markDone(env, req, dp, sl, id, res)
}

// doneProof gates the done on the proof at HEAD: none for a failed outcome, the
// targeted command for a small task, else test.fast for every open bundle issue.
func doneProof(env Env, req DoneRequest, dp DoneDeps, sl *Slot, dir string, bundle func() []string, res *DoneResult) error {
	cmds := env.GateConfig(req.Root).Tier("fast")
	switch {
	case req.Kind == OutcomeFailed:
		// A worker that cannot deliver has nothing to prove: the summary is its record.
		res.ProofSkipped = true
	case req.Targeted != "":
		head, err := branchHead(env, req.Root, sl.Branch())
		if err != nil {
			return noData{err}
		}
		command, err := dp.RunTargeted(dir, req.Targeted, req.Base, head, bundle())
		if command != "" {
			res.Targeted, res.ProofCommand = true, command
		}
		if err != nil {
			return err
		}
		res.Head = head
	case len(cmds) == 0:
		res.Warnings = append(res.Warnings, fmt.Sprintf("test.fast is unset: the proof check is skipped for %s", req.Slot))
		res.ProofSkipped = true
	default:
		head, err := branchHead(env, req.Root, sl.Branch())
		if err != nil {
			return noData{err}
		}
		// {files} is the slot branch's diff, whatever cwd this runs from.
		want, err := dp.ExpandFiles(dir, req.Base, cmds)
		if err != nil {
			return noData{err}
		}
		// A bundle is one change: every issue it closes needs its own proof.
		for _, bid := range bundle() {
			rows, err := dp.ProofRows(bid)
			if err != nil {
				return noData{err}
			}
			missing := MissingFastProof(rows, head, dp.TierForms, want)
			if len(missing) > 0 {
				res.Head, res.Missing = head, missing
				e := fail(exitcode.ExitRefused, fmt.Sprintf("%s has no PASS proof row at %s for test.fast: %s", bid, strutil.ShortSHA(head), strings.Join(missing, "; ")))
				e.Hint = fmt.Sprintf("run `rota proof record %s -- %s` at the current HEAD, then `rota worker done %s` again", bid, FastTierCheck, req.Slot)
				return e
			}
		}
		res.Head = head
	}
	return nil
}

// doneRed is the work.tdd check: non-test Go changed and no bundled issue holds
// a FAIL row. It warns, or refuses under RequireRed; a diff that cannot be
// computed is flagged the same way rather than passing unchecked.
func doneRed(env Env, req DoneRequest, dp DoneDeps, id, dir string, bundle func() []string, res *DoneResult) error {
	tdd, err := env.GateConfig(req.Root).TDDEnabled()
	if err != nil {
		return noData{err}
	}
	if !tdd {
		return nil
	}
	files, derr := dp.ChangedFiles(dir, req.Base)
	var noRed bool
	if derr == nil {
		if noRed, err = MissingRed(files, bundle, dp.ProofRows); err != nil {
			return noData{err}
		}
	}
	var msg, hint string
	switch {
	case derr != nil:
		res.NoRedUnchecked = true
		msg = fmt.Sprintf("cannot check for a RED proof row: %v", derr)
		hint = "fix the base (--base <ref>) so the diff resolves, or drop --require-red to accept the warning"
	case noRed:
		res.NoRed = true
		msg = fmt.Sprintf("%s changes non-test Go code and has no failing (RED) proof row: work.tdd wants a recorded red-first run (`rota proof record %s -- <test command>` before the change)", id, id)
		hint = "record the RED run, or drop --require-red to accept the warning"
	default:
		return nil
	}
	if req.RequireRed {
		e := fail(exitcode.ExitRefused, msg)
		e.Hint = hint
		return e
	}
	res.Warnings = append(res.Warnings, msg)
	return nil
}

// markDone records the done event and moves the slot to done under the registry
// lock, re-checking the fence and the inbox against the fresh document: a
// reclaim and reassign, or mail, since the snapshot must not be marked done over.
func markDone(env Env, req DoneRequest, dp DoneDeps, snap *Slot, id string, res *DoneResult) error {
	changed := false
	var fenced, mailErr error
	lateUnread := 0
	if err := Update(req.PoolRoot, func(doc *Doc) {
		s := doc.Slot(req.Slot)
		if s == nil {
			return
		}
		if fenced = FenceAttempt(s, req.Attempt); fenced != nil {
			return
		}
		if mailErr = unreadMailRefusal(s); mailErr != nil {
			lateUnread = len(s.Unread())
			return
		}
		now := stamp.Format(env.Now())
		// The worker's own word, read ahead of the pane: a static pane with
		// no sentinel must not turn this slot back into idle.
		// A plain done reports no outcome and so answers none: only a relay,
		// a transfer or an explicit --outcome lifts a standing partial.
		if req.Kind != "" {
			s.SetOutcome(req.Kind, req.Summary, now)
		}
		evidence := OutcomeEvidence(req.Kind, req.Summary, s.PR())
		if k, sum := s.Outcome(); req.Kind == "" && OutcomeOpen(k) {
			evidence = OutcomeEvidence(k, sum, s.PR())
		}
		s.SetEvent(EventDone, evidence, now)
		if IsDone(s.State()) {
			return
		}
		// The poll never sees the busy-to-done edge that baselines the
		// review cursor elsewhere: the worker reports done after its PR.
		s.BaselineReview(env.Now())
		_ = s.MarkState("done", now)
		s.ClearSeen()
		changed = true
	}); err != nil {
		return noData{err}
	}
	if fenced != nil {
		return noData{fenced}
	}
	if mailErr != nil {
		res.Unread = lateUnread
		return mailErr
	}
	if changed {
		LedgerDone(env, env.Ctx, dp.Accounts, req.PoolRoot, ledger.Entry{Issue: id, Slot: req.Slot, Account: snap.Account(), Harness: snap.Kind(), PR: snap.PR()})
	}
	res.Changed = changed
	return nil
}

// unreadMailRefusal is the exit-4 refusal of a done while the slot's inbox
// holds mail the worker has not read, nil when there is none.
func unreadMailRefusal(s *Slot) error {
	n := len(s.Unread())
	if n == 0 {
		return nil
	}
	e := fail(exitcode.ExitRefused, fmt.Sprintf("slot %s has %d unread mail: read it before done", s.Name(), n))
	e.Hint = fmt.Sprintf("run `rota worker inbox %s --ack`, act on what it says, then `rota worker done %s` again", s.Name(), s.Name())
	return e
}

// doneDir is the slot's worktree when it exists on disk, else fallback: the
// tree whose HEAD is the slot branch.
func doneDir(sl *Slot, fallback string) string {
	if wt := sl.Worktree(); wt != "" {
		if fi, err := os.Stat(wt); err == nil && fi.IsDir() {
			return wt
		}
	}
	return fallback
}

func doneGitErr(err error) error {
	if errors.Is(err, git.ErrNoGit) {
		return fail(exitcode.ExitUnavailable, "git is not installed")
	}
	return err
}

// settledBranch refuses a worktree with uncommitted changes, and a branch
// whose pushed copy (<remote>/<branch>) is absent or not HEAD: the contract opens
// the PR, and so pushes, before done.
func settledBranch(env Env, dir, remoteName, branch string, res *DoneResult) error {
	repo := git.Repo{Dir: dir, Runner: env.Git}
	dirty, ok, err := repo.Dirty(env.Ctx)
	if err != nil {
		return doneGitErr(err)
	}
	if ok && dirty {
		res.Dirty = true
		e := fail(exitcode.ExitRefused, fmt.Sprintf("worktree %s has uncommitted changes: done is the last action", dir))
		e.Hint = "commit (or discard) them, push, record the proof at the new HEAD, then run `rota worker done` again"
		return e
	}
	remote, err := repo.Run(env.Ctx, "rev-parse", "--verify", "--quiet", "refs/remotes/"+remoteName+"/"+branch)
	if err != nil {
		return doneGitErr(err)
	}
	if remote.ExitCode != 0 {
		res.Unpushed = true
		e := fail(exitcode.ExitRefused, fmt.Sprintf("%s was never pushed to %s: done is the last action, after the PR", branch, remoteName))
		e.Hint = fmt.Sprintf("`git push %s %s` and open the PR, then run `rota worker done` again", remoteName, branch)
		return e
	}
	head, err := repo.Run(env.Ctx, "rev-parse", "HEAD")
	if err != nil {
		return doneGitErr(err)
	}
	if strings.TrimSpace(remote.Stdout) != strings.TrimSpace(head.Stdout) {
		res.Unpushed = true
		e := fail(exitcode.ExitRefused, fmt.Sprintf("%s has commits not pushed to %s/%s: done is the last action", branch, remoteName, branch))
		e.Hint = fmt.Sprintf("`git push %s %s`, then run `rota worker done` again", remoteName, branch)
		return e
	}
	return nil
}

// branchHead is the commit the slot's branch points at.
func branchHead(env Env, root, branch string) (string, error) {
	res, err := git.Repo{Dir: root, Runner: env.Git}.Run(env.Ctx, "rev-parse", "--verify", "--quiet", branch+"^{commit}")
	if err != nil {
		return "", doneGitErr(err)
	}
	head := strings.TrimSpace(res.Stdout)
	if res.ExitCode != 0 || head == "" {
		return "", fail(exitcode.ExitResolution, fmt.Sprintf("branch %s of the slot does not exist", branch))
	}
	return head, nil
}
