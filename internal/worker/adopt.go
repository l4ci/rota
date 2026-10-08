package worker

import (
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/exitcode"
)

// Conflict kinds of RegisterExternal.
const (
	ConflictName   = "name"
	ConflictBranch = "branch"
	ConflictIssue  = "issue"
)

// RegisterConflict is RegisterExternal's refusal: a slot already has the name,
// holds the branch or holds the task.
type RegisterConflict struct{ Kind, Slot string }

func (c *RegisterConflict) Error() string {
	return fmt.Sprintf("slot %s already has this %s", c.Slot, c.Kind)
}

// RegisterExternal adds the hostless slot `rota worker adopt` creates: an
// external slot holding task on branch, with no session handle. The name,
// branch and task are checked against the registry under its lock, so two
// concurrent adoptions cannot both win: a clash is a *RegisterConflict.
func RegisterExternal(root, name, branch, worktree, base, task, pr string) error {
	var conflict *RegisterConflict
	err := Update(root, func(d *Doc) {
		for _, x := range d.Slots() {
			switch {
			case x.Name() == name:
				conflict = &RegisterConflict{ConflictName, x.Name()}
			case x.Branch() == branch:
				conflict = &RegisterConflict{ConflictBranch, x.Name()}
			case task != "" && x.HeldID() == strings.ToUpper(task):
				conflict = &RegisterConflict{ConflictIssue, x.Name()}
			}
			if conflict != nil {
				return
			}
		}
		s := NewSlot(name, branch, worktree, base, "")
		s.MarkExternal(task, pr)
		d.AppendSlot(s)
		d.SortSlots()
	})
	if err == nil && conflict != nil {
		return conflict
	}
	return err
}

// BlockExternal is the blockedBy of a destructive verb pointed at an adopted
// slot: its worktree and branch are not rota's to move, reset or delete.
const BlockExternal = "external"

// ExternalRefusal is the exit-4 refusal of a verb that would move or delete
// the checkout of the adopted slot name.
func ExternalRefusal(name, verb string) error {
	e := fail(exitcode.ExitRefused, fmt.Sprintf("slot %s is an adopted external slot: %s would touch a worktree and branch rota did not create", name, verb))
	e.Data = BlockData{BlockedBy: BlockExternal}
	return e
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
