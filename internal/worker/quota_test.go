package worker

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/harness"
)

func TestQuotasNamedHomesStayIsolated(t *testing.T) {
	q := &Quotas{Env: Env{
		Config: memConfig(t, `{"round":{"accounts":[{"kind":"codex","name":"one","dir":"~/one"},{"kind":"codex","name":"two","dir":"/two"},{"kind":"claude","name":"one","configDir":"/other"}]}}`),
		Home:   func() (string, error) { return "/caller", nil },
		Getenv: func(string) string { return "/ambient" },
	}, Read: func(ctx context.Context, reporter harness.QuotaReporter, req harness.QuotaRequest, opts harness.QuotaOptions) harness.QuotaSnapshot {
		if req.Ambient {
			t.Error("named home became ambient")
		}
		return harness.QuotaSnapshot{Name: req.Name, Home: req.Account.Home, Status: "available"}
	}}
	got := q.List(context.Background(), "")
	if len(got) != 2 {
		t.Fatalf("got %d quota rows, want two reporter accounts", len(got))
	}
	if got[0].Name != "one" || got[0].Home != "/caller/one" || got[1].Name != "two" || got[1].Home != "/two" {
		t.Fatalf("homes = %#v", got)
	}
}

func TestQuotasUnavailableAccountsNeverFallBack(t *testing.T) {
	now := time.Date(2026, 11, 3, 22, 0, 0, 0, time.UTC)
	q := &Quotas{Env: Env{
		Config: memConfig(t, `{"round":{"accounts":[{"kind":"codex","name":"missing-home"},{"kind":"codex","name":"duplicate","dir":"/a"},{"kind":"codex","name":"duplicate","dir":"/b"},{"kind":"codex","name":"relative","dir":"relative"}]}}`),
		Home:   func() (string, error) { return "/caller", nil }, Getenv: func(string) string { return "/ambient" }, Now: func() time.Time { return now },
	}, Read: func(context.Context, harness.QuotaReporter, harness.QuotaRequest, harness.QuotaOptions) harness.QuotaSnapshot {
		t.Error("unresolvable account reached reader")
		return harness.QuotaSnapshot{}
	}}
	refs := []QuotaAccount{{harness.Codex, "deleted"}, {harness.Codex, "missing-home"}, {harness.Codex, "duplicate"}, {harness.Codex, "relative"}, {harness.Codex, ""}, {harness.Codex, "deleted"}, {harness.Claude, "deleted"}}
	got := q.ForAccounts(context.Background(), "", refs)
	if len(got) != 5 {
		t.Fatalf("got %d rows, want five unique reporter references", len(got))
	}
	reasons := []string{"configured account missing", "named account home missing", "configured account name is ambiguous", "account home must be absolute", "historical ambient account cannot resolve with named accounts"}
	for i, row := range got {
		if row.Status != "unavailable" || row.Reason != reasons[i] || row.IdentityStatus != "unavailable" || row.Source != "not-read" || row.Weekly != nil || len(row.Windows) != 0 || !row.ObservedAt.Equal(now) {
			t.Errorf("row %d = %#v", i, row)
		}
	}
}

func TestQuotasAmbientIsExplicitCallerCurrent(t *testing.T) {
	q := &Quotas{Env: Env{Config: memConfig(t, `{"round":{"harnesses":["codex"]}}`), Home: func() (string, error) { return "/caller", nil }, Getenv: func(string) string { return "~/ambient" }}, Read: func(ctx context.Context, r harness.QuotaReporter, req harness.QuotaRequest, opts harness.QuotaOptions) harness.QuotaSnapshot {
		if !req.Ambient || req.Name != "ambient/current" || req.Account.Home != "/caller/ambient" || opts.UserHome != "/caller" {
			t.Errorf("request=%#v userHome=%q", req, opts.UserHome)
		}
		return harness.QuotaSnapshot{Status: "available", Home: req.Account.Home}
	}}
	for _, rows := range [][]harness.QuotaSnapshot{q.List(context.Background(), ""), q.ForAccounts(context.Background(), "", []QuotaAccount{{harness.Codex, ""}})} {
		if len(rows) != 1 || !rows[0].Ambient || rows[0].Name != "ambient/current" || rows[0].Home != "/caller/ambient" {
			t.Fatalf("ambient=%#v", rows)
		}
	}
}

