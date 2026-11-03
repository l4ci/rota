package harness

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/proc"
)

var quotaNow = time.Date(2026, 11, 3, 21, 27, 0, 0, time.UTC)

func quotaFixture(payload string) QuotaOptions {
	return QuotaOptions{Now: func() time.Time { return quotaNow }, UserHome: "/test", Exchange: func(ctx context.Context, cmd proc.Cmd, talk func(io.Reader, io.Writer) error) error {
		return talk(strings.NewReader("{\"id\":1,\"result\":{}}\n{\"id\":2,\"result\":{\"account\":{\"type\":\"chatgpt\"}}}\n{\"id\":3,\"result\":"+payload+"}\n"), io.Discard)
	}}
}

func TestCodexQuotaWeeklyPrimary(t *testing.T) {
	q, ok := LookupQuotaReporter(Codex)
	if !ok {
		t.Fatal("Codex needs account quota reporting")
	}
	s := q.ReadQuota(context.Background(), QuotaRequest{Name: "work", Account: Account{Home: "/homes/work"}}, quotaFixture(`{"accountId":"private-id","rateLimitsByLimitId":{"codex":{"primary":{"usedPercent":50,"windowDurationMins":10080,"resetsAt":1794326854}}}}`))
	if s.Status != "available" || s.Weekly == nil || s.Weekly.RemainingPercent == nil || *s.Weekly.RemainingPercent != 50 {
		t.Fatalf("weekly primary should report 50%% remaining, got %+v", s)
	}
	if s.Weekly.ResetsAt == nil || s.Weekly.ResetsAt.Format(time.RFC3339) != "2026-11-10T16:07:34Z" {
		t.Fatalf("epoch seconds reset: %+v", s.Weekly)
	}
	if s.AccountRef == "" || s.AccountRef == "private-id" || s.IdentityStatus != "reported" {
		t.Fatalf("expected hashed identity, got %+v", s)
	}
	if ResolveAccounts(Codex).UsageMeter() {
		t.Fatal("reporting must not enable scheduling meter")
	}
}

