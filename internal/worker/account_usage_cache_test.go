package worker

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

// usageRig is an Accounts with a logged-in config dir, a temp cache dir and a
// counted fake endpoint.
func usageRig(t *testing.T, handler func(*http.Request) (*http.Response, error)) (*Accounts, string, string, *int) {
	t.Helper()
	cfg := t.TempDir()
	os.WriteFile(filepath.Join(cfg, ".credentials.json"), []byte(`{"claudeAiOauth":{"accessToken":"sk-ant-oat-x"}}`), 0o600)
	calls := 0
	cache := t.TempDir()
	acc := &Accounts{
		CacheDir: cache,
		HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			return handler(r)
		})},
	}
	return acc, cfg, cache, &calls
}

func TestUsageFreshCacheIsReadWithoutRequest(t *testing.T) {
	acc, cfg, _, calls := usageRig(t, func(*http.Request) (*http.Response, error) { return resp(200, okBody, nil), nil })
	if err := os.WriteFile(acc.usageCachePath(cfg), []byte(okBody), 0o644); err != nil {
		t.Fatal(err)
	}
	p, why := acc.defaultFetch(context.Background(), "a", cfg)
	if p == nil || why != "" {
		t.Fatalf("payload=%v why=%q", p, why)
	}
	if *calls != 0 {
		t.Errorf("requests = %d, want 0", *calls)
	}
}

func TestUsageStaleCacheFetchesAndWritesCacheAtomically(t *testing.T) {
	acc, cfg, _, calls := usageRig(t, func(*http.Request) (*http.Response, error) { return resp(200, okBody, nil), nil })
	path := acc.usageCachePath(cfg)
	os.WriteFile(path, []byte(`{"stale":1}`), 0o644)
	old := time.Now().Add(-10 * time.Minute)
	os.Chtimes(path, old, old)
	p, why := acc.defaultFetch(context.Background(), "a", cfg)
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
			acc, cfg, _, _ := usageRig(t, func(*http.Request) (*http.Response, error) { return tc.r, nil })
			p, why := acc.defaultFetch(context.Background(), "a", cfg)
			if p != nil || why != tc.want {
				t.Errorf("payload=%v why=%q, want %q", p, why, tc.want)
			}
		})
	}
}

func TestUsageBackoffWrittenAndHonoured(t *testing.T) {
	acc, cfg, _, calls := usageRig(t, func(*http.Request) (*http.Response, error) {
		return resp(429, `{}`, map[string]string{"Retry-After": "120"}), nil
	})
	acc.defaultFetch(context.Background(), "a", cfg)
	if _, err := os.Stat(acc.usageCachePath(cfg) + ".backoff"); err != nil {
		t.Fatalf("no backoff file: %v", err)
	}
	_, why := acc.defaultFetch(context.Background(), "a", cfg)
	if *calls != 1 {
		t.Errorf("requests = %d, want 1 (backoff holds)", *calls)
	}
	if !strings.HasPrefix(why, "rate limited (retry in ") {
		t.Errorf("why = %q", why)
	}
}

func TestUsageReadsBackoffFromHerdrMod(t *testing.T) {
	acc, cfg, _, calls := usageRig(t, func(*http.Request) (*http.Response, error) { return resp(200, okBody, nil), nil })
	// The mod writes the wait in seconds; the file's mtime is when it began.
	bo := acc.usageCachePath(cfg) + ".backoff"
	os.WriteFile(bo, []byte("120\n"), 0o644)
	p, why := acc.defaultFetch(context.Background(), "a", cfg)
	if p != nil || *calls != 0 || !strings.HasPrefix(why, "backing off after a failed request (retry in ") {
		t.Errorf("payload=%v calls=%d why=%q", p, *calls, why)
	}
	// An elapsed backoff no longer holds.
	old := time.Now().Add(-3 * time.Minute)
	os.Chtimes(bo, old, old)
	if p, _ := acc.defaultFetch(context.Background(), "a", cfg); p == nil || *calls != 1 {
		t.Errorf("elapsed backoff still held: payload=%v calls=%d", p, *calls)
	}
	// Success clears the backoff file, as the mod does.
	if _, err := os.Stat(bo); err == nil {
		t.Error("backoff file survived a successful fetch")
	}
}

func TestUsageCachePathSuffix(t *testing.T) {
	acc := &Accounts{CacheDir: "/c"}
	home, _ := os.UserHomeDir()
	if got := acc.usageCachePath(filepath.Join(home, ".claude")); got != "/c/statusline-usage.json" {
		t.Errorf("default dir path = %s", got)
	}
	if got := acc.usageCachePath("/acct/alpha"); got != "/c/statusline-usage-"+sum8("/acct/alpha")+".json" {
		t.Errorf("hashed path = %s", got)
	}
}

func sum8(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])[:8]
}

func TestPickPrefersReadableMeterOverRateLimitedFirst(t *testing.T) {
	acc := &Accounts{Login: loggedIn(), Env: Env{Config: memConfig(t, `{"round":{"accounts":[{"name":"personal","configDir":"/a/p"},{"name":"work","configDir":"/a/w"}]}}`)},
		Fetch: func(_ context.Context, name, _ string) (*jsonx.Object, string) {
			if name == "personal" {
				return nil, "rate limited (retry in 300s)"
			}
			v, _ := jsonx.Decode([]byte(okBody))
			return v.(*jsonx.Object), ""
		}}
	ms := acc.Meters(bg, "unused")
	if got, ok := acc.PickFrom(ms, nil); !ok || got != "work" {
		t.Errorf("PickFrom = %q, %v; want work", got, ok)
	}
	if got, _ := acc.Pick(bg, "unused", nil); got != "work" {
		t.Errorf("Pick = %q; want work", got)
	}
}

