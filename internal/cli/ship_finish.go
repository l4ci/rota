package cli

import (
	"errors"
	"flag"
	"fmt"
	"slices"
	"strings"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/blockedby"
	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/cycle"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/repos"
	"github.com/l4ci/rota/internal/status"
)

const (
	finishedReason = "already finished"
	learnNudge     = "Capture learnings before context fades? Run `/rota-learn` — this cycle has the fresh session context."
)

// shipFinish is the bookkeeping tail of /rota-ship (Steps 7 to 9.5): drop the
// branch's status entry and handoff, complete the referenced items, and
// evaluate the post-cycle trigger gate. It never dispatches a skill.
func shipFinish(fs *flag.FlagSet) RunFunc {
	commitFlag := fs.String("commit", "", "merge commit to read the cycle from once the branch is gone; default HEAD")
	return func(c *Ctx, args []string) (Result, error) {
		if len(args) > 1 {
			return Result{}, Usage("ship finish takes at most one branch")
		}
		if c.deps().Getenv("ROTA_SLOT") != "" {
			return shipBlocked(blockedby.RoundWorker, "ship finish is the maintainer's bookkeeping: a round worker (ROTA_SLOT set) does not run it")
		}
		dir, err := gitDir(c)
		if err != nil {
			return Result{}, err
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		if repos.Umbrella(root) {
			return shipBlocked(blockedby.Umbrella, "ship finish does not run in an umbrella project")
		}
		ctx := c.Context()
		base, err := shipBase(c, dir)
		if err != nil {
			return Result{}, err
		}
		r := c.deps().Repo(dir)
		branch := ""
		if len(args) == 1 {
			branch = args[0]
		}
		if branch == "" {
			if branch, err = r.CurrentBranch(ctx); err != nil {
				return Result{}, gitErr(err)
			}
			if branch == "" {
				return Result{}, Resolution("no branch given and HEAD is not on a branch")
			}
		}
		if branch == base {
			return Result{}, Usage("name the finished branch: %s is the base branch", base)
		}

		// The cycle: the branch against the base while it exists, else the
		// merge commit M as M^1..M^2.
		t := branchTarget{Dir: dir, Branch: branch, Base: base, CorpusRoot: root}
		commit := ""
		exists, err := r.Verify(ctx, branch+"^{commit}")
		if err != nil {
			return Result{}, gitErr(err)
		}
		if exists {
			commit, err = finishRev(c, dir, "--short", branch)
		} else {
			m := *commitFlag
			if m == "" {
				m = "HEAD"
			}
			if ok, e := r.Verify(ctx, m+"^2^{commit}"); e != nil {
				return Result{}, gitErr(e)
			} else if !ok {
				return Result{}, Resolution("branch '%s' is gone and %s is not a merge commit", branch, m)
			}
			if t.Base, err = finishRev(c, dir, m+"^1"); err == nil {
				if t.Branch, err = finishRev(c, dir, m+"^2"); err == nil {
					commit, err = finishRev(c, dir, "--short", m)
				}
			}
		}
		if err != nil {
			return Result{}, err
		}
		info, err := reviewScan(c, t)
		if err != nil {
			return Result{}, err
		}

		// Status: the entry and the handoff note.
		removed, err := status.Remove(root, branch, "")
		if err != nil {
			return backlogFail(err)
		}
		swept, err := status.RemoveHandoff(root, branch, "")
		if err != nil {
			return backlogFail(err)
		}

		items, unproven, err := finishItems(c, root, info.IDs, commit)
		if err != nil {
			return backlogFail(err)
		}
		completed, _ := items.Get("completed")
		done := removed > 0 || swept || len(completed.([]string)) > 0

		sig := cycle.Signals{Items: len(info.IDs), Files: len(info.Files), DebugCycles: finishDebugCycles(c, info.IDs)}
		cfg := c.deps().Config(root)
		learn := finishGate(cycle.Gate(sig), done)
		learnObj := jsonx.NewObject()
		learnObj.Set("action", learn.Action)
		learnObj.Set("reason", learn.Reason)
		nudge := ""
		if learn.Action == "fire" {
			nudge = learnNudge
		}
		learnObj.Set("nudge", nudge)

		docs := finishGate(cycle.Gate(sig), done)
		if on, e := config.Bool(cfg, "docs.afterWork"); e != nil {
			return Result{}, &Error{Exit: ExitInternal, Message: e.Error()}
		} else if !on && done {
			docs = cycle.Decision{Action: "skip", Reason: "docs.afterWork off"}
		}
		docsObj := jsonx.NewObject()
		docsObj.Set("action", docs.Action)
		docsObj.Set("reason", docs.Reason)

		relObj, err := finishRelease(c, dir, done)
		if err != nil {
			return Result{}, err
		}

		st := jsonx.NewObject()
		st.Set("removed", removed)
		st.Set("handoffRemoved", swept)
		sg := jsonx.NewObject()
		sg.Set("items", sig.Items)
		sg.Set("files", sig.Files)
		sg.Set("debugCycles", sig.DebugCycles)
		autonomy := config.String(cfg, "autonomy.level")

		data := gitObj("branch", branch, "base", base, "commit", commit, "status", st, "items", items, "signals", sg,
			"learn", learnObj, "docs", docsObj, "release", relObj, "autonomy", autonomy, "changed", done)
		text := fmt.Sprintf("finished %s at %s: %d items, %d files; learn %s, docs %s, release %s",
			branch, commit, sig.Items, sig.Files, learn.Action, docs.Action, finishAction(relObj))
		if len(unproven) > 0 {
			data.Set("blockedBy", blockedby.ProofMissing)
			return Result{Data: data, Text: text}, Refused("no proof row for %s", strings.Join(unproven, ", ")).
				WithHint("record proof with `rota proof add`, then run `rota ship finish` again")
		}
		return Result{Data: data, Text: text}, nil
	}
}

// finishGate is the gate's decision, or skip once the bookkeeping had nothing to do.
func finishGate(d cycle.Decision, done bool) cycle.Decision {
	if !done {
		return cycle.Decision{Action: "skip", Reason: finishedReason}
	}
	return d
}

func finishAction(o *jsonx.Object) string {
	v, _ := o.Get("action")
	s, _ := v.(string)
	return s
}

// finishRev is `git rev-parse` of the last argument, with any flags before it.
func finishRev(c *Ctx, dir string, args ...string) (string, error) {
	out, err := reviewGit(c.Context(), c.deps(), dir, append([]string{"rev-parse"}, args...)...)
	return strings.TrimSpace(out), err
}

// finishItems completes the referenced items on the file backend, continuing
// past each; unproven lists the ones with no proof row.
func finishItems(c *Ctx, root string, ids []string, commit string) (*jsonx.Object, []string, error) {
	completed, already, unproven, unknown := []string{}, []string{}, []string{}, []string{}
	o := jsonx.NewObject()
	set := func(skipped any) {
		o.Set("referenced", append([]string{}, ids...))
		o.Set("completed", completed)
		o.Set("alreadyDone", already)
		o.Set("unproven", unproven)
		o.Set("unknown", unknown)
		o.Set("skipped", skipped)
	}
	issue, err := issueBackend(c.deps(), root)
	if err != nil {
		return nil, nil, err
	}
	if issue {
		set("issue backend")
		return o, nil, nil
	}
	be, err := openBacklog(c, root, false, "")
	if err != nil {
		return nil, nil, err
	}
	for _, id := range ids {
		changed, err := be.Complete(id, backlog.CompleteInput{Commit: commit, Date: todayDate(c), Reason: "done"})
		switch {
		case errors.Is(err, backlog.ErrProofMissing):
			unproven = append(unproven, id)
		case errors.Is(err, backlog.ErrNotFound):
			unknown = append(unknown, id)
		case err != nil:
			return nil, nil, err
		case changed:
			completed = append(completed, id)
		default:
			already = append(already, id)
		}
	}
	set(nil)
	return o, unproven, nil
}

// finishDebugCycles is the open debug session's hypothesis cycles when its bug
// is one of ids, else 0.
func finishDebugCycles(c *Ctx, ids []string) int {
	ctr, err := openCounter(c)
	if err != nil {
		return 0
	}
	st, err := ctr.State()
	if err != nil {
		return 0
	}
	bug, _ := st.Get("bug_id")
	if id, _ := bug.(string); id == "" || !slices.Contains(ids, id) {
		return 0
	}
	v, _ := st.Get("hypothesis_cycles")
	n, _ := jsonx.Int(v)
	return n
}

// finishRelease is the release nudge: fires when `rota release pending` has
// shouldNudge and a lastTag.
func finishRelease(c *Ctx, dir string, done bool) (*jsonx.Object, error) {
	o := jsonx.NewObject()
	action, reason, msg := "skip", finishedReason, ""
	if done {
		p, err := releasePendingData(c, dir)
		if err != nil {
			return nil, err
		}
		nudge, _ := p.Get("shouldNudge")
		tag, _ := p.Get("lastTag")
		m, _ := p.Get("message")
		why, _ := p.Get("reason")
		if n, _ := nudge.(bool); n && tag != "" {
			action, msg = "fire", fmt.Sprint(m)
			reason = fmt.Sprint(why)
		} else {
			reason = fmt.Sprint(why)
			if reason == "" {
				reason = "no release due"
			}
		}
	}
	o.Set("action", action)
	o.Set("reason", reason)
	o.Set("message", msg)
	return o, nil
}
