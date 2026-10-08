package worker

import (
	"fmt"

	"github.com/l4ci/rota/internal/exitcode"
)

// RegisterExternal adds the hostless slot `rota worker adopt` creates: an
// external slot holding task on branch, with no session handle. The caller
// has checked that the name and branch are free.
func RegisterExternal(root, name, branch, worktree, base, task, pr string) error {
	return Update(root, func(d *Doc) {
		s := NewSlot(name, branch, worktree, base, "")
		s.MarkExternal(task, pr)
		d.AppendSlot(s)
		d.SortSlots()
	})
}

// ReleaseExternal unregisters the adopted slot name once its PR merged. The
// worktree and branch stay: rota did not create them. prune removes both, and
// leaves a dirty worktree (and with it the branch) in place. A slot a round
// drives is refused.
func (e Env) ReleaseExternal(root, name string, prune bool) error {
	e = e.withDefaults()
	s := LoadRegistry(root).Slot(name)
	if s == nil {
		return fail(exitcode.ExitResolution, fmt.Sprintf("slot '%s' is not in the pool", name))
	}
	if !s.IsExternal() {
		return fail(exitcode.ExitRefused, fmt.Sprintf("slot %s is not an adopted slot; release only unregisters adopted work", name))
	}
	wt, br := s.Worktree(), s.Branch()
	err := Update(root, func(d *Doc) {
		var keep []*Slot
		for _, x := range d.Slots() {
			if x.Name() != name {
				keep = append(keep, x)
			}
		}
		d.SetSlots(keep)
	})
	if err != nil || !prune {
		return err
	}
	if wt != "" {
		if out, code := e.git(root, "worktree", "remove", wt); code != 0 {
			return fail(exitcode.ExitRefused, fmt.Sprintf("released %s but kept %s: git worktree remove failed: %s", name, wt, out))
		}
	}
	if br != "" {
		if out, code := e.git(root, "branch", "-D", br); code != 0 {
			return fail(exitcode.ExitRefused, fmt.Sprintf("released %s but kept branch %s: %s", name, br, out))
		}
	}
	return nil
}
