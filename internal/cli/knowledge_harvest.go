package cli

import (
	"errors"
	"flag"
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/blockedby"
	"github.com/l4ci/rota/internal/ledger"
	"github.com/l4ci/rota/internal/roundlease"
	"github.com/l4ci/rota/internal/worker"
)

// knHarvest lists the `learning` comments on the issues that merged in the
// round, minus the ones already marked. A support machine posts its learnings
// there instead of writing KNOWLEDGE.md; main folds each in and marks it with
// a `harvested` comment on the same issue naming the learning's comment id.
func knHarvest(fs *flag.FlagSet) RunFunc {
	mark := fs.String("mark", "", "mark the learning comment `id` harvested")
	round := fs.Int("round", -1, "the round number; default is the lease's round, else the highest in the ledger")
	return func(c *Ctx, args []string) (Result, error) {
		if err := knNoArgs(args); err != nil {
			return Result{}, err
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		if *mark != "" {
			if res, err := supportRefusal(c, root, "rota knowledge harvest --mark"); err != nil {
				return res, err
			}
		}
		if issue, err := issueBackend(c.deps(), root); err != nil {
			return Result{}, err
		} else if !issue {
			return refuseHarvest(*mark != "", blockedby.Backend, Refused("rota knowledge harvest reads issue comments (issues.source is file)"))
		}
		entries, err := ledger.Load(root)
		if err != nil {
			return Result{}, err
		}
		n := *round
		if n < 0 {
			n = currentRound(c, root, entries)
		}
		issues := mergedIssues(entries, n)
		// A learning ends up in KNOWLEDGE.md, which agents read as instructions:
		// only a comment by someone with write access counts, and so does a mark.
		trust, err := c.deps().workerEnv().WriterTrust(root)
		if errors.Is(err, worker.ErrNoPushTrust) {
			return refuseHarvest(*mark != "", blockedby.Forge, Refused("%s", err.Error()).WithHint("harvest needs GitHub: only GitHub can say whose learning comments to trust"))
		} else if err != nil {
			return Result{}, err
		}
		return withBackend(c, root, false, "", func(be backlog.Backend) (Result, error) {
			var rows []harvestRow
			for _, issue := range issues {
				learnings, marks, err := learningsAndMarks(be, trust, issue)
				if err != nil {
					return Result{}, err
				}
				done := map[string]bool{}
				for _, m := range marks {
					done[strings.TrimSpace(m.Text)] = true
				}
				var here []harvestRow
				for _, l := range learnings {
					here = append(here, harvestRow{issue: issue, comment: l, done: done[l.ID]})
				}
				// --mark stops at the issue holding the id.
				if *mark != "" {
					for _, r := range here {
						if r.comment.ID == *mark {
							return markHarvested(be, here, *mark)
						}
					}
					continue
				}
				rows = append(rows, here...)
			}
			if *mark != "" {
				return markHarvested(be, nil, *mark)
			}
			list := []any{}
			var lines []string
			for _, r := range rows {
				if r.done {
					continue
				}
				list = append(list, jsonObj("issue", r.issue, "commentId", r.comment.ID, "who", r.comment.Who, "text", r.comment.Text))
				lines = append(lines, fmt.Sprintf("- #%s · comment %s · %s", r.issue, r.comment.ID, strings.ReplaceAll(r.comment.Text, "\n", "\n  ")))
			}
			text := strings.Join(lines, "\n")
			if len(lines) == 0 {
				text = fmt.Sprintf("no unharvested learnings on issues merged in round %d", n)
			}
			return Result{Data: jsonObj("round", n, "learnings", list), Text: text}, nil
		})
	}
}

// refuseHarvest answers a refusal. The listing is read-only, so it exits 1 and
// its data carries no "changed"; --mark writes, so it keeps exit 4.
func refuseHarvest(writes bool, blockedBy string, e *Error) (Result, error) {
	if writes {
		return Result{Data: jsonObj("blockedBy", blockedBy, "changed", false)}, e
	}
	e.Exit = ExitFailed
	return Result{Data: jsonObj("blockedBy", blockedBy)}, e
}

// learningsAndMarks reads the issue's comments once and splits the `learning`
// and `harvested` ones, keeping only authors the forge says can push.
func learningsAndMarks(be backlog.Backend, trust *worker.MarkerTrust, issue string) (learnings, marks []backlog.Comment, err error) {
	all, err := be.Comments(issue, "")
	if err != nil {
		return nil, nil, err
	}
	for _, cm := range all {
		if cm.Kind != "learning" && cm.Kind != "harvested" {
			continue
		}
		if ok, err := trust.Writer(cm.Who); err != nil {
			return nil, nil, err
		} else if !ok {
			continue
		}
		if cm.Kind == "learning" {
			learnings = append(learnings, cm)
		} else {
			marks = append(marks, cm)
		}
	}
	return learnings, marks, nil
}

type harvestRow struct {
	issue   string
	comment backlog.Comment
	done    bool
}

func markHarvested(be backlog.Backend, rows []harvestRow, id string) (Result, error) {
	for _, r := range rows {
		if r.comment.ID != id {
			continue
		}
		if !r.done {
			if _, err := be.AddComment(r.issue, "harvested", id); err != nil {
				return backlogFail(err)
			}
		}
		return Result{Data: jsonObj("issue", r.issue, "commentId", id, "changed", !r.done), Text: fmt.Sprintf("marked comment %s on #%s harvested", id, r.issue)}, nil
	}
	return Result{}, Resolution("no learning comment %s on an issue merged in this round", id)
}

// mergedIssues is the distinct issues the round's ledger records as merged,
// in merge order.
func mergedIssues(entries []ledger.Entry, round int) []string {
	var out []string
	seen := map[string]bool{}
	for _, e := range entries {
		id := worker.NormID(e.Issue)
		if e.Kind != ledger.KindMerge || e.Round != round || id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// currentRound is the lease's round while one is live or foreign, else the
// highest round in the ledger.
func currentRound(c *Ctx, root string, entries []ledger.Entry) int {
	n := 0
	for _, e := range entries {
		n = max(n, e.Round)
	}
	env := c.deps().baseRoundEnv()
	if l, st, err := env.ReadLease(c.Context(), root); err == nil && (st == roundlease.Live || st == roundlease.Foreign) {
		n = l.Round
	}
	return n
}
