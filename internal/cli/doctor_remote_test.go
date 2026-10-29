package cli

import (
	"context"
	"testing"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/backlog/trackertest"
	"github.com/l4ci/rota/internal/round"
)

func TestDoctorRemoteTrackerBuiltOnce(t *testing.T) {
	for _, tc := range []struct {
		name, cfg string
		want      int
	}{
		{"issues", issuesConfig, 1}, {"file", fileConfig, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := trackerProject(t, tc.cfg)
			d := testDeps()
			d.Getwd = func() (string, error) { return root, nil }
			fake := &trackertest.Fake{}
			calls := 0
			d.NewTracker = func(context.Context, string, any) (backlog.Tracker, error) {
				calls++
				return fake, nil
			}
			d.RoundEnvLocal = func(context.Context, string) round.Env {
				if tc.want == 0 {
					t.Fatal("file mode must not scan remote sources")
				}
				return round.Env{}
			}
			got := doctorInput(context.Background(), d)
			if calls != tc.want {
				t.Fatalf("tracker constructions = %d, want %d shared by remote scans", calls, tc.want)
			}
			if tc.want == 0 && (got.Labels != nil || len(got.StaleClaimWarnings) != 0) {
				t.Fatalf("file remote inputs = %+v", got)
			}
		})
	}
}
