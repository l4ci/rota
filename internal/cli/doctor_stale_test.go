package cli

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/backlog/trackertest"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

type doctorStaleForge struct {
	returnForge
	err error
}

func (f doctorStaleForge) OpenPRs(context.Context) ([]tracker.PR, error) { return nil, f.err }

func TestDoctorStaleClaimsInput(t *testing.T) {
	for _, tc := range []struct {
		name, cfg   string
		unreachable bool
		want        int
	}{
		{"remote", `{"issues":{"source":"github"}}`, false, 1},
		{"unreachable", `{"issues":{"source":"github"}}`, true, 0},
		{"tracker unavailable", `{"issues":{"source":"github"}}`, false, 0},
		{"file mode", `{"issues":{"source":"file"}}`, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := trackerProject(t, tc.cfg)
			now := time.Date(2026, 10, 25, 11, 0, 0, 0, time.UTC)
			last := now.Add(-26 * time.Hour)
			fake := &trackertest.Fake{Issues: []tracker.Issue{{Key: tracker.NumKey(7), Title: "Abandoned", State: "open", Labels: []string{"type:task"}, Comments: []tracker.Comment{{Body: "<!-- rota:claim mac-ben@3 -->\nClaimed by mac-ben@3", CreatedAt: last}}}}}
			if tc.name == "tracker unavailable" {
				fake.Fail = map[string]error{"list": errors.New("tracker unavailable")}
			}
			d := testDeps()
			d.NewTracker = func(context.Context, string, any) (backlog.Tracker, error) { return fake, nil }
			d.RoundEnv = func(ctx context.Context, _ string) round.Env {
				if tc.name == "file mode" {
					t.Fatal("file mode must not reach the forge")
				}
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > 5*time.Second || time.Until(deadline) <= 0 {
					t.Fatalf("scan deadline = %v, want bounded by 5 seconds", deadline)
				}
				forge := doctorStaleForge{returnForge: returnForge{fake}}
				if tc.unreachable {
					forge.err = errors.New("forge unavailable")
				}
				return round.Env{Forge: forge, Worker: round.FromWorkerEnv(worker.Env{Now: func() time.Time { return now }}), Round: roundcfg.Settings{ClaimStaleMinutes: 240}, Git: func(context.Context, string, ...string) (git.Result, error) { return git.Result{}, nil }}
			}
			got := doctorStaleClaims(context.Background(), d, root)
			if len(got) != tc.want {
				t.Fatalf("stale rows = %+v, want %d", got, tc.want)
			}
			if tc.want > 0 && (got[0].Issue != "7" || got[0].Holder != "mac-ben@3" || !got[0].Last.Equal(last)) {
				t.Fatalf("stale row = %+v", got[0])
			}
		})
	}
}
