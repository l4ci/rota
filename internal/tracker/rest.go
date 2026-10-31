package tracker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/strutil"
)

// REST is the HTTP layer of the trackers rota reaches over HTTP instead of a
// CLI (Jira; Linear's GraphQL endpoint is one POST through Do or ReadPost). It
// knows JSON, auth, retries and the status-to-Kind mapping, nothing about any
// one tracker's schema: a 429 waits its Retry-After and retries, a 5xx on an
// idempotent method backs off and retries, and a wait longer than MaxWait
// gives up as rate limited rather than sleep for an hour. A POST is never
// resent after a 5xx: the server may have made the comment or issue already.
type REST struct {
	Base string // scheme and host, plus any path prefix; Do appends path
	// Auth sets the request's credentials (an Authorization header).
	Auth   func(*http.Request)
	Client *http.Client // nil: a client with RESTTimeout
	// Sleep waits between attempts and returns early with ctx's error; nil
	// waits on a timer.
	Sleep func(ctx context.Context, d time.Duration) error
	Now   func() time.Time // reads an HTTP-date Retry-After; nil is time.Now
	// MaxBody caps a response body; 0 is restMaxBody.
	MaxBody int64
	// MaxWait caps one Retry-After wait; 0 is restMaxWait.
	MaxWait time.Duration
	// Classify reads a tracker's own error body on a non-2xx answer; nil, or
	// a nil result, keeps the status-based mapping.
	Classify func(method, path string, status int, raw []byte) *Error
}

const (
	// RESTTimeout bounds one HTTP attempt when REST.Client is nil.
	RESTTimeout = time.Minute
	restRetries = 3
	restMaxWait = 2 * time.Minute
	restBackoff = time.Second
	restMaxBody = 32 << 20
)

var defaultRESTClient = &http.Client{Timeout: RESTTimeout}

// Do sends in (nil: no body) as JSON to Base+path and decodes the response
// into out (nil: discard it). A failure is a tracker *Error: 401 and 403 are
// KindUnavailable (the token), 404 KindNotFound, a 429 that outlasts the
// retries or the cap KindRateLimited, a transport failure KindUnavailable.
func (r *REST) Do(ctx context.Context, method, path string, in, out any) error {
	return r.do(ctx, method, path, in, out, idempotent(method))
}

// ReadPost is Do for a POST the caller vouches only reads (a GraphQL query):
// repeating it changes nothing, so a 5xx is retried as for a GET. The caller
// owns that call; REST does not look at the body. A write goes through Do,
// which never resends a POST.
func (r *REST) ReadPost(ctx context.Context, path string, in, out any) error {
	return r.do(ctx, http.MethodPost, path, in, out, true)
}

func (r *REST) do(ctx context.Context, method, path string, in, out any, idem bool) error {
	var body []byte
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return internal("encoding the request: %v", err)
		}
		body = b
	}
	for attempt := 0; ; attempt++ {
		status, hdr, raw, err := r.send(ctx, method, path, body)
		if err != nil {
			return err
		}
		if status < 300 {
			if out == nil || len(bytes.TrimSpace(raw)) == 0 {
				return nil
			}
			if err := json.Unmarshal(raw, out); err != nil {
				return failed("unparseable tracker output: %q", strutil.Clip(string(raw), clipLen))
			}
			return nil
		}
		retryable := status == http.StatusTooManyRequests || status >= 500 && idem
		if !retryable || attempt >= restRetries {
			if r.Classify != nil {
				if e := r.Classify(method, path, status, raw); e != nil {
					return e
				}
			}
			return restError(method, path, status, raw)
		}
		wait := restBackoff << attempt
		if after, ok := retryAfter(hdr.Get("Retry-After"), r.now()); ok {
			wait = after
		}
		if wait > r.maxWait() {
			return rateLimited("%s %s: rate limited, the tracker asks to wait %s (more than %s)", method, path, wait, r.maxWait())
		}
		if err := r.sleep(ctx, wait); err != nil {
			return internal("%s %s: %v", method, path, err)
		}
	}
}

func (r *REST) send(ctx context.Context, method, path string, body []byte) (int, http.Header, []byte, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(r.Base, "/")+path, rd)
	if err != nil {
		return 0, nil, nil, internal("building the request: %v", err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if r.Auth != nil {
		r.Auth(req)
	}
	client := r.Client
	if client == nil {
		client = defaultRESTClient
	}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return 0, nil, nil, internal("%s %s: %v", method, path, ctx.Err())
		}
		return 0, nil, nil, Unavailable("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	limit := r.MaxBody
	if limit <= 0 {
		limit = restMaxBody
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return 0, nil, nil, Unavailable("%s %s: reading the response: %v", method, path, err)
	}
	if int64(len(raw)) > limit {
		return 0, nil, nil, failed("%s %s: the response is larger than %d bytes", method, path, limit)
	}
	return resp.StatusCode, resp.Header, raw, nil
}

func (r *REST) maxWait() time.Duration {
	if r.MaxWait > 0 {
		return r.MaxWait
	}
	return restMaxWait
}

func (r *REST) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *REST) sleep(ctx context.Context, d time.Duration) error {
	if r.Sleep != nil {
		return r.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// idempotent methods may be resent after a 5xx: repeating them changes nothing.
func idempotent(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPut, http.MethodDelete:
		return true
	}
	return false
}

// retryAfter reads a Retry-After header: delay seconds or an HTTP date.
func retryAfter(v string, now time.Time) (time.Duration, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, false
	}
	if n, err := strconv.Atoi(v); err == nil && n >= 0 {
		return time.Duration(n) * time.Second, true
	}
	if t, err := http.ParseTime(v); err == nil {
		return max(0, t.Sub(now)), true
	}
	return 0, false
}

func restError(method, path string, status int, raw []byte) *Error {
	msg := fmt.Sprintf("%s %s: HTTP %d", method, path, status)
	if detail := strings.TrimSpace(string(raw)); detail != "" {
		msg += ": " + strutil.Clip(detail, clipLen)
	}
	switch {
	case status == http.StatusUnauthorized:
		return Unavailable("%s (check the connection's token: rota connect test)", msg)
	case status == http.StatusForbidden:
		return Unavailable("%s (check the connection's token or its permission on this issue: rota connect test)", msg)
	case status == http.StatusNotFound:
		return &Error{Kind: KindNotFound, Code: 1, Message: msg}
	case status == http.StatusTooManyRequests:
		return rateLimited("%s", msg)
	}
	return failed("%s", msg)
}

// errRepeatedCursor stops a pager whose tracker hands back a cursor it gave before.
var errRepeatedCursor = errors.New("the tracker returned the same page cursor twice")

// Pages runs a paged read: fetch gets "" first, then each cursor it returned,
// until it returns "". An offset pager returns the next offset as text, a
// token pager its next-page token. A cursor seen before is a failure, never a
// loop.
func Pages(ctx context.Context, fetch func(cursor string) (next string, err error)) error {
	seen := map[string]bool{}
	cursor := ""
	for {
		if err := ctx.Err(); err != nil {
			return internal("%v", err)
		}
		next, err := fetch(cursor)
		if err != nil {
			return err
		}
		if next == "" {
			return nil
		}
		if seen[next] {
			return failed("%v: %q", errRepeatedCursor, next)
		}
		seen[next] = true
		cursor = next
	}
}
