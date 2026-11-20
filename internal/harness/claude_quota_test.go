package harness

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/jsonx"
)

const usageGetFailure = "usage endpoint unreachable"

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func resp(code int, body string, hdr map[string]string) *http.Response {
	h := http.Header{}
	for k, v := range hdr {
		h.Set(k, v)
	}
	return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: h}
}

const okBody = `{"five_hour":{"utilization":12,"resets_at":null},"seven_day":{"utilization":3,"resets_at":null}}`

// usageRig is a Claude fetch with a logged-in config dir, a temp cache dir and
// a counted fake endpoint.
func usageRig(t *testing.T, handler func(*http.Request) (*http.Response, error)) (*claudeFetch, string, *int) {
	t.Helper()
	cfg := t.TempDir()
	os.WriteFile(filepath.Join(cfg, ".credentials.json"), []byte(`{"claudeAiOauth":{"accessToken":"sk-ant-oat-x"}}`), 0o600)
	calls := 0
	f := newClaudeFetch(QuotaOptions{
		CacheDir: t.TempDir(),
		HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			return handler(r)
		})},
	})
	return f, cfg, &calls
}

func TestUsageFreshCacheIsReadWithoutRequest(t *testing.T) {
	f, cfg, calls := usageRig(t, func(*http.Request) (*http.Response, error) { return resp(200, okBody, nil), nil })
	if err := os.WriteFile(f.usageCachePath(cfg), []byte(okBody), 0o644); err != nil {
		t.Fatal(err)
	}
	p, why := f.defaultFetch(context.Background(), "a", cfg)
	if p == nil || why != "" {
		t.Fatalf("payload=%v why=%q", p, why)
	}
	if *calls != 0 {
		t.Errorf("requests = %d, want 0", *calls)
	}
}

func TestUsageStaleCacheFetchesAndWritesCacheAtomically(t *testing.T) {
	f, cfg, calls := usageRig(t, func(*http.Request) (*http.Response, error) { return resp(200, okBody, nil), nil })
	path := f.usageCachePath(cfg)
	os.WriteFile(path, []byte(`{"stale":1}`), 0o644)
	old := time.Now().Add(-10 * time.Minute)
	os.Chtimes(path, old, old)
	p, why := f.defaultFetch(context.Background(), "a", cfg)
	if p == nil || why != "" || *calls != 1 {
		t.Fatalf("payload=%v why=%q calls=%d", p, why, *calls)
	}
	b, _ := os.ReadFile(path)
	if strings.TrimSpace(string(b)) != okBody {
		t.Errorf("cache = %s", b)
	}
	if left, _ := filepath.Glob(path + ".*"); len(left) != 0 {
		t.Errorf("temp files left: %v", left)
	}
}

func TestUsageStatusReasons(t *testing.T) {
	for _, tc := range []struct {
		name string
		r    *http.Response
		want string
	}{
		{"429", resp(429, `{"error":{"type":"rate_limit_error"}}`, map[string]string{"Retry-After": "120"}), "rate limited (retry in 120s)"},
		{"429 default", resp(429, `{}`, nil), "rate limited (retry in 300s)"},
		{"401", resp(401, `{}`, nil), "login expired"},
		{"403", resp(403, `{}`, nil), "login expired"},
		{"500", resp(500, `oops`, nil), "usage endpoint returned 500"},
		{"200 no five_hour", resp(200, `{"x":1}`, nil), "unexpected payload"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, cfg, _ := usageRig(t, func(*http.Request) (*http.Response, error) { return tc.r, nil })
			p, why := f.defaultFetch(context.Background(), "a", cfg)
			if p != nil || why != tc.want {
				t.Errorf("payload=%v why=%q, want %q", p, why, tc.want)
			}
		})
	}
}

func TestUsageRequestCarriesTheAccountsToken(t *testing.T) {
	f, cfg, _ := usageRig(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != claudeUsageURL || r.Header.Get("Authorization") != "Bearer sk-ant-oat-x" || r.Header.Get("anthropic-beta") != "oauth-2025-04-20" {
			t.Errorf("request %s auth=%q beta=%q", r.URL, r.Header.Get("Authorization"), r.Header.Get("anthropic-beta"))
		}
		return resp(200, okBody, nil), nil
	})
	if p, why := f.defaultFetch(context.Background(), "a", cfg); p == nil {
		t.Fatalf("why=%q", why)
	}
}

