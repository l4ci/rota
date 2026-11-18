package round

import (
	"context"
	"testing"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/worker"
)

func TestBranchNoPRWaitsForFinishedSlot(t *testing.T) {
	root := ""
	for _, tc := range []struct {
		name, recorded, live string
		doneEvent            bool // the worker reported done after its last dispatch
		want                 int
	}{
		{"working overrides recorded done", "done", "working", false, 0},
		{"busy", "busy", "busy", false, 0},
		{"blocked", "blocked", "blocked", false, 0},
		{"done after a done event", "busy", "done", true, 1},
		{"reassigned slot keeps the last turn's native done", "busy", "done", false, 0},
		{"idle after a done event", "busy", "idle", true, 1},
		{"reassigned slot keeps the last turn's native idle", "busy", "idle", false, 0},
		{"recorded busy without host", "busy", "", false, 0},
		{"recorded done without host", "done", "", false, 1},
		{"recorded idle without host", "idle", "", false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := slot(root, "kit", "kit/2179-work", func(o *jsonx.Object) {
				o.Set("state", tc.recorded)
				o.Set("task", "2179")
				o.Set("handle", "w1:t1")
				o.Set("activeAt", "2026-10-03T10:00:00Z")
				if tc.doneEvent {
					o.Set("event", jsonx.Obj("kind", "done", "ts", "2026-10-03T10:30:00Z"))
				}
			})
			e := Env{Forge: (&fakeRemote{}).asForge(), Git: func(_ context.Context, _ string, args ...string) (git.Result, error) {
				if len(args) > 0 && args[0] == "rev-list" {
					return git.Result{Stdout: "1"}, nil
				}
				return git.Result{ExitCode: 1}, nil
			}}
			r := &Row{Name: "kit", Issue: "2179", Branch: "kit/2179-work", HostState: tc.live, Registered: true}

			count := func(fs []Finding) int {
				n := 0
				for _, f := range fs {
					if f.Kind == BranchNoPR {
						n++
					}
				}
				return n
			}
			findings, _, notes := e.slotFindings(bg, root, []*Row{r}, map[string]*view{"kit": {base: "main"}}, map[string]*worker.Slot{"kit": s}, forgeState{ok: true})
			if len(notes) != 0 {
				t.Fatalf("diagnostics: %v", notes)
			}
			if got := count(findings); got != tc.want {
				t.Errorf("reports %d branch-no-pr findings, want %d: %+v", got, tc.want, findings)
			}
		})
	}
}
