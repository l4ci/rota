package cli

import (
	"flag"
	"fmt"
	"github.com/l4ci/rota/internal/stamp"
	"os"
	"slices"
	"strings"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/ledger"
	"github.com/l4ci/rota/internal/proof"
	"github.com/l4ci/rota/internal/strutil"
	"github.com/l4ci/rota/internal/worker"
)

// workerDone is `rota worker done <slot>`: the last step before ROTA-DONE, after the PR. It refuses
// (exit 4) a slot whose item has no PASS proof row at the branch's current
// HEAD for test.fast, then marks the slot done.
func workerDone(fs *flag.FlagSet) RunFunc {
	base := fs.String("base", "", "ref {files} is diffed against (default: the base branch)")
	attempt := fs.String("attempt", "", "the attempt id from your brief; a stale one exits 4")
	outcome := fs.String("outcome", "", "how the work ended: "+strings.Join(worker.Outcomes, "|")+" (default: done, no summary)")
	requireRed := fs.Bool("require-red", false, "refuse (exit 4) instead of warning when work.tdd is on and non-test Go changed with no failing (RED) proof row, or the diff cannot be computed")
	targeted := fs.String("proof", "", "a targeted proof command for a small task: run at HEAD and recorded as the targeted proof row instead of test.fast")
	summaryFile := fs.String("summary-file", "", "three lines: what changed, what was found, what remains (required for partial and failed)")
	return func(c *Ctx, args []string) (Result, error) {
		slotName, err := oneArg(args, "slot")
		if err != nil {
			return Result{}, err
		}
		kind, summary, err := readOutcome(*outcome, *summaryFile)
		if err != nil {
			return Result{}, err
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		// The pool registry is gitignored and lives in the main checkout, so a
		// worker running this from its own worktree resolves it through the
		// git common dir. Config and proof stay with the nearest root.
		poolRoot, err := returnRoot(c, slotName)
		if err != nil {
			return Result{}, err
		}
		reg, err := worker.LoadRegistry(poolRoot)
		if err != nil {
			return Result{}, err
		}
		if !reg.Exists {
			return Result{}, Resolution("no worker pool — run rota worker pool init first")
		}
		sl := reg.Slot(slotName)
		if sl == nil {
			return Result{}, Resolution("slot '%s' is not in the pool", slotName)
		}
		if err := worker.FenceAttempt(sl, *attempt); err != nil {
			return Result{}, err
		}
		id := worker.HeldID(sl.Task(), sl.Branch(), sl.Name())
		if id == "" {
			return Result{}, Resolution("slot %s holds no item", slotName)
		}
		d := jsonx.NewObject()
		d.Set("slot", slotName)
		d.Set("item", id)
		d.Set("changed", false)
		// Mail first: a relay written after the last test run can change what
		// the worker must do, and the proof below would be of the old work.
		if err := unreadMailRefusal(sl); err != nil {
			d.Set("unread", len(sl.Unread()))
			return Result{Data: d}, err
		}

		// Done is the worker's last action: a late edit or commit after it moves
		// the head a review or the gate has already bound to.
		if kind != worker.OutcomeFailed {
			if err := settledBranch(c, slotDir(sl, root), sl.Branch(), d); err != nil {
				return Result{Data: d}, err
			}
		}

		cmds := c.deps().workerEnv().GateConfig(root).Tier("fast")
		if kind == worker.OutcomeFailed {
			// A worker that cannot deliver has nothing to prove: the summary is its record.
			d.Set("proofSkipped", true)
		} else if *targeted != "" {
			head, err := branchHead(c, root, sl.Branch())
			if err != nil {
				return Result{}, err
			}
			if err := targetedProof(c, root, sl, *targeted, *base, head, d); err != nil {
				return Result{Data: d}, err
			}
			d.Set("head", head)
		} else if len(cmds) == 0 {
			c.Warn("test.fast is unset: the proof check is skipped for %s", slotName)
			d.Set("proofSkipped", true)
		} else {
			head, err := branchHead(c, root, sl.Branch())
			if err != nil {
				return Result{}, err
			}
			// {files} is the slot branch's diff, whatever cwd this runs from.
			want, err := expandFiles(c, slotDir(sl, root), *base, shellLines(cmds)...)
			if err != nil {
				return Result{}, err
			}
			tierForms := fastTierForms()
			// A bundle is one change: every issue it closes needs its own proof.
			for _, bid := range openBundleIssues(c, root, sl) {
				_, st, _, err := openProof(c, bid)
				if err != nil {
					return Result{}, err
				}
				rows, _, err := proof.Show(st, bid)
				if err != nil {
					return failAny(err)
				}
				missing := worker.MissingFastProof(rows, head, tierForms, want)
				if len(missing) > 0 {
					d.Set("head", head)
					d.Set("missing", strList(missing))
					e := Refused("%s has no PASS proof row at %s for test.fast: %s", bid, strutil.ShortSHA(head), strings.Join(missing, "; "))
					e.Hint = fmt.Sprintf("run `rota proof record %s -- %s` at the current HEAD, then `rota worker done %s` again", bid, worker.FastTierCheck, slotName)
					return Result{Data: d}, e
				}
			}
			d.Set("head", head)
		}

		if kind != worker.OutcomeFailed {
			noRed, unchecked, err := missingRed(c, root, sl, *base)
			if err != nil {
				return Result{}, err
			}
			if unchecked != nil {
				d.Set("noRedUnchecked", true)
				msg := fmt.Sprintf("cannot check for a RED proof row: %v", unchecked)
				if *requireRed {
					e := Refused("%s", msg)
					e.Hint = "fix the base (--base <ref>) so the diff resolves, or drop --require-red to accept the warning"
					return Result{Data: d}, e
				}
				c.Warn("%s", msg)
			} else if noRed {
				d.Set("noRed", true)
				msg := fmt.Sprintf("%s changes non-test Go code and has no failing (RED) proof row: work.tdd wants a recorded red-first run (`rota proof record %s -- <test command>` before the change)", id, id)
				if *requireRed {
					e := Refused("%s", msg)
					e.Hint = "record the RED run, or drop --require-red to accept the warning"
					return Result{Data: d}, e
				}
				c.Warn("%s", msg)
			}
		}

		changed := false
		var fenced, mailErr error
		lateUnread := 0
		if err := worker.Update(poolRoot, func(doc *worker.Doc) {
			s := doc.Slot(slotName)
			if s == nil {
				return
			}
			// The check above read a snapshot: a reclaim and reassign since then
			// must not mark the new holder done.
			if fenced = worker.FenceAttempt(s, *attempt); fenced != nil {
				return
			}
			// Mail posted since the snapshot above must not be marked done over.
			if mailErr = unreadMailRefusal(s); mailErr != nil {
				lateUnread = len(s.Unread())
				return
			}
			now := stamp.Format(c.deps().Now())
			// The worker's own word, read ahead of the pane: a static pane with
			// no sentinel must not turn this slot back into idle.
			// A plain done reports no outcome and so answers none: only a relay,
			// a transfer or an explicit --outcome lifts a standing partial.
			if kind != "" {
				s.SetOutcome(kind, summary, now)
			}
			evidence := outcomeEvidence(kind, summary, s.PR())
			if k, sum := s.Outcome(); kind == "" && worker.OutcomeOpen(k) {
				evidence = outcomeEvidence(k, sum, s.PR())
			}
			s.SetEvent(worker.EventDone, evidence, now)
			if worker.IsDone(s.State()) {
				return
			}
			// The poll never sees the busy-to-done edge that baselines the
			// review cursor elsewhere: the worker reports done after its PR.
			s.BaselineReview(c.deps().Now())
			_ = s.MarkState("done", now)
			s.ClearSeen()
			changed = true
		}); err != nil {
			return Result{}, err
		}
		if fenced != nil {
			return Result{}, fenced
		}
		if mailErr != nil {
			d.Set("unread", lateUnread)
			return Result{Data: d}, mailErr
		}
		if changed {
			worker.LedgerDone(c.deps().workerEnv(), c.Context(), c.deps().WorkerAccounts(), poolRoot, ledger.Entry{Issue: id, Slot: slotName, Account: sl.Account(), Harness: sl.Kind(), PR: sl.PR()})
		}
		d.Set("changed", changed)
		text := fmt.Sprintf("%s\tdone", slotName)
		if kind != "" {
			d.Set("outcome", kind)
			block := outcomeBlock(kind, summary)
			d.Set("prBlock", block)
			text += "\t" + kind + "\n" + block
		}
		return Result{Data: d, Text: text}, nil
	}
}

// targetedProof stands in for the test.fast proof of a small task whose brief
// names its own check: for each issue the slot holds, a PASS row for command at
// head is enough; a missing one is made by running the command here, niced and
// under the test lock, in the slot's worktree. The row is marked targeted. The
// gate still runs everything. A failing run refuses the done (exit 4).
func targetedProof(c *Ctx, root string, sl *worker.Slot, command, base, head string, d *jsonx.Object) error {
	dir := slotDir(sl, root)
	expanded, err := expandFiles(c, dir, base, []string{command})
	if err != nil {
		return err
	}
	command = expanded[0]
	d.Set("targeted", true)
	d.Set("proofCommand", command)
	ctx, stop := workerContext()
	defer stop()
	for _, id := range openBundleIssues(c, root, sl) {
		_, st, _, err := openProof(c, id)
		if err != nil {
			return err
		}
		rows, _, err := proof.Show(st, id)
		if err != nil {
			_, err = failAny(err)
			return err
		}
		if proof.Proves(rows, head, command) {
			continue
		}
		run, err := runProofCommand(c, ctx, root, dir, command, "worker done")
		if err != nil {
			return err
		}
		run.targeted = true
		result := "PASS"
		if run.code != 0 {
			result = "FAIL"
		}
		if _, _, err := proof.Add(st, root, id, c.deps().Today(), proof.AddOpts{Check: command, Result: result, Evidence: run.evidence(), Sha: head}); err != nil {
			_, err = failAny(err)
			return err
		}
		if run.code != 0 {
			e := Refused("%s: the targeted proof `%s` failed (exit %d)", id, command, run.code)
			e.Hint = "fix it, commit and push, then run `rota worker done` again"
			return e
		}
	}
	return nil
}

// unreadMailRefusal is the exit-4 refusal of a done while the slot's inbox
// holds mail the worker has not read, nil when there is none.
func unreadMailRefusal(s *worker.Slot) error {
	n := len(s.Unread())
	if n == 0 {
		return nil
	}
	e := Refused("slot %s has %d unread mail: read it before done", s.Name(), n)
	e.Hint = fmt.Sprintf("run `rota worker inbox %s --ack`, act on what it says, then `rota worker done %s` again", s.Name(), s.Name())
	return e
}

// slotDir is the slot's worktree when it exists on disk, else fallback: the
// tree whose HEAD is the slot branch.
func slotDir(sl *worker.Slot, fallback string) string {
	if wt := sl.Worktree(); wt != "" {
		if fi, err := os.Stat(wt); err == nil && fi.IsDir() {
			return wt
		}
	}
	return fallback
}

// settledBranch refuses a worktree with uncommitted changes, and a branch
// whose pushed copy (origin/<branch>) is absent or not HEAD: the contract opens
// the PR, and so pushes, before done.
func settledBranch(c *Ctx, dir, branch string, d *jsonx.Object) error {
	repo := c.deps().Repo(dir)
	dirty, ok, err := repo.Dirty(c.Context())
	if err != nil {
		return gitErr(err)
	}
	if ok && dirty {
		d.Set("dirty", true)
		e := Refused("worktree %s has uncommitted changes: done is the last action", dir)
		e.Hint = "commit (or discard) them, push, record the proof at the new HEAD, then run `rota worker done` again"
		return e
	}
	remote, err := repo.Run(c.Context(), "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+branch)
	if err != nil {
		return gitErr(err)
	}
	if remote.ExitCode != 0 {
		d.Set("unpushed", true)
		e := Refused("%s was never pushed to origin: done is the last action, after the PR", branch)
		e.Hint = fmt.Sprintf("`git push origin %s` and open the PR, then run `rota worker done` again", branch)
		return e
	}
	head, err := repo.Run(c.Context(), "rev-parse", "HEAD")
	if err != nil {
		return gitErr(err)
	}
	if strings.TrimSpace(remote.Stdout) != strings.TrimSpace(head.Stdout) {
		d.Set("unpushed", true)
		e := Refused("%s has commits not pushed to origin/%s: done is the last action", branch, branch)
		e.Hint = fmt.Sprintf("`git push origin %s`, then run `rota worker done` again", branch)
		return e
	}
	return nil
}

// branchHead is the commit the slot's branch points at.
func branchHead(c *Ctx, root, branch string) (string, error) {
	res, err := c.deps().Repo(root).Run(c.Context(), "rev-parse", "--verify", "--quiet", branch+"^{commit}")
	if err != nil {
		return "", gitErr(err)
	}
	head := strings.TrimSpace(res.Stdout)
	if res.ExitCode != 0 || head == "" {
		return "", Resolution("branch %s of the slot does not exist", branch)
	}
	return head, nil
}

// maxSummaryLine caps one summary line: the three ride one event body.
const maxSummaryLine = 200

// readOutcome validates --outcome and --summary-file. No outcome and no file is
// a plain done, as before. partial and failed must say why, so they require
// the file; a file names three lines: what changed, what was found, what
// remains. kind is "" for a plain done with no summary.
func readOutcome(outcome, file string) (kind string, summary []string, err error) {
	if outcome != "" && !slices.Contains(worker.Outcomes, outcome) {
		return "", nil, Usage("--outcome must be one of %s, got %q", strings.Join(worker.Outcomes, ", "), outcome)
	}
	if file == "" {
		if worker.OutcomeOpen(outcome) {
			return "", nil, Usage("--outcome %s needs --summary-file: three lines naming what changed, what was found and what remains", outcome)
		}
		return outcome, nil, nil
	}
	if outcome == "" {
		outcome = worker.OutcomeDone
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return "", nil, Resolution("cannot read --summary-file: %v", err)
	}
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			summary = append(summary, l)
		}
	}
	for _, l := range summary {
		if strings.Contains(l, " | ") {
			return "", nil, Usage("--summary-file lines must not contain \" | \" (it joins them in the event): %q", l)
		}
		if len([]rune(l)) > maxSummaryLine {
			return "", nil, Usage("--summary-file lines are at most %d characters, got %d", maxSummaryLine, len([]rune(l)))
		}
	}
	if len(summary) != 3 {
		return "", nil, Usage("--summary-file must hold exactly three lines (what changed, what was found, what remains), got %d", len(summary))
	}
	return outcome, summary, nil
}