func TestCodexQuotaWindows(t *testing.T) {
	tests := []struct {
		name, payload, status, reason string
		used, remaining               *float64
		windows                       int
	}{
		{"secondary weekly", `{"rateLimitsByLimitId":{"other":{"primary":{"usedPercent":99,"windowDurationMins":10080}},"codex":{"primary":{"usedPercent":12,"windowDurationMins":300,"resetsAt":1794326854},"secondary":{"usedPercent":25,"windowDurationMins":10080,"resetsAt":1794326854}}}}`, "available", "", floatPtr(25), floatPtr(75), 2},
		{"zero is real", `{"rateLimits":{"limitId":"codex","primary":{"usedPercent":0,"windowDurationMins":10080,"resetsAt":1794326854}}}`, "available", "", floatPtr(0), floatPtr(100), 1},
		{"over capacity", `{"rateLimits":{"primary":{"usedPercent":125,"windowDurationMins":10080,"resetsAt":1794326854}}}`, "available", "", floatPtr(125), floatPtr(0), 1},
		{"missing usage", `{"rateLimits":{"primary":{"windowDurationMins":10080,"resetsAt":1794326854}}}`, "unavailable", "usage percentage missing", nil, nil, 1},
		{"negative usage", `{"rateLimits":{"primary":{"usedPercent":-1,"windowDurationMins":10080,"resetsAt":1794326854}}}`, "unavailable", "invalid usage percentage", nil, nil, 1},
		{"missing reset", `{"rateLimits":{"primary":{"usedPercent":50,"windowDurationMins":10080,"resetsAt":null}}}`, "partial", "reset time unavailable", floatPtr(50), floatPtr(50), 1},
		{"elapsed reset", `{"rateLimits":{"primary":{"usedPercent":50,"windowDurationMins":10080,"resetsAt":1}}}`, "stale", "reset elapsed", floatPtr(50), floatPtr(50), 1},
		{"invalid timestamp", `{"rateLimits":{"primary":{"usedPercent":50,"windowDurationMins":10080,"resetsAt":9999999999999}}}`, "unavailable", "invalid reset timestamp", floatPtr(50), floatPtr(50), 1},
		{"missing duration", `{"rateLimits":{"primary":{"usedPercent":50,"resetsAt":1794326854}}}`, "unavailable", "weekly window unavailable", nil, nil, 1},
		{"shorter only", `{"rateLimits":{"primary":{"usedPercent":50,"windowDurationMins":300}}}`, "unavailable", "weekly window unavailable", nil, nil, 1},
		{"no bucket", `{"rateLimitsByLimitId":{"other":{}},"rateLimits":{"primary":{"usedPercent":0,"windowDurationMins":10080}}}`, "unavailable", "Codex quota bucket unavailable", nil, nil, 0},
		{"empty bucket map", `{"rateLimitsByLimitId":{},"rateLimits":{"primary":{"usedPercent":0,"windowDurationMins":10080}}}`, "unavailable", "Codex quota bucket unavailable", nil, nil, 0},
		{"wrong legacy bucket", `{"rateLimits":{"limitId":"other"}}`, "unavailable", "conflicting quota bucket", nil, nil, 0},
		{"conflicting weekly", `{"rateLimits":{"primary":{"usedPercent":1,"windowDurationMins":10080},"secondary":{"usedPercent":2,"windowDurationMins":10080}}}`, "unavailable", "ambiguous weekly", nil, nil, 2},
		{"nonfinite", `{"rateLimits":{"primary":{"usedPercent":1e999,"windowDurationMins":10080}}}`, "unavailable", "malformed quota fields", nil, nil, 0},
		{"wrong type", `{"rateLimits":{"primary":{"usedPercent":"secret","windowDurationMins":10080}}}`, "unavailable", "malformed quota fields", nil, nil, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := (codex{}).ReadQuota(context.Background(), QuotaRequest{Name: "work", Account: Account{Home: "/work"}}, quotaFixture(tt.payload))
			if s.Status != tt.status || !strings.Contains(s.Reason, tt.reason) || len(s.Windows) != tt.windows {
				t.Fatalf("got %+v, want %s / %s, %d windows", s, tt.status, tt.reason, tt.windows)
			}
			if tt.used != nil && (s.Weekly == nil || s.Weekly.UsedPercent == nil || *s.Weekly.UsedPercent != *tt.used || s.Weekly.RemainingPercent == nil || *s.Weekly.RemainingPercent != *tt.remaining) {
				t.Fatalf("wrong weekly: %+v", s.Weekly)
			}
			if s.IdentityStatus != "unavailable" || s.AccountRef != "" {
				t.Fatalf("fabricated remote identity: %+v", s)
			}
		})
	}
}

func floatPtr(v float64) *float64 { return &v }

func TestCodexQuotaProtocol(t *testing.T) {
	cases := []struct{ name, input, reason string }{
		{"unsupported", `{"id":1,"error":{"code":-32601,"message":"secret-token"}}`, "unsupported"},
		{"RPC failure", `{"id":1,"error":{"code":-1,"message":"secret-token"}}`, "authentication or source unavailable"},
		{"EOF", `{"id":1,"result":{}}`, "closed"},
		{"malformed", `secret-token`, "malformed"},
		{"changed", `{"method":"account/updated","params":{"secret":"token"}}`, "account changed"},
		{"oversized", strings.Repeat("x", 300*1024), "oversized"},
		{"message bound", strings.Repeat("{\"method\":\"noise\"}\n", 65), "message limit"},
		{"no auth", "{\"id\":1,\"result\":{}}\n{\"id\":2,\"result\":{\"account\":null}}\n", "authentication unavailable"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			opts := quotaFixture(`{}`)
			opts.Exchange = func(ctx context.Context, c proc.Cmd, talk func(io.Reader, io.Writer) error) error {
				return talk(strings.NewReader(tt.input), io.Discard)
			}
			s := (codex{}).ReadQuota(context.Background(), QuotaRequest{Ambient: true}, opts)
			if s.Status != "unavailable" || !strings.Contains(s.Reason, tt.reason) || strings.Contains(s.Reason, "secret-token") {
				t.Fatalf("got %+v", s)
			}
		})
	}
}

