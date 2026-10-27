package round

import (
	"testing"

	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/jsonx"
)

func TestBranchNoPRWaitsForFinishedSlot(t *testing.T) {
	root := newRepo(t, map[string]string{"kit": "kit/2179-work"}, "kit")
	for _, tc := range []struct {
		name, recorded, live string
		want                 int
	}{
		{"working overrides recorded done", "done", "working", 0},
		{"busy", "busy", "busy", 0},
		{"blocked", "blocked", "blocked", 0},
		{"done", "busy", "done", 1},
		{"idle", "busy", "idle", 1},
		{"recorded busy without host", "busy", "", 0},
		{"recorded done without host", "done", "", 1},
		{"recorded idle without host", "idle", "", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeRegistry(t, root, slot(root, "kit", "kit/2179-work", func(o *jsonx.Object) {
				o.Set("state", tc.recorded)
				o.Set("task", "2179")
				o.Set("handle", "w1:t1")
			}))
			e := env(nil, (&fakeRemote{labelled: []int{2179}}).asForge())
			if tc.live != "" {
				e = env([]host.Agent{{Tab: "w1:t1", Status: tc.live}}, e.Forge)
			}
			count := func(fs []Finding) int {
				n := 0
				for _, f := range fs {
					if f.Kind == BranchNoPR {
						n++
					}
				}
				return n
			}
			rep, err := e.Status(bg, root)
			if err != nil {
				t.Fatal(err)
			}
			out, err := e.Reconcile(bg, root, false)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("branch-no-pr: status=%d reconcile=%d", count(rep.Findings), count(out.Drift))
			if got := count(rep.Findings); got != tc.want {
				t.Errorf("status reports %d branch-no-pr findings, want %d", got, tc.want)
			}
			if got := count(out.Drift); got != tc.want {
				t.Errorf("reconcile reports %d branch-no-pr findings, want %d", got, tc.want)
			}
		})
	}
}
