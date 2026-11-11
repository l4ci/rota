package limits

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/proc"
)

func TestCodexQuotaReportDoesNotChangeAccountPick(t *testing.T) {
	ctx := context.Background()
	src := &fakeSource{logins: []string{"work", "other"}, picked: "meter-must-not-run", pickOK: true}
	reporter, ok := harness.LookupQuotaReporter(harness.Codex)
	if !ok {
		t.Fatal("missing quota reporter")
	}
	snapshot := reporter.ReadQuota(ctx, harness.QuotaRequest{Name: "work", Account: harness.Account{Home: "/test-work"}}, harness.QuotaOptions{
		Now: func() time.Time { return time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC) },
		Exchange: func(ctx context.Context, cmd proc.Cmd, talk func(io.Reader, io.Writer) error) error {
			return talk(strings.NewReader("{\"id\":1,\"result\":{}}\n{\"id\":2,\"result\":{\"account\":{\"type\":\"chatgpt\"}}}\n{\"id\":3,\"result\":{\"rateLimits\":{\"primary\":{\"usedPercent\":100,\"windowDurationMins\":10080,\"resetsAt\":1794326854}}}}\n"), io.Discard)
		},
	})
	if snapshot.Status != "available" || snapshot.Weekly == nil || *snapshot.Weekly.RemainingPercent != 0 {
		t.Fatalf("expected exhausted report, got %+v", snapshot)
	}
	got, ok := PickAccount(ctx, src, nil, harness.Codex, "", t0)
	if !ok || got != "work" || src.exclude != nil {
		t.Fatalf("a 100%% quota report must not cause meter selection/cooldown: pick=%q ok=%v meter-exclude=%v", got, ok, src.exclude)
	}
}