func TestUsageBackoffWrittenAndHonoured(t *testing.T) {
	f, cfg, calls := usageRig(t, func(*http.Request) (*http.Response, error) {
		return resp(429, `{}`, map[string]string{"Retry-After": "120"}), nil
	})
	f.defaultFetch(context.Background(), "a", cfg)
	if _, err := os.Stat(f.usageCachePath(cfg) + ".backoff"); err != nil {
		t.Fatalf("no backoff file: %v", err)
	}
	_, why := f.defaultFetch(context.Background(), "a", cfg)
	if *calls != 1 {
		t.Errorf("requests = %d, want 1 (backoff holds)", *calls)
	}
	if !strings.HasPrefix(why, "rate limited (retry in ") {
		t.Errorf("why = %q", why)
	}
}

func TestUsageReadsBackoffFromHerdrMod(t *testing.T) {
	f, cfg, calls := usageRig(t, func(*http.Request) (*http.Response, error) { return resp(200, okBody, nil), nil })
	// The mod writes the wait in seconds; the file's mtime is when it began.
	bo := f.usageCachePath(cfg) + ".backoff"
	os.WriteFile(bo, []byte("120\n"), 0o644)
	p, why := f.defaultFetch(context.Background(), "a", cfg)
	if p != nil || *calls != 0 || !strings.HasPrefix(why, "backing off after a failed request (retry in ") {
		t.Errorf("payload=%v calls=%d why=%q", p, *calls, why)
	}
	// An elapsed backoff no longer holds.
	old := time.Now().Add(-3 * time.Minute)
	os.Chtimes(bo, old, old)
	if p, _ := f.defaultFetch(context.Background(), "a", cfg); p == nil || *calls != 1 {
		t.Errorf("elapsed backoff still held: payload=%v calls=%d", p, *calls)
	}
	// Success clears the backoff file, as the mod does.
	if _, err := os.Stat(bo); err == nil {
		t.Error("backoff file survived a successful fetch")
	}
}

func TestUsageCachePathSuffix(t *testing.T) {
	home := "/home/u"
	f := newClaudeFetch(QuotaOptions{CacheDir: "/c", UserHome: home})
	if got := f.usageCachePath(filepath.Join(home, ".claude")); got != "/c/statusline-usage.json" {
		t.Errorf("default dir path = %s", got)
	}
	if got := f.usageCachePath("/acct/alpha"); got != "/c/statusline-usage-"+sum8("/acct/alpha")+".json" {
		t.Errorf("hashed path = %s", got)
	}
	if got := newClaudeFetch(QuotaOptions{UserHome: home}).usageCachePath("/acct/alpha"); !strings.HasPrefix(got, claudeUsageDir+"/") {
		t.Errorf("default cache dir = %s, want under %s", got, claudeUsageDir)
	}
}

func sum8(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])[:8]
}

func TestUsageWaitsAreCappedAtOneHour(t *testing.T) {
	f, cfg, _ := usageRig(t, func(*http.Request) (*http.Response, error) {
		return resp(429, `{}`, map[string]string{"Retry-After": "999999"}), nil
	})
	if _, why := f.defaultFetch(context.Background(), "a", cfg); why != "rate limited (retry in 3600s)" {
		t.Errorf("Retry-After not capped: %q", why)
	}
	// A backoff file someone planted with a huge wait is capped the same way.
	os.WriteFile(f.usageCachePath(cfg)+".backoff", []byte("99999999\n"), 0o644)
	if _, why := f.defaultFetch(context.Background(), "a", cfg); why != "rate limited (retry in 3600s)" {
		t.Errorf("backoff file not capped: %q", why)
	}
}

func TestUsageIgnoresFilesOwnedByAnotherUser(t *testing.T) {
	f, cfg, calls := usageRig(t, func(*http.Request) (*http.Response, error) { return resp(200, okBody, nil), nil })
	f.opts.OwnedByMe = func(os.FileInfo) bool { return false }
	os.WriteFile(f.usageCachePath(cfg), []byte(okBody), 0o644)
	os.WriteFile(f.usageCachePath(cfg)+".backoff", []byte("120\n"), 0o644)
	if p, _ := f.defaultFetch(context.Background(), "a", cfg); p == nil || *calls != 1 {
		t.Errorf("foreign-owned cache or backoff was trusted: payload=%v calls=%d", p, *calls)
	}
}

