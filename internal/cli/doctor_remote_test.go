package cli

import (
	"context"
	"errors"
	"io"
	"strings"
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

// The round verbs and doctor open the claim board through claimBoard: a
// tracker that will not open is an error to the caller, file mode is no board.
func TestClaimBoardOpenErrorAndFileMode(t *testing.T) {
	d := testDeps()
	d.NewTracker = func(context.Context, string, any) (backlog.Tracker, error) { return nil, errors.New("tracker offline") }
	c := &Ctx{Deps: d, Stderr: io.Discard}
	if b, err := claimBoard(c, trackerProject(t, fileConfig), nil); b != nil || err != nil {
		t.Fatalf("file mode board = %v, %v; want neither", b, err)
	}
	root := trackerProject(t, issuesConfig)
	if _, err := claimBoard(c, root, nil); err == nil || !strings.Contains(err.Error(), "tracker offline") {
		t.Fatalf("open error = %v, want the tracker error", err)
	}
	env := withBoard(c, root, round.Env{})
	if env.Board != nil || len(c.warnings) != 1 || !strings.Contains(c.warnings[0], "claim check skipped") {
		t.Fatalf("withBoard board %v warnings %v, want a warning and no board", env.Board, c.warnings)
	}
}
