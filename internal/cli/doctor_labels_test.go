package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/doctor"
	"github.com/l4ci/rota/internal/tracker"
)

func TestDoctorLabelsReadOnlyBoundedAndUnreachable(t *testing.T) {
	for _, offline := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "unreachable"}[offline], func(t *testing.T) {
			root := trackerProject(t, issuesConfig)
			fake := syncFixture()
			fake.definitions = nil
			fake.listContext = func(ctx context.Context) {
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > 5*time.Second || time.Until(deadline) <= 0 {
					t.Fatalf("unbounded scan: %v", deadline)
				}
			}
			if offline {
				fake.listErr = &tracker.Error{Kind: tracker.KindUnavailable, Message: "offline"}
			}
			state := doctorLabelInput(context.Background(), withTracker(t, fake), root)
			if state == nil {
				t.Fatal("no label input for issue backend")
			}
			report := doctor.Run(context.Background(), doctor.Input{Labels: state, Look: func(string) (string, bool) { return "", false }})
			found := false
			for _, check := range report.Checks {
				if check.Name != "labels" {
					continue
				}
				found = true
				want := doctor.Warn
				if offline {
					want = doctor.Skip
				}
				if check.Status != want || (offline && !strings.Contains(check.Detail, "offline")) {
					t.Fatalf("label check = %+v", check)
				}
			}
			wantCalls := 1
			if offline {
				wantCalls = 0
			}
			if !found || len(fake.upserts) != 0 || len(fake.Calls) != wantCalls {
				t.Fatalf("doctor did not stay read-only: found %v writes %+v calls %+v", found, fake.upserts, fake.Calls)
			}
		})
	}
	root := trackerProject(t, fileConfig)
	if state := doctorLabelInput(context.Background(), withTracker(t, issueFixture()), root); state != nil {
		t.Fatalf("file labels = %+v", state)
	}
}

func TestDoctorNameOnlyLabelsSkipDefinitionCheck(t *testing.T) {
	for _, source := range []string{"jira", "itsaplan"} {
		t.Run(source, func(t *testing.T) {
			root := trackerProject(t, `{"issues":{"source":"`+source+`"}}`)
			fake := syncFixture().labelSyncReader
			fake.definitions = nil
			state := doctorLabelInput(context.Background(), withTracker(t, fake), root)
			report := doctor.Run(context.Background(), doctor.Input{Labels: state, Look: func(string) (string, bool) { return "", false }})
			for _, check := range report.Checks {
				if check.Name == "labels" {
					if check.Status != doctor.Skip || !strings.Contains(check.Detail, "report-only") || check.Hint != "" {
						t.Fatalf("unfixable name-only notice: %+v", check)
					}
					return
				}
			}
			t.Fatal("no label check")
		})
	}
}
