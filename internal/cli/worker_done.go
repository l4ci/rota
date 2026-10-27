package cli

import (
	"flag"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/l4ci/rota/internal/acceptance"
	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/proof"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// workerDone is `rota worker done <slot>`: the last step before ROTA-DONE, after the PR. It refuses
// (exit 4) a slot whose item has no PASS proof row at the branch's current
// HEAD for test.fast or whose completed outcome has unmet explicit acceptance
// criteria on tracker items the PR closes, then marks the slot done.
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
		req := worker.DoneRequest{Root: root, PoolRoot: poolRoot, Slot: slotName, Attempt: *attempt, Base: *base,
			Kind: kind, Summary: summary, Targeted: *targeted, RequireRed: *requireRed}
		res, err := worker.Done(req, doneDeps(c, root))
		if res == nil {
			return Result{}, err
		}
		for _, w := range res.Warnings {
			c.Warn("%s", w)
		}
		d := doneData(res)
		if err != nil {
			return Result{Data: d}, err
		}
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

// doneDeps wires worker.Done to the cli's proof store, backlog and {files}
// expansion, which hang off the invocation's context.
func doneDeps(c *Ctx, root string) worker.DoneDeps {
	var be backlog.Backend
	opened := false
	return worker.DoneDeps{
		Env:       c.deps().workerEnv().WithDefaults(),
		Accounts:  c.deps().WorkerAccounts(),
		TierForms: fastTierForms(),
		ExpandFiles: func(dir, base string, lines []string) ([]string, error) {
			return expandFiles(c, dir, base, shellLines(lines)...)
		},
		ChangedFiles: func(dir, base string) ([]string, error) { return changedFiles(c, dir, base) },
		ProofRows:    func(id string) ([]proof.Row, error) { return proofRows(c, id) },
		Acceptance: func(dir, branch string, ids []string) error {
			return doneAcceptance(c, root, dir, branch, ids)
		},
		// A backlog that cannot be read keeps every member.
		Closed: func(id string) bool {
			if !opened {
				be, _ = openBacklog(c, root, false, "")
				opened = true
			}
			if be == nil {
				return false
			}
			it, err := be.Get(id)
			return err == nil && it.Closed
		},
		RunTargeted: func(dir, command, base, head string, ids []string) (string, error) {
			return targetedProof(c, root, dir, command, base, head, ids)
		},
	}
}

// doneAcceptance uses the same coverage as plan pass and item show, but only
// explicit ids of tracker items the PR closes block completion, as at the gate.
// Check the whole bundle before refusing so the worker can mark every missing
// criterion in one pass. Non-closing slices and file items keep their flow.
func doneAcceptance(c *Ctx, root, dir, branch string, ids []string) error {
	be, err := openBacklog(c, root, false, "")
	if err != nil {
		_, err = backlogFail(err)
		return err
	}
	if !be.Capabilities().IssueIDs {
		return nil
	}
	var closes func(tracker.IssueKey) bool
	var missing, commands []string
	for _, id := range ids {
		it, err := be.Get(id)
		if err != nil {
			_, err = backlogFail(err)
			return err
		}
		if it.Closed {
			continue
		}
		body, _, err := be.Detail(id)
		if err != nil {
			_, err = backlogFail(err)
			return err
		}
		if !backlog.HasAcceptanceIDs(body) {
			continue
		}
		if closes == nil {
			closes, err = doneClosing(c, root, dir, branch)
			if err != nil {
				return err
			}
		}
		if !closes(it.IssueKey()) {
			continue
		}
		crits, _ := acceptance.Parse(body)
		wf, err := backlog.WorkflowOf(be)
		if err != nil {
			_, err = backlogFail(err)
			return err
		}
		cover, err := acceptanceCoverage(c, wf, id, &backlog.Status{Body: body})
		if err != nil {
			_, err = backlogFail(err)
			return err
		}
		for _, st := range cover {
			if st.Met || !slices.ContainsFunc(crits, func(c acceptance.Criterion) bool { return c.ID == st.ID }) {
				continue
			}
			missing = append(missing, fmt.Sprintf("[%s] %s: %s", id, st.ID, st.Text))
			key := it.ID
			if it.Number > 0 {
				key = "'#" + strconv.Itoa(it.Number) + "'" // Keep the item key out of shell comments.
			}
			commands = append(commands, fmt.Sprintf("`rota plan pass %s %s --proof '<sha:check>'`", key, st.ID))
		}
	}
	if len(missing) == 0 {
		return nil
	}
	e := Refused("unmet acceptance criteria: %s", strings.Join(missing, "; "))
	e.Hint = "report --outcome partial --summary-file <file> with each unmet criterion and its reason on the third line"
	if len(commands) > 0 {
		e.Hint = "mark met criteria with " + strings.Join(commands, "; ") + ", or " + e.Hint
	}
	return e
}

// doneClosing uses the issue source's closing convention, including keyed
// trackers on another forge. Like the gate, a branch with no PR closes nothing.
func doneClosing(c *Ctx, root, dir, branch string) (func(tracker.IssueKey) bool, error) {
	env := c.deps().workerEnv().WithDefaults()
	settings := env.GateConfig(root).Tracker
	f, err := env.Forge("", dir, settings)
	if err != nil {
		return nil, trackerErr(err)
	}
	prs, err := f.OpenPRs(c.Context())
	if err != nil {
		return nil, trackerErr(err)
	}
	for _, pr := range prs {
		if pr.Branch != branch {
			continue
		}
		issues, err := env.Issues(dir, settings)
		if err != nil {
			return nil, trackerErr(err)
		}
		src := tracker.ClosingSource{Body: pr.Body, Title: pr.Title, Branch: pr.Branch}
		return func(key tracker.IssueKey) bool { return issues.ClosesIssue(key, src) }, nil
	}
	return func(tracker.IssueKey) bool { return false }, nil
}

// proofRows is the proof rows of item id, errors mapped onto the exit table.
func proofRows(c *Ctx, id string) ([]proof.Row, error) {
	_, st, _, err := openProof(c, id)
	if err != nil {
		return nil, err
	}
	rows, _, err := proof.Show(st, id)
	if err != nil {
		_, err = failAny(err)
	}
	return rows, err
}

// doneData is the verb's data object. Keys appear in the order the sequence
// reaches them, which no two paths of one run contradict.
func doneData(r *worker.DoneResult) *jsonx.Object {
	d := jsonx.NewObject()
	d.Set("slot", r.Slot)
	d.Set("item", r.Item)
	d.Set("changed", r.Changed)
	if r.Unread > 0 {
		d.Set("unread", r.Unread)
	}
	if r.Dirty {
		d.Set("dirty", true)
	}
	if r.Unpushed {
		d.Set("unpushed", true)
	}
	if r.ProofSkipped {
		d.Set("proofSkipped", true)
	}
	if r.Targeted {
		d.Set("targeted", true)
		d.Set("proofCommand", r.ProofCommand)
	}
	if r.Head != "" {
		d.Set("head", r.Head)
	}
	if len(r.Missing) > 0 {
		d.Set("missing", strList(r.Missing))
	}
	if r.NoRedUnchecked {
		d.Set("noRedUnchecked", true)
	}
	if r.NoRed {
		d.Set("noRed", true)
	}
	return d
}

// targetedProof stands in for the test.fast proof of a small task whose brief
// names its own check: for each issue the slot holds, a PASS row for command at
// head is enough; a missing one is made by running the command here, niced and
// under the test lock, in the slot's worktree. The row is marked targeted. The
// gate still runs everything. A failing run refuses the done (exit 4).
func targetedProof(c *Ctx, root, dir, command, base, head string, ids []string) (string, error) {
	expanded, err := expandFiles(c, dir, base, []string{command})
	if err != nil {
		return "", err
	}
	command = expanded[0]
	ctx, stop := workerContext()
	defer stop()
	for _, id := range ids {
		_, st, _, err := openProof(c, id)
		if err != nil {
			return command, err
		}
		rows, _, err := proof.Show(st, id)
		if err != nil {
			_, err = failAny(err)
			return command, err
		}
		if proof.Proves(rows, head, command) {
			// The worker recorded the passing run itself: the row stays the
			// record, now marked targeted.
			if _, err := proof.Mark(st, id, head, command, "targeted=true"); err != nil {
				_, err = failAny(err)
				return command, err
			}
			continue
		}
		run, err := runProofCommand(c, ctx, root, dir, command, "worker done")
		if err != nil {
			return command, err
		}
		run.targeted = true
		result := "PASS"
		if run.code != 0 {
			result = "FAIL"
		}
		if _, _, err := proof.Add(st, root, id, c.deps().Today(), proof.AddOpts{Check: command, Result: result, Evidence: run.evidence(), Sha: head}); err != nil {
			_, err = failAny(err)
			return command, err
		}
		if run.code != 0 {
			e := Refused("%s: the targeted proof `%s` failed (exit %d)", id, command, run.code)
			e.Hint = "fix it, commit and push, then run `rota worker done` again"
			return command, e
		}
	}
	return command, nil
}

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
	if summary, err = worker.ParseSummary(string(b)); err != nil {
		return "", nil, Usage("%v", err)
	}
	return outcome, summary, nil
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