func TestUsageBackoffKeepsTheRealReason(t *testing.T) {
	f, cfg, calls := usageRig(t, func(*http.Request) (*http.Response, error) { return resp(401, `{}`, nil), nil })
	if _, why := f.defaultFetch(context.Background(), "a", cfg); why != "login expired" {
		t.Fatalf("first why = %q", why)
	}
	if _, why := f.defaultFetch(context.Background(), "a", cfg); why != "login expired" || *calls != 1 {
		t.Errorf("held why = %q calls=%d, want login expired with no request", why, *calls)
	}
}

func TestUsageEveryFailedRequestWritesBackoff(t *testing.T) {
	for _, tc := range []struct {
		name string
		h    func(*http.Request) (*http.Response, error)
		want string
	}{
		{"transport", func(*http.Request) (*http.Response, error) { return nil, io.ErrUnexpectedEOF }, usageGetFailure},
		{"not json", func(*http.Request) (*http.Response, error) { return resp(200, `<html>`, nil), nil }, usageGetFailure},
		{"no five_hour", func(*http.Request) (*http.Response, error) { return resp(200, `{"x":1}`, nil), nil }, "unexpected payload"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, cfg, calls := usageRig(t, tc.h)
			f.defaultFetch(context.Background(), "a", cfg)
			b, err := os.ReadFile(f.usageCachePath(cfg) + ".backoff")
			if err != nil || strings.TrimSpace(string(b)) != "300" {
				t.Fatalf("backoff = %q, %v", b, err)
			}
			if _, why := f.defaultFetch(context.Background(), "a", cfg); *calls != 1 || why != tc.want {
				t.Errorf("held: calls=%d why=%q, want %q", *calls, why, tc.want)
			}
		})
	}
}

func TestUsageRetryAfterStoredAsGiven(t *testing.T) {
	f, cfg, _ := usageRig(t, func(*http.Request) (*http.Response, error) {
		return resp(429, `{}`, map[string]string{"Retry-After": "7200"}), nil
	})
	f.defaultFetch(context.Background(), "a", cfg)
	b, _ := os.ReadFile(f.usageCachePath(cfg) + ".backoff")
	if strings.TrimSpace(string(b)) != "7200" {
		t.Errorf("stored wait = %q, want 7200", b)
	}
}

func TestUsageSymlinkedFilesAreNotFollowed(t *testing.T) {
	f, cfg, calls := usageRig(t, func(*http.Request) (*http.Response, error) { return resp(200, okBody, nil), nil })
	target := filepath.Join(t.TempDir(), "t")
	os.WriteFile(target, []byte(okBody), 0o644)
	cache := f.usageCachePath(cfg)
	if err := os.Symlink(target, cache); err != nil {
		t.Fatal(err)
	}
	bo := t.TempDir() + "/b"
	os.WriteFile(bo, []byte("120\n"), 0o644)
	os.Symlink(bo, cache+".backoff")
	if p, _ := f.defaultFetch(context.Background(), "a", cfg); p == nil || *calls != 1 {
		t.Errorf("symlinked cache or backoff was followed: payload=%v calls=%d", p, *calls)
	}
}

func TestUsageFifoAtCacheOrBackoffPathReturnsPromptly(t *testing.T) {
	for _, suffix := range []string{"", ".backoff"} {
		t.Run("fifo"+suffix, func(t *testing.T) {
			f, cfg, calls := usageRig(t, func(*http.Request) (*http.Response, error) { return resp(200, okBody, nil), nil })
			fifo := f.usageCachePath(cfg) + suffix
			if err := syscall.Mkfifo(fifo, 0o644); err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			go func() {
				f.defaultFetch(context.Background(), "a", cfg)
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				// Release the blocked open so the goroutine ends, then fail.
				if fh, err := os.OpenFile(fifo, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
					fh.Close()
				}
				t.Fatal("fetch blocked on a FIFO")
			}
			if *calls != 1 {
				t.Errorf("requests = %d, want 1 (FIFO unreadable)", *calls)
			}
		})
	}
}

func TestParseResetReadsStringsOnly(t *testing.T) {
	if parseReset("garbage") != nil || parseReset(nil) != nil || parseReset("") != nil || parseReset(5) != nil {
		t.Error("unparseable resets must read as none")
	}
	if parseReset("2026-10-02T15:00:00Z") == nil {
		t.Error("a valid reset must parse")
	}
}

