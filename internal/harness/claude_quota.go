package harness

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/stamp"
)

// Claude's account usage. Each slot can run under its own CLAUDE_CONFIG_DIR,
// which carries its own OAuth credentials, so "how much headroom does this
// account have" is a per-config-dir question. Three sources, tried in order,
// each degrading honestly rather than guessing:
//  1. The OAuth usage endpoint, authenticated with the account's own
//     credentials, behind the status-line cache Claude Code and the herdr
//     limits mod share (5 minutes) and a backoff after a failed request.
//     Works with NO session running, which is what makes pre-dispatch
//     balancing possible.
//  2. A fixture payload (ROTA_ACCOUNT_USAGE_DIR) for offline tests.
//  3. An unavailable snapshot, which callers treat as "rotate", never as
//     "free" and never as "exhausted".
//
// Three distinctions the reader must not collapse: five_hour and seven_day are
// SEPARATE windows with staggered resets; `utilization: 0` with `resets_at:
// null` means nothing spent, which is not the same as no data; and seven_day
// at 100% does NOT make an account unusable when extra_usage is enabled and
// spend_limit_reached is false (that window reports claudeExtraUsage, so a
// scheduling meter discounts it).

const (
	claudeUsageURL   = "https://api.anthropic.com/api/oauth/usage"
	claudeUsageDir   = "/tmp/claude"
	claudeUsageSrc   = "claude-oauth-usage"
	claudeFiveHour   = 5 * 60
	claudeSevenDay   = 7 * 24 * 60
	claudeExtraUsage = "extra-usage"
)

