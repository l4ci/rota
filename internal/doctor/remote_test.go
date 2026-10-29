package doctor

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/backlog/trackertest"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

type remoteForge struct {
	tracker.Forge
	err error
}

func (f remoteForge) OpenPRs(context.Context) ([]tracker.PR, error) { return nil, f.err }

type remoteLabels struct {
	*trackertest.Fake
	definitions []tracker.Label
	listErr     error
	inspect     func(context.Context)
}

func (f *remoteLabels) ListLabels(ctx context.Context) ([]tracker.Label, error) {
	if f.inspect != nil {
		f.inspect(ctx)
	}
	return f.definitions, f.listErr
}

type remoteMetadata struct{ *remoteLabels }

func (f remoteMetadata) UpsertLabel(context.Context, tracker.Label) error {
	panic("doctor must not write labels")
}

var remoteNow = time.Date(2026, 10, 25, 11, 0, 0, 0, time.UTC)

func remoteFixture(t *testing.T, tr Tracker) (RemoteScanner, any) {
	t.Helper()
	cfg, err := jsonx.Decode([]byte(`{"issues":{"source":"github"}}`))
	if err != nil {
		t.Fatal(err)
	}
	return RemoteScanner{
		NewTracker: func(context.Context, string) (Tracker, error) { return tr, nil },
		NewForge:   func(context.Context) (tracker.Forge, error) { return remoteForge{}, nil },
		RoundEnv: func(context.Context) round.Env {
			return round.Env{Worker: round.FromWorkerEnv(worker.Env{Now: func() time.Time { return remoteNow }}), Round: roundcfg.Settings{ClaimStaleMinutes: 240}, Git: func(context.Context, string, ...string) (git.Result, error) { return git.Result{}, nil }}
		},
	}, cfg
}

func abandonedIssues() *trackertest.Fake {
	f := &trackertest.Fake{}
	for _, n := range []int{7, 9} {
		f.Issues = append(f.Issues, tracker.Issue{Key: tracker.NumKey(n), Title: "Abandoned", State: "open", Labels: []string{"type:task", "claimed:mac-ben@3"}, Comments: []tracker.Comment{{Body: "<!-- rota:claim mac-ben@3 -->\nClaimed by mac-ben@3", CreatedAt: remoteNow.Add(-26 * time.Hour)}}})
	}
	return f
}

func TestRemoteScanSharesTrackerAndDeadline(t *testing.T) {
	fake := abandonedIssues()
	labels := remoteMetadata{&remoteLabels{Fake: fake}}
	s, cfg := remoteFixture(t, labels)
	s.Timeout = 3 * time.Second
	var deadline time.Time
	calls := 0
	s.NewTracker = func(ctx context.Context, _ string) (Tracker, error) {
		calls++
		var ok bool
		deadline, ok = ctx.Deadline()
		if !ok || time.Until(deadline) > 3*time.Second || time.Until(deadline) <= 0 {
			t.Fatalf("scan deadline = %v", deadline)
		}
		return labels, nil
	}
	labels.inspect = func(ctx context.Context) {
		got, _ := ctx.Deadline()
		if got != deadline {
			t.Fatalf("label deadline %v differs from claim deadline %v", got, deadline)
		}
	}
	result := s.Scan(context.Background(), t.TempDir(), cfg)
	if calls != 1 {
		t.Fatalf("tracker constructions = %d, want 1", calls)
	}
	if len(result.StaleClaims) != 2 || len(result.Warnings) != 0 {
		t.Fatalf("scan = %+v", result)
	}
	for _, c := range result.StaleClaims {
		if c.Holder != "mac-ben@3" || !c.Last.Equal(remoteNow.Add(-26*time.Hour)) {
			t.Fatalf("claim = %+v", c)
		}
	}
	if result.Labels == nil || result.Labels.Problem != "" || !result.Labels.Metadata || !result.Labels.Used["claimed:mac-ben@3"] {
		t.Fatalf("labels = %+v", result.Labels)
	}
	for _, call := range fake.Calls {
		if call.Method != "list" && call.Method != "get" && call.Method != "comments" {
			t.Fatalf("remote scan wrote to tracker: %+v", call)
		}
	}
}

func TestRemoteScanDefaultBudget(t *testing.T) {
	s, cfg := remoteFixture(t, &trackertest.Fake{})
	base := s.RoundEnv
	s.RoundEnv = func(ctx context.Context) round.Env {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 5*time.Second || time.Until(deadline) <= 0 {
			t.Fatalf("default scan deadline = %v", deadline)
		}
		return base(ctx)
	}
	s.Scan(context.Background(), t.TempDir(), cfg)
}