func TestWindowRejectsOutOfRangeUtilization(t *testing.T) {
	for _, u := range []string{"150", "-1", "100.5"} {
		v, _ := jsonx.Decode([]byte(`{"five_hour":{"utilization":` + u + `}}`))
		if got, _, present := claudeWindow(v.(*jsonx.Object), "five_hour"); got != nil || !present {
			t.Errorf("utilization %s read as %v (present %v), want an unknown window", u, got, present)
		}
	}
	v, _ := jsonx.Decode([]byte(`{"five_hour":{"utilization":0},"seven_day":{"utilization":100}}`))
	for _, k := range []string{"five_hour", "seven_day"} {
		if got, _, _ := claudeWindow(v.(*jsonx.Object), k); got == nil {
			t.Errorf("%s boundary value rejected", k)
		}
	}
	if _, _, present := claudeWindow(v.(*jsonx.Object), "other"); present {
		t.Error("a missing window is not present")
	}
}

// fixtureQuota reads one account from a ROTA_ACCOUNT_USAGE_DIR fixture through
// the reporter, as an offline test does.
func fixtureQuota(t *testing.T, name, body string) QuotaSnapshot {
	t.Helper()
	dir := t.TempDir()
	if body != "" {
		os.WriteFile(filepath.Join(dir, name+".json"), []byte(body), 0o644)
	}
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	return claude{}.ReadQuota(context.Background(), QuotaRequest{Name: name, Account: Account{Home: "/acct/" + name}}, QuotaOptions{
		Now: func() time.Time { return now }, UserHome: "/home/u",
		Getenv: func(k string) string {
			if k == "ROTA_ACCOUNT_USAGE_DIR" {
				return dir
			}
			return ""
		},
	})
}

func windowOf(t *testing.T, s QuotaSnapshot, mins int) QuotaWindow {
	t.Helper()
	for _, w := range s.Windows {
		if w.DurationMins != nil && *w.DurationMins == mins {
			return w
		}
	}
	t.Fatalf("no %d-minute window in %+v", mins, s.Windows)
	return QuotaWindow{}
}

func TestClaudeReadQuotaMapsBothWindows(t *testing.T) {
	future := "2099-01-01T00:00:00.000000+00:00"
	s := fixtureQuota(t, "a", `{"five_hour":{"utilization":30,"resets_at":null},"seven_day":{"utilization":10.5,"resets_at":"`+future+`"}}`)
	if s.Kind != Claude || s.Name != "a" || s.Home != "/acct/a" || s.Source != "claude-oauth-usage" || s.Status != "available" || s.Reason != "" {
		t.Fatalf("snapshot = %+v", s)
	}
	five, seven := windowOf(t, s, 300), windowOf(t, s, 10080)
	if *five.UsedPercent != 30 || *five.RemainingPercent != 70 || five.ResetsAt != nil || five.Status != "available" {
		t.Errorf("five_hour = %+v", five)
	}
	if *seven.UsedPercent != 10.5 || *seven.RemainingPercent != 89.5 || seven.ResetsAt == nil || seven.ResetsAt.Year() != 2099 || seven.Status != "available" {
		t.Errorf("seven_day = %+v", seven)
	}
	if s.Weekly == nil || *s.Weekly.UsedPercent != 10.5 {
		t.Errorf("weekly = %+v", s.Weekly)
	}
}

// Weekly exhaustion with extra usage still live neither parks nor constrains
// the account, so that window must not read as available; the 5-hour window has
// no such escape hatch, and a reached spend limit takes the escape away.
func TestClaudeReadQuotaDiscountsWeeklyOnlyUnderLiveExtraUsage(t *testing.T) {
	const week = `"seven_day":{"utilization":100,"resets_at":"2099-01-01T00:00:00Z"}`
	for _, tc := range []struct {
		name, body, week, five string
	}{
		{"extra usage live", `{"five_hour":{"utilization":20,"resets_at":null},` + week + `,"extra_usage":{"is_enabled":true,"spend_limit_reached":false}}`, "extra-usage", "available"},
		{"spend limit reached", `{"five_hour":{"utilization":1,"resets_at":null},` + week + `,"extra_usage":{"is_enabled":true,"spend_limit_reached":true}}`, "available", "available"},
		{"extra usage off", `{"five_hour":{"utilization":1,"resets_at":null},` + week + `}`, "available", "available"},
		{"five hour has no escape hatch", `{"five_hour":{"utilization":100,"resets_at":"2099-01-01T00:00:00Z"},"seven_day":{"utilization":5,"resets_at":null},"extra_usage":{"is_enabled":true,"spend_limit_reached":false}}`, "available", "available"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := fixtureQuota(t, "a", tc.body)
			if got := windowOf(t, s, 10080).Status; got != tc.week {
				t.Errorf("seven_day status = %q, want %q", got, tc.week)
			}
			if got := windowOf(t, s, 300).Status; got != tc.five {
				t.Errorf("five_hour status = %q, want %q", got, tc.five)
			}
		})
	}
}