// outcomeEvidence is the done event's body: the PR, as before, or for a typed
// outcome "<outcome>: <line> | <line> | <line>", which `round wait` and
// `round status` show beside the slot state.
func outcomeEvidence(kind string, summary []string, pr string) string {
	if kind == "" {
		return pr
	}
	if len(summary) == 0 {
		return kind
	}
	return kind + ": " + strings.Join(summary, " | ")
}

// outcomeBlock is the first block of the PR body: the outcome and its three
// lines.
func outcomeBlock(kind string, summary []string) string {
	var b strings.Builder
	b.WriteString("Outcome: " + kind + "\n")
	for _, l := range summary {
		b.WriteString("- " + l + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// openBundleIssues is the slot's bundle with the closed members dropped. A
// backlog that cannot be read keeps every member.
func openBundleIssues(c *Ctx, root string, sl *worker.Slot) []string {
	ids := sl.BundleIssues()
	if len(ids) < 2 {
		return ids
	}
	be, err := openBacklog(c, root, false, "")
	if err != nil {
		return ids
	}
	return worker.OpenBundle(ids, func(id string) bool {
		it, err := be.Get(id)
		return err == nil && it.Closed
	})
}

// missingRed gathers the inputs for worker.MissingRed. unchecked is the diff
// error when the changed files could not be computed (an unresolvable base, no
// merge base, a git failure): the caller warns, or refuses under --require-red,
// rather than passing the done unchecked.
func missingRed(c *Ctx, root string, sl *worker.Slot, base string) (noRed bool, unchecked, err error) {
	tdd, err := c.deps().workerEnv().GateConfig(root).TDDEnabled()
	if err != nil || !tdd {
		return false, nil, err
	}
	files, derr := changedFiles(c, slotDir(sl, root), base)
	if derr != nil {
		return false, derr, nil
	}
	noRed, err = worker.MissingRed(files, func() []string { return openBundleIssues(c, root, sl) }, func(id string) ([]proof.Row, error) {
		_, st, _, err := openProof(c, id)
		if err != nil {
			return nil, err
		}
		rows, _, err := proof.Show(st, id)
		return rows, err
	})
	return noRed, nil, err
}