func TestUsageWaitsAreCappedAtOneHour(t *testing.T) {
	acc, cfg, _, _ := usageRig(t, func(*http.Request) (*http.Response, error) {
		return resp(429, `{}`, map[string]string{"Retry-After": "999999"}), nil
	})
	if _, why := acc.defaultFetch(context.Background(), "a", cfg); why != "rate limited (retry in 3600s)" {
		t.Errorf("Retry-After not capped: %q", why)
	}
	// A backoff file someone planted with a huge wait is capped the same way.
	os.WriteFile(acc.usageCachePath(cfg)+".backoff", []byte("99999999\n"), 0o644)
	if _, why := acc.defaultFetch(context.Background(), "a", cfg); why != "rate limited (retry in 3600s)" {
		t.Errorf("backoff file not capped: %q", why)
	}
}

func TestUsageIgnoresFilesOwnedByAnotherUser(t *testing.T) {
	acc, cfg, _, calls := usageRig(t, func(*http.Request) (*http.Response, error) { return resp(200, okBody, nil), nil })
	acc.ownedByMe = func(os.FileInfo) bool { return false }
	os.WriteFile(acc.usageCachePath(cfg), []byte(okBody), 0o644)
	os.WriteFile(acc.usageCachePath(cfg)+".backoff", []byte("120\n"), 0o644)
	if p, _ := acc.defaultFetch(context.Background(), "a", cfg); p == nil || *calls != 1 {
		t.Errorf("foreign-owned cache or backoff was trusted: payload=%v calls=%d", p, *calls)
	}
}

func TestUsageBackoffKeepsTheRealReason(t *testing.T) {
	acc, cfg, _, calls := usageRig(t, func(*http.Request) (*http.Response, error) { return resp(401, `{}`, nil), nil })
	if _, why := acc.defaultFetch(context.Background(), "a", cfg); why != "login expired" {
		t.Fatalf("first why = %q", why)
	}
	if _, why := acc.defaultFetch(context.Background(), "a", cfg); why != "login expired" || *calls != 1 {
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
			acc, cfg, _, calls := usageRig(t, tc.h)
			acc.defaultFetch(context.Background(), "a", cfg)
			b, err := os.ReadFile(acc.usageCachePath(cfg) + ".backoff")
			if err != nil || strings.TrimSpace(string(b)) != "300" {
				t.Fatalf("backoff = %q, %v", b, err)
			}
			if _, why := acc.defaultFetch(context.Background(), "a", cfg); *calls != 1 || why != tc.want {
				t.Errorf("held: calls=%d why=%q, want %q", *calls, why, tc.want)
			}
		})
	}
}

func TestUsageRetryAfterStoredAsGiven(t *testing.T) {
	acc, cfg, _, _ := usageRig(t, func(*http.Request) (*http.Response, error) {
		return resp(429, `{}`, map[string]string{"Retry-After": "7200"}), nil
	})
	acc.defaultFetch(context.Background(), "a", cfg)
	b, _ := os.ReadFile(acc.usageCachePath(cfg) + ".backoff")
	if strings.TrimSpace(string(b)) != "7200" {
		t.Errorf("stored wait = %q, want 7200", b)
	}
}

func TestUsageSymlinkedFilesAreNotFollowed(t *testing.T) {
	acc, cfg, _, calls := usageRig(t, func(*http.Request) (*http.Response, error) { return resp(200, okBody, nil), nil })
	target := filepath.Join(t.TempDir(), "t")
	os.WriteFile(target, []byte(okBody), 0o644)
	cache := acc.usageCachePath(cfg)
	if err := os.Symlink(target, cache); err != nil {
		t.Fatal(err)
	}
	bo := t.TempDir() + "/b"
	os.WriteFile(bo, []byte("120\n"), 0o644)
	os.Symlink(bo, cache+".backoff")
	if p, _ := acc.defaultFetch(context.Background(), "a", cfg); p == nil || *calls != 1 {
		t.Errorf("symlinked cache or backoff was followed: payload=%v calls=%d", p, *calls)
	}
}

func TestWindowRejectsOutOfRangeUtilization(t *testing.T) {
	for _, u := range []string{"150", "-1", "100.5"} {
		v, _ := jsonx.Decode([]byte(`{"five_hour":{"utilization":` + u + `}}`))
		if got, _ := window(v.(*jsonx.Object), "five_hour"); got != nil {
			t.Errorf("utilization %s read as %v, want unknown", u, *got)
		}
	}
	v, _ := jsonx.Decode([]byte(`{"five_hour":{"utilization":0},"seven_day":{"utilization":100}}`))
	for _, k := range []string{"five_hour", "seven_day"} {
		if got, _ := window(v.(*jsonx.Object), k); got == nil {
			t.Errorf("%s boundary value rejected", k)
		}
	}
}

func TestUsageFifoAtCacheOrBackoffPathReturnsPromptly(t *testing.T) {
	for _, suffix := range []string{"", ".backoff"} {
		t.Run("fifo"+suffix, func(t *testing.T) {
			acc, cfg, _, calls := usageRig(t, func(*http.Request) (*http.Response, error) { return resp(200, okBody, nil), nil })
			fifo := acc.usageCachePath(cfg) + suffix
			if err := syscall.Mkfifo(fifo, 0o644); err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			go func() {
				acc.defaultFetch(context.Background(), "a", cfg)
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				// Release the blocked open so the goroutine ends, then fail.
				if f, err := os.OpenFile(fifo, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
					f.Close()
				}
				t.Fatal("fetch blocked on a FIFO")
			}
			if *calls != 1 {
				t.Errorf("requests = %d, want 1 (FIFO unreadable)", *calls)
			}
		})
	}
}
