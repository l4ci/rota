package cli

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/ledger"
	"github.com/l4ci/rota/internal/proof"
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
	return func(c *Ctx, args []string) (Result, error) {
		slotName, err := oneArg(args, "slot")
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
		reg := worker.LoadRegistry(poolRoot)
		if !reg.Exists {
			return Result{}, Resolution("no worker pool — run rota worker pool init first")
		}
		sl := reg.Slot(slotName)
		if sl == nil {
			return Result{}, Resolution("slot '%s' is not in the pool", slotName)
		}
		id := worker.HeldID(sl.Task(), sl.Branch(), sl.Name())
		if id == "" {
			return Result{}, Resolution("slot %s holds no item", slotName)
		}
		d := jsonx.NewObject()
		d.Set("slot", slotName)
		d.Set("item", id)
		d.Set("changed", false)

		cmds := worker.TierCommands(root, "fast")
		if len(cmds) == 0 {
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
					quoted[i] = shellQuote(f)
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
				e := Refused("%s has no PASS proof row at %s for test.fast: %s", id, shortSha(head), strings.Join(missing, "; "))
				e.Hint = fmt.Sprintf("run `rota proof record %s -- %s` at the current HEAD, then `rota worker done %s` again", id, fastTierCheck, slotName)
				return Result{Data: d}, e
			}
			d.Set("head", head)
		}

		changed := false
		if err := worker.Update(poolRoot, func(doc *worker.Doc) {
			s := doc.Slot(slotName)
			if s == nil || s.State() == "done" {
				return
			}
			// The PR is opened after this verb, so the poll never sees the
			// busy-to-done edge that baselines the review cursor elsewhere.
			s.BaselineReview(time.Now())
			_ = s.MarkState("done", "")
			s.ClearSeen()
			changed = true
		}); err != nil {
			return Result{}, err
		}
		if changed {
			worker.LedgerDone(c.Context(), c.deps().WorkerAccounts(), poolRoot, ledger.Entry{Issue: id, Slot: slotName, Account: sl.Account(), Harness: sl.Kind(), PR: sl.PR()})
		}
		d.Set("changed", changed)
		return Result{Data: d, Text: fmt.Sprintf("%s\tdone", slotName)}, nil
	}
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
// command without a PASS row of its own. A FAIL row or a row at another sha
// proves nothing.
func missingFastProof(rows []proof.Row, head string, want []string) (missing []string) {
	passed := map[string]bool{}
	for _, r := range rows {
		if r.Result == "PASS" && atHead(r.Sha, head) {
			passed[r.Check] = true
		}
	}
	// `proof record -- rota test run fast` stores the quoted multi-arg form;
	// `-- "rota test run fast"` stores the plain one. Both prove the tier.
	if passed[fastTierCheck] || passed[recordedCommand(strings.Fields(fastTierCheck), "")] {
		return nil
	}
	for _, w := range want {
		if !passed[w] {
			missing = append(missing, w)
		}
	}
	return missing
}

// atHead reports whether a row's (abbreviated) sha names head.
func atHead(sha, head string) bool { return len(sha) >= 7 && strings.HasPrefix(head, sha) }

func shortSha(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
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
