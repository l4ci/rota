package round

import (
	"context"
	"fmt"
	"os"

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
		to, _, err := e.milestoneBase(ctx, root, e.Board, it)
		if err != nil {
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("base of %s not read: %v", s.Task(), err))
			continue
		}
		have, want := s.Base(), to
		if want == "" {
			want = s.HomeBase()
		}
		if want == "" || have == want {
			continue
		}
		rep.add(Finding{Kind: BaseDrift, Slot: s.Name(), Issue: s.Task(), to: to,
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
	if f.to != "" {
		if _, err := e.fetchBase(ctx, root, f.to); err != nil {
			return err
		}
	} else if res, err := e.Git(ctx, root, "fetch", "-q", "origin", want); err != nil || res.ExitCode != 0 {
		return fmt.Errorf("could not fetch origin/%s: %s", want, gitWhy(res.Stderr, err))
	}
	if err := e.Forge.PRSetBase(ctx, n, want); err != nil {
		return fmt.Errorf("could not retarget PR #%d onto %s: %w", n, want, err)
	}
	if err := editSlot(root, f.Slot, func(s *worker.Slot) error { s.RouteBase(f.to); return nil }); err != nil {
		return err
	}
	text := fmt.Sprintf("BASE %s\nThe base of your PR changed: it now targets %s. Before the PR merges: `git fetch origin %s`, then `git merge origin/%s` into your branch (a merge, never a rebase), resolve every conflict, re-run your targeted checks, record proof at the merged head, push and run `rota worker done %s` again.\n",
		s.PR(), want, want, want, f.Slot)
	if _, err := worker.PostMail(root, f.Slot, worker.MailBase, text, e.Worker.Now()); err != nil {
		return err
	}
	if s.PaneHandle() == "" {
		return nil // the mail waits in the inbox: `worker done` refuses while it is unread
	}
	body, err := os.CreateTemp("", "rota-base-relay-*")
	if err != nil {
		return err
	}
	defer os.Remove(body.Name())
	_, werr := body.WriteString(text)
	if cerr := body.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return werr
	}
	if _, err := e.Worker.Dispatch(ctx, root, worker.DispatchOpts{Slot: f.Slot, BodyFile: body.Name(), Relay: true}); err != nil {
		return fmt.Errorf("PR retargeted, but the relay failed: %w", err)
	}
	return nil
}
