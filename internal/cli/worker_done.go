package cli

import (
	"flag"
	"fmt"
	"github.com/l4ci/rota/internal/stamp"
	"os"
	"slices"
	"strings"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/ledger"
	"github.com/l4ci/rota/internal/proof"
	"github.com/l4ci/rota/internal/shlex"
	"github.com/l4ci/rota/internal/strutil"
	"github.com/l4ci/rota/internal/worker"
)

// fastTierCheck is the check `rota proof record <ID> -- rota test run fast`
// writes: the tier run as one command, which the worker contract recommends.
const fastTierCheck = "rota test run fast"

// workerDone is `rota worker done <slot>`: the step before the PR. It refuses
// (exit 4) a slot whose item has no PASS proof row at the branch's current
// HEAD for test.fast, then marks the slot done.
func workerDone(fs *flag.FlagSet) RunFunc {
	base := fs.String("base", "", "ref {files} is diffed against (default: the base branch)")
	attempt := fs.String("attempt", "", "the attempt id from your brief; a stale one exits 4")
	outcome := fs.String("outcome", "", "how the work ended: "+strings.Join(worker.Outcomes, "|")+" (default: done, no summary)")
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

		cmds := c.deps().workerEnv().GateConfig(root).Tier("fast")
		if kind == worker.OutcomeFailed {
			// A worker that cannot deliver has nothing to prove: the summary is its record.
			d.Set("proofSkipped", true)
		} else if len(cmds) == 0 {
			c.Warn("test.fast is unset: the proof check is skipped for %s", slotName)
			d.Set("proofSkipped", true)
		} else {
			head, err := branchHead(c, root, sl.Branch())
			if err != nil {
				return Result{}, err
			}
			want := cmds
			if anyContains(cmds, filesPlaceholder) {
				// {files} is the slot branch's diff, whatever cwd this runs from.
				files, err := changedFiles(c, slotDir(sl, root), *base)
				if err != nil {
					return Result{}, err
				}
				quoted := make([]string, len(files))
				for i, f := range files {
					quoted[i] = shlex.QuoteAlways(f)
				}
				want = make([]string, len(cmds))
				for i, cmd := range cmds {
					want[i] = recordedCommand([]string{cmd}, strings.Join(quoted, " "))
				}
			}
			_, st, _, err := openProof(c, id)
			if err != nil {
				return Result{}, err
			}
			rows, _, err := proof.Show(st, id)
			if err != nil {
				return failAny(err)
			}
			missing := missingFastProof(rows, head, want)
			if len(missing) > 0 {
				d.Set("head", head)
				d.Set("missing", strList(missing))
				e := Refused("%s has no PASS proof row at %s for test.fast: %s", id, strutil.ShortSHA(head), strings.Join(missing, "; "))
				e.Hint = fmt.Sprintf("run `rota proof record %s -- %s` at the current HEAD, then `rota worker done %s` again", id, fastTierCheck, slotName)
				return Result{Data: d}, e
			}
			d.Set("head", head)
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
			if k, sum := s.Outcome(); kind == "" && (k == worker.OutcomePartial || k == worker.OutcomeFailed) {
				evidence = outcomeEvidence(k, sum, s.PR())
			}
			s.SetEvent(worker.EventDone, evidence, now)
			if s.State() == "done" {
				return
			}
			// The PR is opened after this verb, so the poll never sees the
			// busy-to-done edge that baselines the review cursor elsewhere.
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

// missingFastProof lists what the PASS rows at head leave unproven: nothing
// when a row ran the whole tier (`rota test run fast`), else each test.fast
// command without a PASS row of its own. The latest row for a check at head
// decides (proof.Proves): a FAIL after a PASS, or a row at another sha, proves
// nothing.
func missingFastProof(rows []proof.Row, head string, want []string) (missing []string) {
	// `proof record -- rota test run fast` stores the quoted multi-arg form;
	// `-- "rota test run fast"` stores the plain one. Both prove the tier.
	if proof.Proves(rows, head, fastTierCheck) || proof.Proves(rows, head, recordedCommand(strings.Fields(fastTierCheck), "")) {
		return nil
	}
	for _, w := range want {
		if !proof.Proves(rows, head, w) {
			missing = append(missing, w)
		}
	}
	return missing
}

// branchHead is the commit the slot's branch points at.
func branchHead(c *Ctx, root, branch string) (string, error) {
	res, err := git.Repo{Dir: root}.Run(c.Context(), "rev-parse", "--verify", "--quiet", branch+"^{commit}")
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
		if outcome == worker.OutcomePartial || outcome == worker.OutcomeFailed {
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
