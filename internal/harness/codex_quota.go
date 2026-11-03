package harness

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os/exec"
	"path/filepath"
	"reflect"
	"time"

	"github.com/l4ci/rota/internal/proc"
)

const weeklyMinutes = 7 * 24 * 60

// ReadQuota delegates authentication to the installed CLI. No login or model
// turns are sent, and neither stderr nor backend error messages reach reports.
func (codex) ReadQuota(ctx context.Context, req QuotaRequest, opts QuotaOptions) QuotaSnapshot {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	s := QuotaSnapshot{Kind: Codex, Name: req.Name, Home: req.Account.Home, Ambient: req.Ambient, Source: "codex-app-server", IdentityStatus: "unavailable", Status: "unavailable", ObservedAt: now().UTC(), Windows: []QuotaWindow{}}
	fail := func(reason string) QuotaSnapshot {
		s.Status = "unavailable"
		s.Reason = reason
		s.Weekly = nil
		s.Windows = []QuotaWindow{}
		return s
	}
	if s.Home == "" {
		if !req.Ambient {
			return fail("named account home missing")
		}
		if opts.UserHome == "" {
			return fail("ambient account home unavailable")
		}
		s.Home = filepath.Join(opts.UserHome, ".codex")
	}
	if !filepath.IsAbs(s.Home) {
		return fail("account home must be absolute")
	}
	exchange := opts.Exchange
	if exchange == nil {
		exchange = proc.Duplex
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var payload json.RawMessage
	err := exchange(ctx, proc.Cmd{Name: Codex, Args: []string{"app-server", "--listen", "stdio://"}, Dir: opts.UserHome, Env: []string{"CODEX_HOME=" + s.Home}, Timeout: proc.NoTimeout, Group: true, MaxOutput: 1024 * 1024}, func(r io.Reader, w io.Writer) error {
		rpc := quotaRPC{scan: bufio.NewScanner(r), out: json.NewEncoder(w)}
		rpc.scan.Buffer(make([]byte, 4096), 256*1024)
		if _, err := rpc.call(1, "initialize", map[string]any{"clientInfo": map[string]string{"name": "rota-quota", "version": "1"}}); err != nil {
			return err
		}
		if err := rpc.out.Encode(map[string]any{"method": "initialized"}); err != nil {
			return err
		}
		account, err := rpc.call(2, "account/read", map[string]any{"refreshToken": false})
		if err != nil {
			return err
		}
		var a struct {
			Account *struct {
				Type string `json:"type"`
			} `json:"account"`
		}
		if json.Unmarshal(account, &a) != nil {
			return quotaFailure("malformed account response")
		}
		if a.Account == nil || a.Account.Type == "apiKey" {
			return quotaFailure("subscription authentication unavailable")
		}
		payload, err = rpc.call(3, "account/rateLimits/read", nil)
		return err
	})
	s.ObservedAt = now().UTC()
	if err != nil {
		var failure quotaFailure
		switch {
		case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
			return fail("quota read timed out or canceled")
		case errors.Is(err, exec.ErrNotFound):
			return fail("Codex binary unavailable")
		case errors.Is(err, proc.ErrOutputLimit):
			return fail("quota response exceeds output limit")
		case errors.As(err, &failure):
			return fail(string(failure))
		default:
			return fail("app-server unavailable or exchange failed")
		}
	}
	return parseCodexQuota(s, payload)
}

type quotaFailure string

func (e quotaFailure) Error() string { return string(e) }

type quotaRPC struct {
	scan     *bufio.Scanner
	out      *json.Encoder
	messages int
}

func (r *quotaRPC) call(id int, method string, params any) (json.RawMessage, error) {
	if err := r.out.Encode(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		return nil, err
	}
	for r.scan.Scan() {
		r.messages++
		if r.messages > 64 {
			return nil, quotaFailure("quota response message limit exceeded")
		}
		var m struct {
			ID     *int            `json:"id"`
			Method string          `json:"method"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code int `json:"code"`
			} `json:"error"`
		}
		if json.Unmarshal(r.scan.Bytes(), &m) != nil {
			return nil, quotaFailure("malformed app-server response")
		}
		if m.Method == "account/updated" {
			return nil, quotaFailure("account changed during quota read")
		}
		if m.ID == nil || *m.ID != id {
			continue
		}
		if m.Error != nil {
			if m.Error.Code == -32601 {
				return nil, quotaFailure("app-server quota RPC unsupported")
			}
			return nil, quotaFailure("app-server quota RPC failed (authentication or source unavailable)")
		}
		if len(m.Result) == 0 || string(m.Result) == "null" {
			return nil, quotaFailure("missing app-server result")
		}
		return m.Result, nil
	}
	if r.scan.Err() != nil {
		return nil, quotaFailure("malformed or oversized app-server response")
	}
	return nil, quotaFailure("app-server closed before quota response")
}

type codexQuotaWindow struct {
	Used     *float64 `json:"usedPercent"`
	Duration *int     `json:"windowDurationMins"`
	Reset    *int64   `json:"resetsAt"`
}
type codexQuotaBucket struct {
	ID                 string `json:"limitId"`
	Primary, Secondary *codexQuotaWindow
}

func parseCodexQuota(s QuotaSnapshot, data []byte) QuotaSnapshot {
	fail := func(reason string) QuotaSnapshot { s.Status = "unavailable"; s.Reason = reason; return s }
	var response struct {
		AccountID *string                      `json:"accountId"`
		Buckets   map[string]*codexQuotaBucket `json:"rateLimitsByLimitId"`
		Legacy    *codexQuotaBucket            `json:"rateLimits"`
	}
	if json.Unmarshal(data, &response) != nil {
		return fail("malformed quota fields")
	}
	var bucket *codexQuotaBucket
	if response.Buckets != nil {
		bucket = response.Buckets[Codex]
		s.SourceDetail = "rateLimitsByLimitId.codex"
	} else {
		bucket = response.Legacy
		s.SourceDetail = "legacy rateLimits"
	}
	if bucket == nil {
		return fail("Codex quota bucket unavailable")
	}
	if bucket.ID != "" && bucket.ID != Codex {
		return fail("conflicting quota bucket identity")
	}
	if response.AccountID != nil && *response.AccountID != "" {
		h := sha256.Sum256([]byte(*response.AccountID))
		s.AccountRef = "sha256:" + hex.EncodeToString(h[:12])
		s.IdentityStatus = "reported"
	}
	var weekly *codexQuotaWindow
	for _, raw := range []*codexQuotaWindow{bucket.Primary, bucket.Secondary} {
		if raw == nil {
			continue
		}
		w := codexWindow(raw, s.ObservedAt)
		s.Windows = append(s.Windows, w)
		if raw.Duration == nil || *raw.Duration != weeklyMinutes {
			continue
		}
		if weekly != nil && !reflect.DeepEqual(weekly, raw) {
			s.Weekly = nil
			return fail("ambiguous weekly quota windows")
		}
		weekly = raw
		ww := w
		s.Weekly = &ww
	}
	if s.Weekly == nil {
		return fail("weekly window unavailable (requires duration 10080 minutes)")
	}
	s.Status, s.Reason = s.Weekly.Status, s.Weekly.Reason
	return s
}

func codexWindow(raw *codexQuotaWindow, now time.Time) QuotaWindow {
	w := QuotaWindow{DurationMins: raw.Duration, Status: "available"}
	if raw.Used == nil {
		w.Status = "unavailable"
		w.Reason = "usage percentage missing"
	} else if math.IsNaN(*raw.Used) || math.IsInf(*raw.Used, 0) || *raw.Used < 0 {
		w.Status = "unavailable"
		w.Reason = "invalid usage percentage"
	} else {
		w.UsedPercent = raw.Used
		remaining := math.Max(0, 100-*raw.Used)
		w.RemainingPercent = &remaining
	}
	if raw.Duration == nil || *raw.Duration <= 0 {
		w.Status = "unavailable"
		w.Reason = "window duration unavailable"
	}
	if raw.Reset == nil {
		if w.Status == "available" {
			w.Status = "partial"
			w.Reason = "reset time unavailable"
		}
	} else if *raw.Reset <= 0 || *raw.Reset > 253402300799 {
		w.Status = "unavailable"
		w.Reason = "invalid reset timestamp"
	} else {
		ts := time.Unix(*raw.Reset, 0).UTC()
		w.ResetsAt = &ts
		if !ts.After(now) && w.Status == "available" {
			w.Status = "stale"
			w.Reason = "reset elapsed; a new source reading is required"
		}
	}
	return w
}
