package round

import (
	"context"
	"fmt"
	"time"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/milestone"
	"github.com/l4ci/rota/internal/worker"
)

// findBaseDrift reports each slot whose waiting PR targets a base the issue's
// milestone no longer routes it to: the issue was re-tagged to another active
// milestone, or its milestone left active. A slot is judged by what assign would
// choose now, so an issue assigned before its milestone became active is not
// drift until it is re-tagged. The repair retargets the PR and tells the worker.
func (e Env) findBaseDrift(ctx context.Context, root string, rep *Report, reg worker.Registry) {
	if e.Board == nil || e.Forge == nil {
		return
	}
	for _, s := range reg.Slots() {
		if s.IsExternal() || s.Task() == "" || s.PR() == "" || !worker.IsDone(s.State()) {
			continue
		}
		if _, ok := worker.PRRefNumber(s.PR()); !ok {
			continue
		}
		it, err := e.Board.Get(s.Task())
		if err != nil || it == nil || it.Closed {
			continue
		}
		// Drift is the issue's milestone tag changing since assign routed the
		// slot: a milestone going active under an unchanged tag is not (a slot
		// assigned while it was planned keeps its base). A slot assigned before
		// the tag was recorded is not judged.
		tag := it.Fields.Get("milestone")
		if was, ok := s.RoutedTag(); !ok || was == tag {
			continue
		}
		// Local comparison first; origin is asked only when it differs.
		id, err := e.milestoneTarget(root, e.Board, it)
		if err != nil {
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("base of %s not read: %v", s.Task(), err))
			continue
		}
		have, want := s.Base(), ""
		if id != "" {
			want = milestone.Branch(id)
		} else if want = s.HomeBase(); want == "" {
			want = have
		}
		if have == want {
			continue
		}
		to := ""
		if id != "" {
			var warn string
			if to, warn, err = e.milestoneBase(ctx, root, e.Board, it); err != nil {
				rep.Warnings = append(rep.Warnings, fmt.Sprintf("base of %s not read: %v", s.Task(), err))
				continue
			}
			if warn != "" {
				rep.Warnings = append(rep.Warnings, warn)
				continue
			}
			if to == "" { // no origin: nothing to retarget onto
				continue
			}
			want = to
		}
		rep.add(Finding{Kind: BaseDrift, Slot: s.Name(), Issue: s.Task(), to: to, tag: tag,
			Detail: fmt.Sprintf("PR %s targets %s, but %s now belongs on %s", s.PR(), have, s.Task(), want),
			Repair: "retarget the PR and tell the worker to merge " + want})
	}
}

// retarget moves the PR of f's slot onto its issue's current base, routes the
// slot there and tells the worker to merge that base in before the PR merges.
// The PR is edited first: a failed edit changes nothing else.
func (e Env) retarget(ctx context.Context, root string, f Finding) error {
	reg, err := worker.LoadRegistry(root)
	if err != nil {
		return err
	}
	s := reg.Slot(f.Slot)
	if s == nil {
		return fmt.Errorf("slot %s is not in the registry", f.Slot)
	}
	n, ok := worker.PRRefNumber(s.PR())
	if !ok {
		return fmt.Errorf("slot %s records no PR", f.Slot)
	}
	want := f.to
	if want == "" {
		want = s.HomeBase()
	}
	remote := config.GitRemote(e.Worker.Config(root))
	if f.to != "" {
		if _, err := e.fetchBase(ctx, root, f.to); err != nil {
			return err
		}
	} else if res, err := e.Git(ctx, root, "fetch", "-q", remote, want); err != nil || res.ExitCode != 0 {
		return fmt.Errorf("could not fetch %s/%s: %s", remote, want, gitWhy(res.Stderr, err))
	}
	if err := e.Forge.PRSetBase(ctx, n, want); err != nil {
		return fmt.Errorf("could not retarget PR #%d onto %s: %w", n, want, err)
	}
	// The slot leaves the gate set until its next `worker done`: the tick
	// gates done slots only, and the worker must merge the new base first.
	at := e.Worker.Now().UTC().Format(time.RFC3339)
	if err := editSlot(root, f.Slot, func(s *worker.Slot) error {
		s.RouteBase(f.to)
		s.SetRoutedTag(f.tag)
		return s.MarkState("busy", at)
	}); err != nil {
		return err
	}
	text := fmt.Sprintf("BASE %s\nThe base of your PR changed: it now targets %s. Before the PR merges: `git fetch %s %s`, then `git merge %s/%s` into your branch (a merge, never a rebase), resolve every conflict, re-run your targeted checks, record proof at the merged head, push and run `rota worker done %s` again.\n",
		s.PR(), want, remote, want, remote, want, f.Slot)
	if _, err := worker.PostMail(root, f.Slot, worker.MailBase, text, e.Worker.Now()); err != nil {
		return err
	}
	if s.PaneHandle() == "" {
		return nil // the mail waits in the inbox: `worker done` refuses while it is unread
	}
	path, cleanup, err := fsio.WriteTemp(e.Worker.Getenv("TMPDIR"), "rota-base-relay-*", []byte(text))
	if err != nil {
		return err
	}
	defer cleanup()
	if _, err := e.Worker.Dispatch(ctx, root, worker.DispatchOpts{Slot: f.Slot, BodyFile: path, Relay: true}); err != nil {
		return fmt.Errorf("PR retargeted, but the relay failed: %w", err)
	}
	return nil
}