func TestCodexQuotaHandshakeAndIsolation(t *testing.T) {
	opts := quotaFixture(`{}`)
	opts.Exchange = func(ctx context.Context, c proc.Cmd, talk func(io.Reader, io.Writer) error) error {
		if c.Name != Codex || strings.Join(c.Args, " ") != "app-server --listen stdio://" || len(c.Env) != 1 || c.Env[0] != "CODEX_HOME=/named" {
			t.Fatalf("wrong child: %+v", c)
		}
		clientR, serverW := io.Pipe()
		serverR, clientW := io.Pipe()
		done := make(chan error, 1)
		go func() {
			defer serverW.Close()
			defer serverR.Close()
			done <- talk(clientR, clientW)
			clientR.Close()
			clientW.Close()
		}()
		scan := bufio.NewScanner(serverR)
		expected := []string{"initialize", "initialized", "account/read", "account/rateLimits/read"}
		for i, method := range expected {
			if !scan.Scan() {
				t.Fatalf("request %s missing", method)
			}
			var req struct {
				ID     int
				Method string
				Params map[string]any
			}
			if json.Unmarshal(scan.Bytes(), &req) != nil || req.Method != method {
				t.Fatalf("request %q, want %s", scan.Text(), method)
			}
			switch i {
			case 0:
				io.WriteString(serverW, "{\"method\":\"noise\"}\n{\"id\":99,\"result\":{}}\n{\"id\":1,\"result\":{}}\n")
			case 2:
				if req.Params["refreshToken"] != false {
					t.Fatal("must not request token refresh")
				}
				io.WriteString(serverW, "{\"id\":2,\"result\":{\"account\":{\"type\":\"chatgpt\"}}}\n")
			case 3:
				io.WriteString(serverW, "{\"id\":3,\"result\":{\"rateLimits\":{\"primary\":{\"usedPercent\":100,\"windowDurationMins\":10080,\"resetsAt\":1794326854}}}}\n")
			}
		}
		return <-done
	}
	s := (codex{}).ReadQuota(context.Background(), QuotaRequest{Name: "work", Account: Account{Home: "/named"}}, opts)
	if s.Status != "available" || s.Home != "/named" || *s.Weekly.RemainingPercent != 0 {
		t.Fatalf("got %+v", s)
	}
	called := false
	opts.Exchange = func(context.Context, proc.Cmd, func(io.Reader, io.Writer) error) error { called = true; return nil }
	s = (codex{}).ReadQuota(context.Background(), QuotaRequest{Name: "missing"}, opts)
	if called || s.Status != "unavailable" || !strings.Contains(s.Reason, "home missing") {
		t.Fatalf("missing named home must fail closed: %+v", s)
	}
}

// TestCodexQuotaLive is a manual, opt-in source check; the default suite is offline.
// It starts no thread/turn and logs only capacity, reset and source metadata.
func TestCodexQuotaLive(t *testing.T) {
	if os.Getenv("ROTA_CODEX_QUOTA_LIVE") != "1" {
		t.Skip("manual authenticated source check; set ROTA_CODEX_QUOTA_LIVE=1")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal("user home unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	s := (codex{}).ReadQuota(ctx, QuotaRequest{Name: "ambient/current", Ambient: true, Account: (codex{}).AmbientAccount(os.Getenv)}, QuotaOptions{UserHome: home})
	if s.Status != "available" && s.Status != "partial" {
		t.Fatalf("source check: %s: %s", s.Status, s.Reason)
	}
	if s.Weekly == nil || s.Weekly.UsedPercent == nil || s.Weekly.RemainingPercent == nil {
		t.Fatal("source did not report weekly capacity")
	}
	reset := "unavailable"
	if s.Weekly.ResetsAt != nil {
		reset = s.Weekly.ResetsAt.Format(time.RFC3339)
	}
	t.Logf("observed=%s source=%s detail=%s weekly-used=%g%% remaining=%g%% reset=%s status=%s identity=%s shorter-windows=%d", s.ObservedAt.Format(time.RFC3339), s.Source, s.SourceDetail, *s.Weekly.UsedPercent, *s.Weekly.RemainingPercent, reset, s.Status, s.IdentityStatus, len(s.Windows)-1)
}
