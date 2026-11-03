package cli

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/ledger"
	"github.com/l4ci/rota/internal/proc"
)

func TestAccountsViewQuota(t *testing.T) {
	q := knObj("kind", harness.Codex, "name", "work", "home", "/homes/work", "source", "codex-app-server", "status", "available", "reason", "", "identityStatus", "unavailable", "weekly", knObj("remainingPercent", 50.0, "usedPercent", 50.0, "resetsAt", "2026-11-10T16:07:34Z", "status", "available", "reason", ""))
	b := buildBrowser(t, accountsView, knObj("accounts", objList(knObj("kind", harness.Codex, "name", "work", "quota", q))))
	if len(b.Rows) != 1 || !strings.Contains(b.Rows[0].Label, "weekly") || !strings.Contains(b.Rows[0].Preview, "2026-11-10T16:07:34Z") {
		t.Fatalf("UI needs weekly capacity/reset: %+v", b.Rows)
	}
}

func quotaReportingDeps(t *testing.T) *Deps {
	t.Helper()
	d := testDeps()
	pinNow(d, "2026-11-03T21:27:00Z")
	d.UserHomeDir = func() (string, error) { return "/test-home", nil }
	d.Getenv = func(string) string { return "" }
	d.QuotaExchange = func(ctx context.Context, cmd proc.Cmd, talk func(io.Reader, io.Writer) error) error {
		if len(cmd.Env) != 1 || cmd.Env[0] != "CODEX_HOME=/homes/work" {
			t.Fatalf("wrong account child: %+v", cmd)
		}
		return talk(strings.NewReader("{\"id\":1,\"result\":{}}\n{\"id\":2,\"result\":{\"account\":{\"type\":\"chatgpt\"}}}\n{\"id\":3,\"result\":{\"accountId\":\"private-id\",\"rateLimitsByLimitId\":{\"codex\":{\"primary\":{\"usedPercent\":50,\"windowDurationMins\":10080,\"resetsAt\":1794326854},\"secondary\":{\"usedPercent\":12,\"windowDurationMins\":300,\"resetsAt\":1794326854}}}}}\n"), io.Discard)
	}
	return d
}

func TestAccountQuotaReporting(t *testing.T) {
	dir := workerProject(t, `{"round":{"accounts":[{"kind":"codex","name":"work","dir":"/homes/work"}],"harnesses":["codex"]}}`)
	d := quotaReportingDeps(t)
	code, out, stderr := rotaInWith(t, d, dir, "worker", "account", "list", "--json")
	if code != 0 {
		t.Fatalf("%d %s %s", code, out, stderr)
	}
	rows := data(t, out)["accounts"].([]any)
	if len(rows) != 1 {
		t.Fatalf("missing quota row: %s", out)
	}
	row := rows[0].(map[string]any)
	quota := row["quota"].(map[string]any)
	if quota["weekly"].(map[string]any)["remainingPercent"] != float64(50) || row["headroom"] != nil || row["verdict"] != nil || strings.Contains(out, "private-id") {
		t.Fatalf("quota must not become scheduling meter or leak identity: %s", out)
	}
	code, out, stderr = rotaInWith(t, d, dir, "worker", "account", "list")
	if code != 0 || !strings.Contains(out, "weekly: 50% remaining") || !strings.Contains(out, "300m window: 88% remaining") || !strings.Contains(out, "2026-11-10T16:07:34Z") {
		t.Fatalf("%d %s %s", code, out, stderr)
	}
	d.QuotaExchange = func(context.Context, proc.Cmd, func(io.Reader, io.Writer) error) error {
		return errors.New("private credential must not leak")
	}
	_, out, _ = rotaInWith(t, d, dir, "worker", "account", "list")
	if !strings.Contains(out, "unavailable") || strings.Contains(out, "private credential") {
		t.Fatalf("safe unavailable output: %s", out)
	}
}

func TestRoundQuotaReportingKeepsBurnUnknown(t *testing.T) {
	dir := workerProject(t, `{"round":{"accounts":[{"kind":"codex","name":"work","dir":"/homes/work"}]}}`)
	if err := ledger.Append(dir, ledger.Entry{TS: time.Date(2026, 11, 3, 20, 0, 0, 0, time.UTC), Kind: ledger.KindAssign, Round: 4, Issue: "2632", Slot: "dana", Harness: harness.Codex, Account: "work"}); err != nil {
		t.Fatal(err)
	}
	d := quotaReportingDeps(t)
	code, out, stderr := rotaInWith(t, d, dir, "round", "summary", "--round", "4", "--json")
	if code != 0 {
		t.Fatalf("%d %s %s", code, out, stderr)
	}
	obj := data(t, out)
	quotas, ok := obj["accountQuotas"].([]any)
	if !ok || len(quotas) != 1 {
		t.Fatalf("missing current account quota: %s", out)
	}
	if quotas[0].(map[string]any)["weekly"].(map[string]any)["remainingPercent"] != float64(50) {
		t.Fatalf("wrong weekly quota: %s", out)
	}
	for _, key := range []string{"issues", "slots", "accounts"} {
		for _, raw := range obj[key].([]any) {
			if raw.(map[string]any)["quotaShare"] != nil {
				t.Fatalf("current quota must leave %s burn unknown: %s", key, out)
			}
		}
	}
	_, out, _ = rotaInWith(t, d, dir, "round", "summary", "--round", "4")
	if !strings.Contains(out, "Current account quota (observed now; not round usage)") || !strings.Contains(out, "weekly: 50% remaining") {
		t.Fatal(out)
	}
	// A deleted historical name must never be replaced by a live ambient reading.
	d.Config = func(string) any { return jsonx.NewObject() }
	d.QuotaExchange = func(context.Context, proc.Cmd, func(io.Reader, io.Writer) error) error {
		t.Fatal("deleted name queried ambient home")
		return nil
	}
	_, out, _ = rotaInWith(t, d, dir, "round", "summary", "--round", "4", "--json")
	quota := data(t, out)["accountQuotas"].([]any)[0].(map[string]any)
	if quota["status"] != "unavailable" || quota["weekly"] != nil || quota["reason"] == "" {
		t.Fatalf("deleted home unavailable: %s", out)
	}
}

func TestAccountsViewQuotaKeysIncludeKind(t *testing.T) {
	q := knObj("kind", harness.Codex, "name", "work", "status", "unavailable", "reason", "no source")
	b := buildBrowser(t, accountsView, knObj("accounts", objList(knObj("kind", harness.Claude, "name", "work", "verdict", "unknown"), knObj("kind", harness.Codex, "name", "work", "quota", q))))
	if len(b.Rows) != 2 || b.Rows[0].Key == b.Rows[1].Key || !strings.Contains(b.Rows[1].Preview, "no source") {
		t.Fatalf("kind/name isolation: %+v", b.Rows)
	}
}