func TestRemoteScanPartialClaims(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		t.Run(map[bool]string{false: "branch warning", true: "cancellation"}[timeout], func(t *testing.T) {
			s, cfg := remoteFixture(t, abandonedIssues())
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			base := s.RoundEnv
			s.RoundEnv = func(ctx context.Context) round.Env {
				e := base(ctx)
				reads := 0
				e.Git = func(context.Context, string, ...string) (git.Result, error) {
					reads++
					if reads == 2 {
						if timeout {
							cancel()
							return git.Result{}, context.DeadlineExceeded
						}
						return git.Result{}, errors.New("branch unavailable")
					}
					return git.Result{}, nil
				}
				return e
			}
			result := s.Scan(ctx, t.TempDir(), cfg)
			if len(result.StaleClaims) != 1 || result.StaleClaims[0].Issue != "7" {
				t.Fatalf("confirmed claims = %+v", result.StaleClaims)
			}
			want := "branch unavailable"
			if timeout {
				want = "deadline exceeded"
			}
			warnings := strings.Join(result.Warnings, "; ")
			if !strings.Contains(warnings, want) {
				t.Fatalf("warnings = %v, want %q", result.Warnings, want)
			}
			if timeout && strings.Count(warnings, "context canceled") != 1 {
				t.Fatalf("cancellation warning must appear once: %s", warnings)
			}
		})
	}
}

func TestRemoteScanFailures(t *testing.T) {
	for _, stage := range []string{"tracker", "round settings", "forge construction", "forge", "claims", "label definitions", "open labels"} {
		t.Run(stage, func(t *testing.T) {
			fake := abandonedIssues()
			labels := &remoteLabels{Fake: fake}
			s, cfg := remoteFixture(t, remoteMetadata{labels})
			offline := errors.New("offline")
			switch stage {
			case "tracker":
				s.NewTracker = func(context.Context, string) (Tracker, error) { return nil, offline }
			case "round settings":
				s.RoundEnv = func(context.Context) round.Env { return round.Env{RoundErr: offline} }
			case "forge construction":
				s.NewForge = func(context.Context) (tracker.Forge, error) { return nil, offline }
			case "forge":
				s.NewForge = func(context.Context) (tracker.Forge, error) { return remoteForge{err: offline}, nil }
			case "claims":
				fake.Fail = map[string]error{"list": offline}
			case "label definitions":
				labels.listErr = offline
			case "open labels":
				labels.inspect = func(context.Context) { fake.Fail = map[string]error{"list": offline} }
			}
			got := s.Scan(context.Background(), t.TempDir(), cfg)
			if stage == "label definitions" || stage == "open labels" {
				if len(got.StaleClaims) != 2 || got.Labels.Problem != "offline" {
					t.Fatalf("partial scan = %+v labels %+v", got, got.Labels)
				}
			} else {
				if len(got.StaleClaims) != 0 || !strings.Contains(strings.Join(got.Warnings, "; "), "offline") {
					t.Fatalf("incomplete scan = %+v", got)
				}
			}
			if stage == "tracker" || stage == "claims" {
				if got.Labels.Problem != "offline" {
					t.Fatalf("label failure = %+v", got.Labels)
				}
			}
		})
	}
}

func TestRemoteScanLabelCapabilities(t *testing.T) {
	for _, mode := range []string{"missing", "offline", "name only", "no listing"} {
		t.Run(mode, func(t *testing.T) {
			reader := &remoteLabels{Fake: &trackertest.Fake{}}
			var tr Tracker = remoteMetadata{reader}
			if mode == "offline" {
				reader.listErr = errors.New("offline")
			}
			if mode == "name only" {
				tr = reader
			}
			if mode == "no listing" {
				tr = reader.Fake
			}
			s, cfg := remoteFixture(t, tr)
			got := s.Scan(context.Background(), t.TempDir(), cfg)
			check := labelCheck(*got.Labels)
			want := Warn
			if mode != "missing" {
				want = Skip
			}
			if check.Status != want {
				t.Fatalf("label check = %+v, want %s", check, want)
			}
			if mode == "name only" && (!strings.Contains(check.Detail, "report-only") || check.Hint != "") {
				t.Fatalf("name-only check = %+v", check)
			}
		})
	}
}