// ReadQuota reads the usage of the account behind req.Account (its ConfigDir,
// else its Home; empty means ~/.claude). It never refreshes credentials.
func (claude) ReadQuota(ctx context.Context, req QuotaRequest, opts QuotaOptions) QuotaSnapshot {
	f := newClaudeFetch(opts)
	dir := f.expand(firstNonEmpty(req.Account.ConfigDir, req.Account.Home))
	s := QuotaSnapshot{Kind: Claude, Name: req.Name, Home: dir, Ambient: req.Ambient, Source: claudeUsageSrc, IdentityStatus: "unavailable", Status: "unavailable", ObservedAt: f.now(), Windows: []QuotaWindow{}}
	payload, why := f.defaultFetch(ctx, req.Name, dir)
	if payload == nil {
		s.Reason = why
		return s
	}
	extra, _ := payload.Get("extra_usage")
	eo, _ := extra.(*jsonx.Object)
	extraLive := false
	if eo != nil {
		en, _ := eo.Get("is_enabled")
		lim, _ := eo.Get("spend_limit_reached")
		extraLive = jsonx.Truthy(en) && !jsonx.Truthy(lim)
	}
	readable := false
	for _, k := range []struct {
		key  string
		mins int
	}{{"five_hour", claudeFiveHour}, {"seven_day", claudeSevenDay}} {
		util, reset, present := claudeWindow(payload, k.key)
		if !present {
			continue
		}
		mins := k.mins
		w := QuotaWindow{DurationMins: &mins, ResetsAt: reset, Status: "available"}
		switch {
		case util == nil:
			// Outside 0..100 is a bad cache, not headroom: leave it unknown.
			w.Status, w.Reason = "unavailable", "utilization unreadable"
		default:
			readable = true
			rem := math.Max(0, 100-*util)
			w.UsedPercent, w.RemainingPercent = util, &rem
			// Weekly exhaustion with extra usage still live neither parks the
			// account nor constrains it. The 5-hour window has no such escape hatch.
			if k.key == "seven_day" && *util >= 100 && extraLive {
				w.Status, w.Reason = claudeExtraUsage, "weekly limit spent, extra usage live"
			}
		}
		s.Windows = append(s.Windows, w)
		if k.key == "seven_day" {
			wk := w
			s.Weekly = &wk
		}
	}
	if !readable {
		s.Reason = "no readable utilization"
		return s
	}
	s.Status = "available"
	return s
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// claudeFetch is one read's seams, QuotaOptions with the zero values resolved.
type claudeFetch struct {
	opts  QuotaOptions
	login Credentials
}

func newClaudeFetch(opts QuotaOptions) *claudeFetch {
	f := &claudeFetch{opts: opts}
	if f.opts.Now == nil {
		f.opts.Now = time.Now
	}
	f.login = opts.Login
	if f.login.Home == nil {
		home := opts.UserHome
		f.login.Home = func() (string, error) { return home, nil }
	}
	if f.login.Now == nil {
		f.login.Now = f.now
	}
	return f
}

func (f *claudeFetch) now() time.Time { return f.opts.Now().UTC() }

func (f *claudeFetch) getenv(k string) string {
	if f.opts.Getenv == nil {
		return ""
	}
	return f.opts.Getenv(k)
}

// expand is the cleaned absolute form of a config dir against the user home;
// empty means ~/.claude, what Claude Code uses when CLAUDE_CONFIG_DIR is unset.
func (f *claudeFetch) expand(d string) string {
	if d == "" {
		d = "~/.claude"
	}
	return config.ExpandDir(d, f.opts.UserHome)
}

// mine reports whether a file belongs to the current user. /tmp/claude is
// shared and world-writable, so a cache or backoff another user planted is
// never trusted.
func (f *claudeFetch) mine(st os.FileInfo) bool {
	if f.opts.OwnedByMe != nil {
		return f.opts.OwnedByMe(st)
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	return ok && int(sys.Uid) == os.Getuid()
}

// usageCachePath is the status-line usage cache of the account whose config
// dir is configDir (already expanded): statusline-usage.json for ~/.claude,
// else the file suffixed with the first 8 hex chars of sha256(dir), the name
// Claude Code and the herdr mod both use.
func (f *claudeFetch) usageCachePath(configDir string) string {
	dir := f.opts.CacheDir
	if dir == "" {
		dir = claudeUsageDir
	}
	suffix := ""
	if configDir != f.expand("") {
		sum := sha256.Sum256([]byte(configDir))
		suffix = "-" + hex.EncodeToString(sum[:])[:8]
	}
	return filepath.Join(dir, "statusline-usage"+suffix+".json")
}

func claudeWindow(p *jsonx.Object, key string) (util *float64, reset *time.Time, present bool) {
	v, _ := p.Get(key)
	w, _ := v.(*jsonx.Object)
	if w == nil {
		return nil, nil, false
	}
	if u, ok := w.Get("utilization"); ok {
		if n, ok := u.(json.Number); ok {
			if f, err := n.Float64(); err == nil && f >= 0 && f <= 100 {
				util = &f
			}
		}
	}
	r, _ := w.Get("resets_at")
	return util, parseReset(r), true
}

func parseReset(v any) *time.Time {
	s, ok := v.(string)
	if !ok || s == "" {
		return nil
	}
	return stamp.ParseISO(s)
}

// defaultFetch reads ROTA_ACCOUNT_USAGE_DIR/<name>.json when set, else the OAuth
// usage endpoint with the account's own access token.
func (f *claudeFetch) defaultFetch(ctx context.Context, name, configDir string) (*jsonx.Object, string) {
	if dir := f.getenv("ROTA_ACCOUNT_USAGE_DIR"); dir != "" {
		b, err := os.ReadFile(filepath.Join(dir, name+".json"))
		if err != nil {
			return nil, "no fixture"
		}
		v, err := jsonx.Decode(b)
		o, ok := v.(*jsonx.Object)
		if err != nil || !ok {
			return nil, "bad fixture"
		}
		return o, ""
	}
	return f.fetchUsage(ctx, configDir)
}

// usageMaxAge is how long the shared usage cache counts as fresh.
const usageMaxAge = 5 * time.Minute

// defaultBackoff is the wait after a failed request without a Retry-After;
// maxBackoff caps a wait when it is read, never when it is written, so the
// file keeps what the server said.
const (
	defaultBackoff = 300 * time.Second
	maxBackoff     = time.Hour
)

// backoffOf reads a wait in seconds as written: the default for anything
// unusable. It does not cap; backoffLeft does.
func backoffOf(s string) time.Duration {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n <= 0 || n > math.MaxInt32 {
		return defaultBackoff
	}
	return time.Duration(n) * time.Second
}

// reasonSkew is how far apart our backoff and its .reason sidecar may be
// written; a backoff rewritten later (by the herdr mod) orphans the reason.
const reasonSkew = 2 * time.Second

// openOwned opens path without following a symlink and returns its bytes and
// mtime, both taken from the one open file. A file that is not a regular file
// of the current user reads as absent: /tmp/claude is shared and world-writable.
func (f *claudeFetch) openOwned(path string) ([]byte, time.Time, bool) {
	fh, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, time.Time{}, false
	}
	defer fh.Close()
	st, err := fh.Stat()
	if err != nil || !st.Mode().IsRegular() || !f.mine(st) {
		return nil, time.Time{}, false
	}
	b, err := io.ReadAll(io.LimitReader(fh, 1<<20))
	if err != nil {
		return nil, time.Time{}, false
	}
	return b, st.ModTime(), true
}

// fetchUsage reads the usage of the account behind configDir (already
// expanded). A cache under five minutes old answers without a request; a
// backoff left by a failed request (ours or the herdr mod's) holds further
// requests until it passes.
func (f *claudeFetch) fetchUsage(ctx context.Context, configDir string) (*jsonx.Object, string) {
	cache := f.usageCachePath(configDir)
	if b, mt, ok := f.openOwned(cache); ok && f.now().Sub(mt) < usageMaxAge {
		if o := parseUsage(b); o != nil {
			return o, ""
		}
	}
	if left, why := f.backoffLeft(cache); left > 0 {
		return nil, why
	}
	token, why := f.token(configDir)
	if token == "" {
		return nil, why
	}
	client := f.opts.HTTP
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, claudeUsageURL, nil)
	if err != nil {
		return nil, "usage endpoint unreachable"
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	resp, err := client.Do(req)
	if err != nil {
		return f.failed(cache, "", reasonUnreachable)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		retryAfter := resp.Header.Get("Retry-After")
		switch resp.StatusCode {
		case http.StatusTooManyRequests:
			return f.failed(cache, retryAfter, reasonRateLimited)
		case http.StatusUnauthorized, http.StatusForbidden:
			return f.failed(cache, retryAfter, "login expired")
		}
		return f.failed(cache, retryAfter, fmt.Sprintf("usage endpoint returned %d", resp.StatusCode))
	}
	o := parseUsage(b)
	if o == nil {
		v, err := jsonx.Decode(b)
		if _, isObj := v.(*jsonx.Object); err != nil || !isObj {
			return f.failed(cache, "", reasonUnreachable)
		}
		return f.failed(cache, "", "unexpected payload")
	}
	_ = os.MkdirAll(filepath.Dir(cache), 0o755)
	if fsio.WriteFileAtomic(cache, append(bytes.TrimSpace(b), '\n')) == nil {
		_ = os.Remove(cache + ".backoff")
		_ = os.Remove(cache + ".reason")
	}
	return o, ""
}

// reasonRateLimited and reasonUnreachable are stored reasons whose text is
// rebuilt on read: the first with the time left, the second as its message.
const (
	reasonRateLimited = "rate limited"
	reasonUnreachable = "usage endpoint unreachable"
)

// failed records a failed request the way the herdr mod does: a backoff file
// holding the Retry-After as given, else 300 seconds. The reason goes in a
// sidecar the mod ignores, so a held backoff can still say what went wrong.
// It returns the message for this call.
func (f *claudeFetch) failed(cache, retryAfter, reason string) (*jsonx.Object, string) {
	wait := backoffOf(retryAfter)
	_ = os.MkdirAll(filepath.Dir(cache), 0o755)
	_ = fsio.WriteFileAtomic(cache+".reason", []byte(reason+"\n"))
	_ = fsio.WriteFileAtomic(cache+".backoff", []byte(strconv.Itoa(int(wait/time.Second))+"\n"))
	return nil, heldMessage(reason, min(wait, maxBackoff))
}

func heldMessage(reason string, left time.Duration) string {
	if reason == reasonRateLimited {
		return fmt.Sprintf("rate limited (retry in %ds)", int((left+time.Second-1)/time.Second))
	}
	return reason
}

// backoffLeft is how much of the wait recorded beside cache remains (the
// backoff file holds the wait in seconds, capped at an hour, and its mtime is
// when the wait began) and why. The reason is the one rota recorded; a
// backoff rota did not write gets a neutral message.
func (f *claudeFetch) backoffLeft(cache string) (time.Duration, string) {
	b, mt, ok := f.openOwned(cache + ".backoff")
	if !ok {
		return 0, ""
	}
	left := min(backoffOf(string(b)), maxBackoff) - f.now().Sub(mt)
	if left <= 0 {
		return 0, ""
	}
	if rb, rmt, ok := f.openOwned(cache + ".reason"); ok {
		if d := rmt.Sub(mt); d > -reasonSkew && d < reasonSkew {
			if reason := strings.TrimSpace(string(rb)); reason != "" {
				return left, heldMessage(reason, left)
			}
		}
	}
	return left, fmt.Sprintf("backing off after a failed request (retry in %ds)", int((left+time.Second-1)/time.Second))
}

func parseUsage(b []byte) *jsonx.Object {
	v, err := jsonx.Decode(b)
	o, _ := v.(*jsonx.Object)
	if err != nil || o == nil {
		return nil
	}
	if _, has := o.Get("five_hour"); !has {
		return nil
	}
	return o
}

// token reads the access token of the account behind configDir (already
// expanded) without refreshing it.
func (f *claudeFetch) token(configDir string) (string, string) {
	oauth, tok, why := f.login.read(configDir, f.now())
	if why != "" {
		return "", why
	}
	if expired(oauth, f.now()) {
		return "", "token expired"
	}
	return tok, ""
}