// Outside 0..100 is a bad cache, not headroom: the window reads unknown, never
// as a used percent a scheduler could rank.
func TestClaudeReadQuotaUnreadableUtilizationIsUnknownNotHeadroom(t *testing.T) {
	s := fixtureQuota(t, "a", `{"five_hour":{"utilization":150,"resets_at":null},"seven_day":{"utilization":40,"resets_at":null}}`)
	five := windowOf(t, s, 300)
	if five.Status != "unavailable" || five.UsedPercent != nil || five.RemainingPercent != nil {
		t.Errorf("five_hour = %+v, want unavailable with no percent", five)
	}
	if s.Status != "available" || *windowOf(t, s, 10080).UsedPercent != 40 {
		t.Errorf("snapshot = %+v", s)
	}
	if s := fixtureQuota(t, "a", `{"five_hour":{"utilization":-1}}`); s.Status != "unavailable" || s.Reason == "" {
		t.Errorf("no readable window: %+v, want unavailable with a reason", s)
	}
}

func TestClaudeReadQuotaFixtureFailuresNameTheirReason(t *testing.T) {
	if s := fixtureQuota(t, "a", ""); s.Status != "unavailable" || s.Reason != "no fixture" || len(s.Windows) != 0 {
		t.Errorf("no fixture: %+v", s)
	}
	if s := fixtureQuota(t, "a", "not json"); s.Status != "unavailable" || s.Reason != "bad fixture" {
		t.Errorf("bad fixture: %+v", s)
	}
}

// A real read: a failed request is an unavailable snapshot carrying the same
// reason the fetch reports, and it leaves a backoff behind.
func TestClaudeReadQuotaFailureIsUnavailableWithTheFetchReason(t *testing.T) {
	f, cfg, calls := usageRig(t, func(*http.Request) (*http.Response, error) {
		return resp(429, `{}`, map[string]string{"Retry-After": "120"}), nil
	})
	read := func() QuotaSnapshot {
		return claude{}.ReadQuota(context.Background(), QuotaRequest{Name: "a", Account: Account{ConfigDir: cfg}}, f.opts)
	}
	for i := 0; i < 2; i++ {
		s := read()
		if s.Status != "unavailable" || !strings.HasPrefix(s.Reason, "rate limited (retry in ") || s.Home != cfg {
			t.Fatalf("read %d: %+v", i, s)
		}
	}
	if *calls != 1 {
		t.Errorf("requests = %d, want 1 (backoff holds)", *calls)
	}
}

func TestClaudeReadQuotaReadsAccountOverRealEndpointPayload(t *testing.T) {
	f, cfg, _ := usageRig(t, func(*http.Request) (*http.Response, error) { return resp(200, okBody, nil), nil })
	s := claude{}.ReadQuota(context.Background(), QuotaRequest{Name: "a", Account: Account{Home: cfg}}, f.opts)
	if s.Status != "available" || *windowOf(t, s, 300).UsedPercent != 12 || *windowOf(t, s, 10080).UsedPercent != 3 {
		t.Errorf("snapshot = %+v", s)
	}
}

// The ambient login reads ~/.claude under UserHome and reports the dir it read.
func TestClaudeReadQuotaAmbientDefaultsToTheUsersClaudeDir(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, ".claude"), 0o755)
	os.WriteFile(filepath.Join(home, ".claude", ".credentials.json"), []byte(`{"claudeAiOauth":{"accessToken":"t"}}`), 0o600)
	cache := t.TempDir()
	os.WriteFile(filepath.Join(cache, "statusline-usage.json"), []byte(okBody), 0o644)
	s := claude{}.ReadQuota(context.Background(), QuotaRequest{Name: "ambient/current", Ambient: true}, QuotaOptions{UserHome: home, CacheDir: cache})
	if s.Status != "available" || !s.Ambient || s.Home != filepath.Join(home, ".claude") {
		t.Errorf("ambient = %+v", s)
	}
}
