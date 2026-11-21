package round

import "github.com/l4ci/rota/internal/worker"

// MarkMergedDone records on each done slot whether the forge shows its PR
// merged, so the prompt digest and the Stop hook stop asking for a slot that
// waits on nothing (#1843). A slot absent from merged is left as it is, an
// unchanged flag is not rewritten, and the first write error is returned after
// every slot has been tried. NeedsYou reads the flag this writes.
func MarkMergedDone(root string, merged map[string]bool) error {
	var first error
	for _, s := range worker.LoadRegistryTolerant(root).Slots() {
		m, ok := merged[s.Name()]
		if !ok || !worker.IsDone(s.State()) || s.PRMerged() == m {
			continue
		}
		if _, err := worker.UpdateSlot(root, s.Name(), func(s *worker.Slot) {
			if worker.IsDone(s.State()) {
				s.SetPRMerged(m)
			}
		}); err != nil && first == nil {
			first = err
		}
	}
	return first
}
