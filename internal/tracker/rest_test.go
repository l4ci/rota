package tracker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

// restFor is a REST client on srv whose waits are recorded, not slept.
func restFor(srv *httptest.Server, waits *[]time.Duration) *REST {
	return &REST{Base: srv.URL, Sleep: func(_ context.Context, d time.Duration) error {
		*waits = append(*waits, d)
		return nil
	}}
}

func TestRESTRetriesA429AfterRetryAfter(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"ok": "yes"})
	}))
	defer srv.Close()
	var waits []time.Duration
	var out struct{ OK string }
	if err := restFor(srv, &waits).Do(context.Background(), "GET", "/x", nil, &out); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || out.OK != "yes" {
		t.Fatalf("calls=%d out=%+v, want a retry that succeeds", calls, out)
	}
	if len(waits) != 1 || waits[0] != 7*time.Second {
		t.Fatalf("waits=%v, want [7s] from Retry-After", waits)
	}
}

func TestRESTGivesUpOnARateLimitLongerThanTheCap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "3600")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	var waits []time.Duration
	err := restFor(srv, &waits).Do(context.Background(), "GET", "/x", nil, nil)
	if !IsKind(err, KindRateLimited) {
		t.Fatalf("err=%v, want KindRateLimited", err)
	}
	if len(waits) != 0 {
		t.Fatalf("waits=%v, want no sleep past the cap", waits)
	}
}

func TestRESTBacksOffOn5xxAndResendsTheBody(t *testing.T) {
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in map[string]string
		_ = json.NewDecoder(r.Body).Decode(&in)
		bodies = append(bodies, in["v"])
		if len(bodies) < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	var waits []time.Duration
	if err := restFor(srv, &waits).Do(context.Background(), "PUT", "/x", map[string]string{"v": "a"}, nil); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 3 || bodies[2] != "a" {
		t.Fatalf("bodies=%v, want the body on every attempt", bodies)
	}
	if len(waits) != 2 || waits[1] <= waits[0] {
		t.Fatalf("waits=%v, want a growing backoff", waits)
	}
}

func TestRESTMapsStatusToKind(t *testing.T) {
	for _, tc := range []struct {
		status int
		kind   Kind
	}{{401, KindUnavailable}, {403, KindUnavailable}, {404, KindNotFound}, {400, KindFailed}, {500, KindFailed}} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(`{"errorMessages":["nope"]}`))
		}))
		var waits []time.Duration
		err := restFor(srv, &waits).Do(context.Background(), "GET", "/x", nil, nil)
		srv.Close()
		if !IsKind(err, tc.kind) {
			t.Errorf("status %d: err=%v, want kind %d", tc.status, err, tc.kind)
		}
	}
}

func TestRESTSendsAuthAndJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" || r.Header.Get("Accept") != "application/json" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Method == "POST" && r.Header.Get("Content-Type") != "application/json" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	var waits []time.Duration
	c := restFor(srv, &waits)
	c.Auth = func(r *http.Request) { r.Header.Set("Authorization", "Bearer tok") }
	if err := c.Do(context.Background(), "POST", "/x", map[string]int{"a": 1}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestPagesFollowsCursorsUntilEmpty(t *testing.T) {
	var seen []string
	err := Pages(context.Background(), func(cursor string) (string, error) {
		seen = append(seen, cursor)
		if n, _ := strconv.Atoi(cursor); n < 2 {
			return strconv.Itoa(n + 1), nil
		}
		return "", nil
	})
	if err != nil || len(seen) != 3 || seen[0] != "" || seen[2] != "2" {
		t.Fatalf("seen=%q err=%v, want three pages from an empty cursor", seen, err)
	}
}

func TestPagesStopsOnARepeatedCursor(t *testing.T) {
	calls := 0
	err := Pages(context.Background(), func(string) (string, error) {
		calls++
		return "same", nil
	})
	if err == nil || calls != 2 {
		t.Fatalf("calls=%d err=%v, want a failure once a cursor repeats", calls, err)
	}
}

func TestRESTDoesNotResendAPostOn5xx(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	var waits []time.Duration
	err := restFor(srv, &waits).Do(context.Background(), "POST", "/comment", map[string]string{"body": "x"}, nil)
	if !IsKind(err, KindFailed) || calls != 1 || len(waits) != 0 {
		t.Fatalf("calls=%d waits=%v err=%v, want one POST and no retry: the server may have made the comment", calls, waits, err)
	}
}

func TestRESTRetriesAPostOn429(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	var waits []time.Duration
	if err := restFor(srv, &waits).Do(context.Background(), "POST", "/comment", nil, nil); err != nil || calls != 2 {
		t.Fatalf("calls=%d err=%v, want a 429 POST retried", calls, err)
	}
}

func TestRESTWaitStopsWhenTheContextEnds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := (&REST{Base: srv.URL}).Do(ctx, "GET", "/x", nil, nil)
	if !IsKind(err, KindInternal) || time.Since(start) > 5*time.Second {
		t.Fatalf("err=%v after %s, want a cancelled wait that returns at once", err, time.Since(start))
	}
}

func TestRESTFailsOnAnOversizedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(make([]byte, 2048))
	}))
	defer srv.Close()
	var waits []time.Duration
	c := restFor(srv, &waits)
	c.MaxBody = 1024
	if err := c.Do(context.Background(), "GET", "/x", nil, nil); !IsKind(err, KindFailed) {
		t.Fatalf("err=%v, want KindFailed for a body past MaxBody", err)
	}
}

func TestRESTRetryAfterForms(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		header string
		want   time.Duration // the one wait slept before the retry
	}{
		{now.Add(30 * time.Second).Format(http.TimeFormat), 30 * time.Second},
		{now.Add(-time.Minute).Format(http.TimeFormat), 0},
		{"soon", restBackoff},
		{"-5", restBackoff},
		{"120", 120 * time.Second}, // exactly the cap still waits
	} {
		calls := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			if calls == 1 {
				w.Header().Set("Retry-After", tc.header)
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		var waits []time.Duration
		c := restFor(srv, &waits)
		c.Now = func() time.Time { return now }
		c.MaxWait = 120 * time.Second
		err := c.Do(context.Background(), "GET", "/x", nil, nil)
		srv.Close()
		if err != nil || len(waits) != 1 || waits[0] != tc.want {
			t.Errorf("Retry-After %q: waits=%v err=%v, want [%s]", tc.header, waits, err, tc.want)
		}
	}
}

func TestRESTGivesUpOneSecondPastTheCap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "121")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	var waits []time.Duration
	c := restFor(srv, &waits)
	c.MaxWait = 120 * time.Second
	if err := c.Do(context.Background(), "GET", "/x", nil, nil); !IsKind(err, KindRateLimited) || len(waits) != 0 {
		t.Fatalf("waits=%v err=%v, want an immediate rate-limit failure", waits, err)
	}
}

func TestRESTRateLimitExhaustsTheRetries(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	var waits []time.Duration
	err := restFor(srv, &waits).Do(context.Background(), "GET", "/x", nil, nil)
	if !IsKind(err, KindRateLimited) || calls != restRetries+1 || len(waits) != restRetries {
		t.Fatalf("calls=%d waits=%v err=%v, want %d attempts then KindRateLimited", calls, waits, err, restRetries+1)
	}
}