func TestQuotasCancellationBoundsConcurrencyAndQueuedAccounts(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{}, 5)
	q := &Quotas{Env: Env{Config: memConfig(t, `{"round":{"accounts":[{"kind":"codex","name":"a","dir":"/a"},{"kind":"codex","name":"b","dir":"/b"},{"kind":"codex","name":"c","dir":"/c"},{"kind":"codex","name":"d","dir":"/d"},{"kind":"codex","name":"e","dir":"/e"}]}}`), Home: func() (string, error) { return "/caller", nil }, Getenv: func(string) string { return "" }}, Read: func(ctx context.Context, r harness.QuotaReporter, req harness.QuotaRequest, opts harness.QuotaOptions) harness.QuotaSnapshot {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 12*time.Second {
			t.Error("missing total 12-second deadline")
		}
		started <- struct{}{}
		<-ctx.Done()
		return harness.QuotaSnapshot{Status: "available"}
	}}
	done := make(chan []harness.QuotaSnapshot, 1)
	go func() { done <- q.List(ctx, "") }()
	<-started
	<-started
	select {
	case <-started:
		t.Error("more than two concurrent readers")
	default:
	}
	cancel()
	got := <-done
	if len(got) != 5 {
		t.Fatalf("got %d rows, want all five including queued", len(got))
	}
	for _, row := range got {
		if row.Status != "unavailable" || !strings.Contains(row.Reason, "timed out or canceled") || row.Weekly != nil {
			t.Errorf("canceled row=%#v", row)
		}
	}
}

func TestQuotasRefreshDoesNotReusePreviousReading(t *testing.T) {
	calls := 0
	q := &Quotas{Env: Env{Config: memConfig(t, `{"round":{"accounts":[{"kind":"codex","name":"a","dir":"/a"}]}}`), Home: func() (string, error) { return "/caller", nil }, Getenv: func(string) string { return "" }}, Read: func(ctx context.Context, r harness.QuotaReporter, req harness.QuotaRequest, opts harness.QuotaOptions) harness.QuotaSnapshot {
		calls++
		if calls == 1 {
			used := 100.0
			return harness.QuotaSnapshot{Status: "available", Weekly: &harness.QuotaWindow{UsedPercent: &used}}
		}
		return harness.QuotaSnapshot{Status: "unavailable", Reason: "offline"}
	}}
	first := q.List(context.Background(), "")
	second := q.List(context.Background(), "")
	if len(first) != 1 || first[0].Weekly == nil || len(second) != 1 || second[0].Weekly != nil || second[0].Reason != "offline" || calls != 2 {
		t.Fatalf("first=%#v second=%#v calls=%d", first, second, calls)
	}
	hz, _ := harness.LookupAccounts(harness.Codex)
	if hz.UsageMeter() {
		t.Fatal("display quota enabled scheduling meter")
	}
}

func TestQuotasCancellationWaitsForReaderCleanup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	cleanupStarted := make(chan struct{})
	allowCleanup := make(chan struct{})
	cleanupDone := make(chan struct{})
	q := &Quotas{Env: Env{Config: memConfig(t, `{"round":{"accounts":[{"kind":"codex","name":"a","dir":"/a"}]}}`), Home: func() (string, error) { return "/caller", nil }, Getenv: func(string) string { return "" }}, Read: func(ctx context.Context, r harness.QuotaReporter, req harness.QuotaRequest, opts harness.QuotaOptions) harness.QuotaSnapshot {
		close(started)
		<-ctx.Done()
		close(cleanupStarted)
		<-allowCleanup
		close(cleanupDone)
		return harness.QuotaSnapshot{}
	}}
	done := make(chan []harness.QuotaSnapshot, 1)
	go func() { done <- q.List(ctx, "") }()
	<-started
	cancel()
	<-cleanupStarted
	select {
	case <-done:
		close(allowCleanup)
		t.Fatal("returned while reader cleanup was blocked")
	default:
	}
	close(allowCleanup)
	<-done
	select {
	case <-cleanupDone:
	default:
		t.Fatal("returned before reader cleanup acknowledgement")
	}
}

