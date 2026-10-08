package worker

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
