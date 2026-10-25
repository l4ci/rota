package cli

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/backlog/trackertest"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/tui"
	"github.com/l4ci/rota/internal/worker"
)

func staleStatusDeps(t *testing.T) (string, *Deps) {
	t.Helper()
	root := trackerProject(t, `{"issues":{"source":"github"}}`)
	now := time.Date(2026, 10, 25, 11, 0, 0, 0, time.UTC)
	fake := &trackertest.Fake{Issues: []tracker.Issue{{Key: tracker.NumKey(7), Title: "Abandoned", State: "open", Labels: []string{"type:task", "claimed:mac-ben@mac"}, Comments: []tracker.Comment{{Body: "<!-- rota:claim mac-ben@3 -->\nClaimed by mac-ben@3", CreatedAt: now.Add(-26 * time.Hour)}}}}}
	d := testDeps()
	d.NewTracker = func(context.Context, string, any) (backlog.Tracker, error) { return fake, nil }
	d.RoundEnv = func(context.Context, string) round.Env {
		return round.Env{Forge: returnForge{fake}, Worker: round.FromWorkerEnv(worker.Env{Now: func() time.Time { return now }}), Round: roundcfg.Settings{ClaimStaleMinutes: 240}, Git: func(context.Context, string, ...string) (git.Result, error) { return git.Result{}, nil }}
	}
	return root, d
}

func TestRoundStatusNamesRemoteStaleClaim(t *testing.T) {
	root, d := staleStatusDeps(t)
	code, out, _ := rotaInWith(t, d, root, "--json", "round", "status")
	if code != 0 {
		t.Fatalf("status: exit %d: %s", code, out)
	}
	data := data(t, out)
	claims, ok := data["staleClaims"].([]any)
	if !ok || len(claims) != 1 {
		t.Fatalf("staleClaims = %v, want one finding naming remote holder/activity; data %v", data["staleClaims"], data)
	}
	finding := claims[0].(map[string]any)
	detail, _ := finding["detail"].(string)
	if finding["kind"] != "stale-claim" || finding["issue"] != "7" || !strings.Contains(detail, "mac-ben@3") || !strings.Contains(detail, "2026-10-24T09:00:00Z") {
		t.Fatalf("stale finding = %v", finding)
	}
	if _, ok := finding["repair"]; ok {
		t.Fatalf("report-only finding has repair: %v", finding)
	}
	code, out, _ = rotaInWith(t, d, root, "round", "status")
	if code != 0 || !strings.Contains(out, "stale-claim\t"+detail) {
		t.Fatalf("status text: exit %d: %s", code, out)
	}
}

func TestRoundStatusViewNamesRemoteStaleClaim(t *testing.T) {
	root, d := staleStatusDeps(t)
	d.Getwd = func() (string, error) { return root, nil }
	snap, err := loadRoundSnap(context.Background(), &Ctx{Deps: d, Stderr: io.Discard}, root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(snap.Notes, "\n"), "claim mac-ben@3 on #7 has had no push or PR activity since 2026-10-24T09:00:00Z") {
		t.Fatalf("status notes omitted stale finding: %v", snap.Notes)
	}
	frame := tui.Strip(newRoundScreen(snap, nil, d.Now, roundRefresh).Render(200, 40, tui.Style{}))
	if !strings.Contains(frame, "mac-ben@3") || !strings.Contains(frame, "2026-10-24T09:00:00Z") {
		t.Fatalf("status view omitted remote holder/activity:\n%s", frame)
	}
}

func TestRoundStatusOmitsEmptyStaleClaims(t *testing.T) {
	root, d := staleStatusDeps(t)
	original := d.RoundEnv
	d.RoundEnv = func(ctx context.Context, root string) round.Env {
		env := original(ctx, root)
		env.Round.ClaimStaleMinutes = 0
		return env
	}
	code, out, _ := rotaInWith(t, d, root, "--json", "round", "status")
	if code != 0 {
		t.Fatalf("status: exit %d: %s", code, out)
	}
	if _, ok := data(t, out)["staleClaims"]; ok {
		t.Fatalf("disabled stale scan adds a field: %s", out)
	}
}
