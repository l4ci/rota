package round

import (
	"testing"

	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/jsonx"
)

// A done slot whose recorded PR merged, then parked (the transfer to the
// human parks its worktree), must still report the PR as merged: the watch
// marks prMerged from this row, and the mark silences "(needs you)".
func TestStatusReadsRecordedPRStateOfParkedSlot(t *testing.T) {
	root := newRepo(t, map[string]string{"ben": "park/ben"})
	writeRegistry(t, root,
		slot(root, "ben", "park/ben", func(s *jsonx.Object) {
			s.Set("handle", "w1:t1")
			s.Set("state", "done")
			s.Set("task", "56")
			s.Set("pr", "https://github.com/o/r/pull/9")
		}),
	)
	forge := &fakeRemote{states: map[int]string{9: "merged"}, labelled: []int{56}}
	agents := []host.Agent{{Tab: "w1:t1", Name: "ben", Status: "done"}}
	rep, err := env(agents, forge.asForge()).Status(bg, root)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rep.Rows {
		if r.Name == "ben" && r.PRState != "merged" {
			t.Fatalf("parked done slot with merged PR #9: PRState = %q, want merged", r.PRState)
		}
	}
}