func TestQuotasAmbientDefaultHomeUsesInjectedUserHome(t *testing.T) {
	q := &Quotas{Env: Env{Config: memConfig(t, `{"round":{"harnesses":["codex"]}}`), Home: func() (string, error) { return "/isolated", nil }, Getenv: func(string) string { return "" }}, Read: func(ctx context.Context, r harness.QuotaReporter, req harness.QuotaRequest, opts harness.QuotaOptions) harness.QuotaSnapshot {
		if !req.Ambient || req.Account.Home != "" || opts.UserHome != "/isolated" {
			t.Errorf("default home request=%#v userHome=%q", req, opts.UserHome)
		}
		return harness.QuotaSnapshot{Status: "available", Home: "/isolated/.codex"}
	}}
	got := q.List(context.Background(), "")
	if len(got) != 1 || got[0].Home != "/isolated/.codex" {
		t.Fatalf("default ambient=%#v", got)
	}
}

func TestQuotasAmbientIndependentOfWorkerDefault(t *testing.T) {
	for _, cfg := range []string{`{"round":{"harnesses":["claude","codex"]}}`, `{}`} {
		t.Run(cfg, func(t *testing.T) {
			configuration := memConfig(t, cfg)
			before := harness.ProjectKind(configuration(""))
			q := &Quotas{Env: Env{Config: configuration, Home: func() (string, error) { return "/caller", nil }, Getenv: func(string) string { return "/ambient" }}, Read: func(ctx context.Context, r harness.QuotaReporter, req harness.QuotaRequest, opts harness.QuotaOptions) harness.QuotaSnapshot {
				if !req.Ambient || req.Name != "ambient/current" || req.Account.Home != "/ambient" {
					t.Errorf("wrong ambient identity: %+v", req)
				}
				return harness.QuotaSnapshot{Home: req.Account.Home, Status: "unavailable", IdentityStatus: "unavailable", Reason: "offline fixture"}
			}}
			rows := q.List(context.Background(), "")
			if len(rows) != 1 || !rows[0].Ambient || rows[0].Name != "ambient/current" || rows[0].Home != "/ambient" || rows[0].Reason != "offline fixture" {
				t.Fatalf("ambient reporting must survive a different worker default: %+v", rows)
			}
			if after := harness.ProjectKind(configuration("")); after != before {
				t.Fatalf("report changed worker default: before=%s after=%s", before, after)
			}
		})
	}
}

// A cached reading belongs to the login it read: the same account name under
// another dir is read again, and an unchanged one is not.
func TestQuotaMetersCacheKeyIncludesTheHome(t *testing.T) {
	dir := "/h1"
	var reads atomic.Int32
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	q := &Quotas{
		Env: Env{Now: func() time.Time { return now }, Home: func() (string, error) { return "/home/x", nil }, Config: func(root string) any {
			return memConfig(t, `{"round":{"accounts":[{"kind":"codex","name":"c1","dir":"`+dir+`"}]}}`)(root)
		}},
		Options: harness.QuotaOptions{Now: func() time.Time { return now }},
		Read: func(context.Context, harness.QuotaReporter, harness.QuotaRequest, harness.QuotaOptions) harness.QuotaSnapshot {
			reads.Add(1)
			return harness.QuotaSnapshot{Status: "unavailable"}
		},
	}
	read := func() { q.Meters(context.Background(), "/p", []string{harness.Codex}) }
	read()
	read()
	if n := reads.Load(); n != 1 {
		t.Fatalf("%d reads for one unchanged account", n)
	}
	dir = "/h2"
	read()
	if n := reads.Load(); n != 2 {
		t.Fatalf("%d reads after the dir changed, want 2", n)
	}
}
