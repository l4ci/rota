package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/bugreport"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/worker"
)

func TestRoundNeedsYouHumanReservations(t *testing.T) {
	root, deps := roundFixture(t, nil)
	write(t, worker.RegistryPath(root), `{"slots":[{"name":"question","task":"12","state":"blocked","event":{"kind":"question","body":"Which helper name?"}},{"name":"cap","task":"13","state":"done","pr":"#7"}],"prs":[{"issue":"14","pr":"#8"}],"bounces":{"13":3,"14":3}}`)
	original := deps.RoundEnv
	deps.RoundEnv = func(ctx context.Context, root string) round.Env {
		env := original(ctx, root)
		env.Round.MaxBounces = 3
		return env
	}
	code, out, stderr := rotaInWith(t, deps, root, "--json", "round", "status", "--needs-you")
	if code != 0 || len(data(t, out)["needsYou"].([]any)) != 0 {
		t.Errorf("worker questions and cap audits belong to the orchestrator: %d %s %s", code, out, stderr)
	}
	if err := worker.UpdateEscalations(root, func([]worker.Escalation) []worker.Escalation {
		return []worker.Escalation{{ID: "human", Kind: "pr", Number: 7, Title: "Approve a scope cut?", Status: "pending", Options: []string{"Keep scope", "Cut scope"}, Recommend: 1}}
	}); err != nil {
		t.Fatal(err)
	}
	code, out, stderr = rotaInWith(t, deps, root, "--json", "round", "status", "--needs-you")
	rows := data(t, out)["needsYou"].([]any)
	if code != 0 || len(rows) != 1 || rows[0].(map[string]any)["ask"] != "Approve a scope cut?" {
		t.Fatalf("only the routed human decision should remain: %d %s %s", code, out, stderr)
	}
}

func TestRoundNeedsYouReservedFilingAndReclaim(t *testing.T) {
	root, deps := staleStatusDeps(t)
	code, out, stderr := rotaInWith(t, deps, root, "--json", "round", "status", "--needs-you")
	rows := data(t, out)["needsYou"].([]any)
	if code != 0 || len(rows) != 1 || rows[0].(map[string]any)["kind"] != "stale-claim" {
		t.Errorf("reclaiming a stale remote claim needs human approval: %d %s %s", code, out, stderr)
	}
	root, deps = roundFixture(t, nil)
	if _, err := bugreport.Add(context.Background(), git.Exec, root, bugreport.Record{Source: bugreport.SourceAgent, Summary: "Wrong refusal"}, time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	code, out, stderr = rotaInWith(t, deps, root, "--json", "round", "status", "--needs-you")
	rows = data(t, out)["needsYou"].([]any)
	if code != 0 || len(rows) != 1 || rows[0].(map[string]any)["kind"] != "bugreport" {
		t.Errorf("filing an open bugreport needs human approval: %d %s %s", code, out, stderr)
	}
}

func TestMaintainerAgesAreCompact(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		age  time.Duration
		want string
	}{{37 * time.Second, "37s"}, {4 * time.Minute, "4m"}, {2 * time.Hour, "2h"}, {51 * time.Hour, "2d"}} {
		_, lines := maintainerRows([]round.MaintainerWait{{Item: "#7", Ask: "Approve?", Since: now.Add(-tc.age).Format(time.RFC3339), DefaultReply: "hold"}}, now)
		if len(lines) != 1 || !strings.Contains(lines[0], "\t"+tc.want+"\t") {
			t.Errorf("age %s: %v, want %s", tc.age, lines, tc.want)
		}
	}
}

func TestMaintainerTextStripsFormat(t *testing.T) {
	_, lines := maintainerRows([]round.MaintainerWait{{Item: "#7", Ask: "Keep \u202ethe\u2066 flag?\u200b", DefaultReply: "hold"}}, time.Now())
	if len(lines) != 1 || strings.ContainsAny(lines[0], "\u202e\u2066\u200b") || !strings.Contains(lines[0], "Keep the flag?") {
		t.Fatalf("unsafe format characters: %q", lines)
	}
}

func TestWatchMaintainerWarningsOnlyOnChange(t *testing.T) {
	root, deps := roundFixture(t, nil)
	deps.Getwd = func() (string, error) { return root, nil }
	cfg, err := jsonx.Decode([]byte(`{"issues":{"source":"github"}}`))
	if err != nil {
		t.Fatal(err)
	}
	state := "outage-one"
	original := deps.RoundEnv
	deps.RoundEnv = func(ctx context.Context, root string) round.Env {
		e := original(ctx, root)
		e.NeedsHuman, e.ForgeErr = "needs-human", state
		e.Worker = round.FromWorkerEnv(worker.Env{Config: func(string) any { return cfg }})
		return e
	}
	var stderr bytes.Buffer
	c := &Ctx{Deps: deps, Stderr: &stderr, ctx: context.Background()}
	for _, next := range []string{"outage-one", "outage-one", "outage-two", "outage-two", "", "outage-one"} {
		state = next
		watchForge(context.Background(), c, root)
	}
	if got := strings.Count(stderr.String(), "maintainer digest forge unavailable:"); got != 3 {
		t.Fatalf("warn once per transition, got %d:\n%s", got, stderr.String())
	}
}
