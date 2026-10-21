package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/l4ci/rota/internal/exitcode"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/connections"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/proc"
)

// Account usage headroom and slot assignment: the port of
// bin/hv-worker-account.
//
// Each slot can run under its own CLAUDE_CONFIG_DIR, which carries its own
// OAuth credentials, so "how much headroom does this account have" is a
// per-config-dir question. Three sources, tried in order, each degrading
// honestly rather than guessing:
//  1. The OAuth usage endpoint, authenticated with the account's own
//     credentials. Works with NO session running, which is what makes
//     pre-dispatch balancing possible.
//  2. A fixture payload (ROTA_ACCOUNT_USAGE_DIR) for offline tests.
//  3. `unknown`, which callers treat as "rotate", never as "free" and never
//     as "exhausted".
//
// Three distinctions the caller must not collapse: five_hour and seven_day are
// SEPARATE windows with staggered resets; `utilization: 0` with `resets_at:
// null` means nothing spent, which is not the same as no data; and seven_day
// at 100% does NOT make an account unusable when extra_usage is enabled and
// spend_limit_reached is false.
//
// This code NEVER refreshes or writes credentials: an expired token reports
// `unknown`. Refreshing would mutate state a live Claude Code session owns.

const usageURL = "https://api.anthropic.com/api/oauth/usage"

// Account verdicts.
const (
	VerdictFree    = "free"
	VerdictCooling = "cooling"
	VerdictUnknown = "unknown"
)

// Meter is one configured account with its reading.
type Meter struct {
	Name, ConfigDir, Verdict, Reason string
	FiveHour, SevenDay, Headroom     *float64
	ResetsAt                         *time.Time
}

// Fetcher returns the usage payload for an account, or a reason it has none.
type Fetcher func(ctx context.Context, name, configDir string) (payload *jsonx.Object, reason string)

// Accounts reads and assigns accounts. Its clock and environment come from Env
// (Env.WithDefaults), the same source as the worker env that holds it.
type Accounts struct {
	Env Env
	// Fetch defaults to the fixture dir or the OAuth endpoint.
	Fetch Fetcher
	// HTTP is the client for the usage endpoint; nil means a 5s-timeout client.
	HTTP *http.Client
	// Login reads each account's credentials; the zero value reads files only.
	Login Credentials
}

func (a *Accounts) now() time.Time { return a.Env.WithDefaults().Now().UTC() }

func (a *Accounts) getenv(k string) string { return a.Env.WithDefaults().Getenv(k) }

// home is the home dir Env reports; "" when it has none, which leaves a
// leading ~/ unexpanded.
func (a *Accounts) home() string {
	h, _ := a.Env.WithDefaults().Home()
	return h
}

// expand is ExpandConfigDir against Env's home.
func (a *Accounts) expand(d string) string { return ExpandConfigDirIn(d, a.home()) }

// same is SameConfigDir against Env's home.
func (a *Accounts) same(x, y string) bool { return a.expand(x) == a.expand(y) }

// login is Login with Env's home unless Login names its own.
func (a *Accounts) login() Credentials {
	c := a.Login
	if c.Home == nil {
		c.Home = a.Env.WithDefaults().Home
	}
	return c
}

type acct struct{ name, configDir string }

// Configured returns round.accounts of the project config.
func (a *Accounts) Configured(root string) []acct {
	cfg := a.Env.WithDefaults().Config(root)
	var out []acct
	for _, a := range config.Accounts(cfg) {
		out = append(out, acct{a.Name, a.ConfigDir})
	}
	return out
}

// Meters reads every configured account.
func (a *Accounts) Meters(ctx context.Context, root string) []Meter {
	fetch := a.Fetch
	if fetch == nil {
		fetch = a.defaultFetch
	}
	var rows []Meter
	for _, ac := range a.Configured(root) {
		if ac.name == "" {
			continue
		}
		payload, why := fetch(ctx, ac.name, ac.configDir)
		if payload == nil {
			rows = append(rows, Meter{Name: ac.name, ConfigDir: ac.configDir, Verdict: VerdictUnknown, Reason: why})
			continue
		}
		rows = append(rows, a.classify(ac, payload))
	}
	return rows
}

func (a *Accounts) classify(ac acct, payload *jsonx.Object) Meter {
	five, fiveReset := window(payload, "five_hour")
	seven, sevenReset := window(payload, "seven_day")
	extra, _ := payload.Get("extra_usage")
	eo, _ := extra.(*jsonx.Object)
	extraLive := false
	if eo != nil {
		en, _ := eo.Get("is_enabled")
		lim, _ := eo.Get("spend_limit_reached")
		extraLive = jsonx.Truthy(en) && !jsonx.Truthy(lim)
	}
	m := Meter{Name: ac.name, ConfigDir: ac.configDir, Verdict: VerdictFree, FiveHour: five, SevenDay: seven}
	var binding []float64
	for _, w := range []struct {
		label string
		util  *float64
		reset *time.Time
	}{{"five_hour", five, fiveReset}, {"seven_day", seven, sevenReset}} {
		if w.util == nil {
			continue
		}
		// Weekly exhaustion with extra usage still live neither parks the
		// account nor constrains it, so the window is discounted from
		// headroom too. The 5-hour window has no such escape hatch.
		if w.label == "seven_day" && *w.util >= 100 && extraLive {
			continue
		}
		binding = append(binding, *w.util)
		if *w.util < 100 {
			continue
		}
		// A window is only cooling if it is spent AND names a FUTURE reset: a
		// spent window with no resets_at is not actionable, and treating it as
		// cooling would park the slot forever.
		if w.reset == nil || !w.reset.After(a.now()) {
			continue
		}
		m.Verdict = VerdictCooling
		if m.ResetsAt == nil || w.reset.After(*m.ResetsAt) {
			r := *w.reset
			m.ResetsAt = &r
		}
	}
	if len(binding) > 0 {
		mx := binding[0]
		for _, b := range binding {
			mx = math.Max(mx, b)
		}
		h := roundTenth(100 - mx)
		m.Headroom = &h
	}
	return m
}

// roundTenth is Python's round(x, 1): correct rounding of the exact binary value.
func roundTenth(x float64) float64 {
	r, _ := strconv.ParseFloat(strconv.FormatFloat(x, 'f', 1, 64), 64)
	return r
}

func window(p *jsonx.Object, key string) (*float64, *time.Time) {
	v, _ := p.Get(key)
	w, _ := v.(*jsonx.Object)
	if w == nil {
		return nil, nil
	}
	var util *float64
	if u, ok := w.Get("utilization"); ok {
		if n, ok := u.(json.Number); ok {
			if f, err := n.Float64(); err == nil {
				util = &f
			}
		}
	}
	r, _ := w.Get("resets_at")
	return util, parseReset(r)
}

func parseReset(v any) *time.Time {
	s, ok := v.(string)
	if !ok || s == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999", "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return &t
		}
	}
	return nil
}

// defaultFetch reads ROTA_ACCOUNT_USAGE_DIR/<name>.json when set, else the OAuth
// usage endpoint with the account's own access token.
func (a *Accounts) defaultFetch(ctx context.Context, name, configDir string) (*jsonx.Object, string) {
	if dir := a.getenv("ROTA_ACCOUNT_USAGE_DIR"); dir != "" {
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
	token, why := a.token(configDir)
	if token == "" {
		return nil, why
	}
	client := a.HTTP
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, usageURL, nil)
	if err != nil {
		return nil, "usage endpoint unreachable"
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	resp, err := client.Do(req)
	if err != nil {
		return nil, "usage endpoint unreachable"
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	v, err := jsonx.Decode(b)
	if err != nil {
		return nil, "usage endpoint unreachable"
	}
	o, ok := v.(*jsonx.Object)
	if _, has := func() (any, bool) {
		if o == nil {
			return nil, false
		}
		return o.Get("five_hour")
	}(); !ok || !has {
		return nil, "unexpected payload"
	}
	return o, ""
}

// Credentials reads an account's login. GOOS and Run are the platform seams:
// on darwin the login Keychain is consulted through Run (the one `security`
// read, connections.SecurityRead), elsewhere never. The zero value reads files
// only. Now defaults to time.Now.
type Credentials struct {
	GOOS string
	Run  proc.Runner
	Now  func() time.Time
	// Home is whose ~/.claude is the unsuffixed Keychain service; nil means
	// none is, and SystemCredentials sets os.UserHomeDir.
	Home func() (string, error)
}

// SystemCredentials is Credentials on this machine, running tools through run.
func SystemCredentials(run proc.Runner) Credentials {
	return Credentials{GOOS: runtime.GOOS, Run: run, Now: time.Now, Home: os.UserHomeDir}
}

// Problem reports why the account whose config dir is dir (already expanded)
// has no usable credentials: no file (or, on macOS, Keychain entry),
// unreadable, or no access token. "" means usable. Expiry is not checked:
// Claude Code refreshes a stale token itself, and this code never does.
func (c Credentials) Problem(dir string) string {
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	_, _, why := c.read(dir, now())
	return why
}

const keychainBase = "Claude Code-credentials"

// keychainService is the generic-password service name Claude Code files the
// OAuth credentials under for config dir dir (already expanded): the default
// ~/.claude has no suffix, any other dir carries the first 8 hex chars of
// sha256(dir).
func keychainService(dir, home string) string {
	if home != "" && dir == filepath.Join(home, ".claude") {
		return keychainBase
	}
	sum := sha256.Sum256([]byte(dir))
	return keychainBase + "-" + hex.EncodeToString(sum[:])[:8]
}

// readCredentials returns the OAuth blob, access token and a reason when there
// is none. The file comes first. On darwin the login Keychain is consulted only
// when the file is missing, unusable or expired: Claude Code moved its
// credentials there, so a leftover file can be stale while the Keychain entry
// is current. Without a usable Keychain entry the file's own result stands.
func (c Credentials) read(dir string, now time.Time) (*jsonx.Object, string, string) {
	oauth, tok, why := readCredentialsFile(dir)
	if why == "" && !expired(oauth, now) {
		return oauth, tok, ""
	}
	if c.GOOS == "darwin" && c.Run != nil {
		home := ""
		if c.Home != nil {
			home, _ = c.Home()
		}
		b, ok, err := connections.SecurityRead(context.Background(), c.Run, keychainService(dir, home), "")
		if err == nil && ok {
			if o, t, w := parseCredentials([]byte(b)); w == "" {
				return o, t, ""
			}
		}
		if why == "no credentials file" {
			why = "no credentials file or Keychain entry"
			if err != nil {
				why = "no credentials file, Keychain unavailable: " + err.Error()
			}
		}
	}
	return oauth, tok, why
}

func readCredentialsFile(dir string) (*jsonx.Object, string, string) {
	b, err := os.ReadFile(filepath.Join(dir, ".credentials.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", "no credentials file"
		}
		return nil, "", "unreadable credentials"
	}
	return parseCredentials(b)
}

// expired reports whether the blob's expiresAt is at or before now.
func expired(oauth *jsonx.Object, now time.Time) bool {
	exp, ok := oauth.Get("expiresAt")
	if !ok || !jsonx.Truthy(exp) {
		return false
	}
	n, ok := exp.(json.Number)
	if !ok {
		return false
	}
	ms, err := n.Int64()
	return err == nil && !time.UnixMilli(ms).After(now)
}

func parseCredentials(b []byte) (*jsonx.Object, string, string) {
	v, err := jsonx.Decode(b)
	blob, ok := v.(*jsonx.Object)
	if err != nil || !ok {
		return nil, "", "unreadable credentials"
	}
	ov, _ := blob.Get("claudeAiOauth")
	oauth, _ := ov.(*jsonx.Object)
	if oauth == nil {
		return nil, "", "no access token"
	}
	tok := jsonx.Str(oauth, "accessToken")
	if tok == "" {
		return nil, "", "no access token"
	}
	return oauth, tok, ""
}

// token reads the access token without refreshing it.
func (a *Accounts) token(configDir string) (string, string) {
	oauth, tok, why := a.login().read(a.expand(configDir), a.now())
	if why != "" {
		return "", why
	}
	if expired(oauth, a.now()) {
		return "", "token expired"
	}
	return tok, ""
}

// Pick returns the account with the most headroom, skipping cooling and
// excluded accounts. Unknown meters sort last but stay eligible, so a fleet
// with no readable meters still rotates instead of stalling. An account whose
// config dir has no usable login (Login.Problem) is never picked: it would
// dispatch into a login prompt. ok is false when no account is usable.
func (a *Accounts) Pick(ctx context.Context, root string, exclude []string) (name string, ok bool) {
	skip := map[string]bool{}
	for _, n := range exclude {
		skip[strings.TrimSpace(n)] = true
	}
	var usable []Meter
	for _, m := range a.Meters(ctx, root) {
		if !skip[m.Name] && m.Verdict != VerdictCooling && a.login().Problem(a.expand(m.ConfigDir)) == "" {
			usable = append(usable, m)
		}
	}
	if len(usable) == 0 {
		return "", false
	}
	sort.SliceStable(usable, func(i, j int) bool {
		hi, hj := usable[i].Headroom, usable[j].Headroom
		if (hi == nil) != (hj == nil) {
			return hi != nil
		}
		if hi == nil {
			return false
		}
		return *hi > *hj
	})
	return usable[0].Name, true
}

// NoPickReason explains a failed Pick when a missing login is involved: it
// names each non-excluded, non-cooling account without a usable login and why.
// "" when no account is blocked by its login, so the caller's own reason (all
// cooling, nothing configured) stands.
func (a *Accounts) NoPickReason(ctx context.Context, root string, exclude []string) string {
	skip := map[string]bool{}
	for _, n := range exclude {
		skip[strings.TrimSpace(n)] = true
	}
	var loginless []string
	cooling := false
	for _, m := range a.Meters(ctx, root) {
		if skip[m.Name] {
			continue
		}
		if m.Verdict == VerdictCooling {
			cooling = true
			continue
		}
		if why := a.login().Problem(a.expand(m.ConfigDir)); why != "" {
			loginless = append(loginless, m.Name+" ("+why+")")
		}
	}
	if len(loginless) == 0 {
		return ""
	}
	lead := "every configured account has no login: "
	if cooling {
		lead = "every configured account is cooling down or has no login: "
	}
	return lead + strings.Join(loginless, ", ")
}

// SameConfigDirIn reports whether two CLAUDE_CONFIG_DIR values name one
// directory under home: compared after Clean, with a leading ~/ expanded and
// empty meaning ~/.claude (what Claude Code uses when the variable is unset).
func SameConfigDirIn(a, b, home string) bool {
	return ExpandConfigDirIn(a, home) == ExpandConfigDirIn(b, home)
}

// ExpandConfigDirIn is the cleaned absolute form of a configDir against home.
// An empty home leaves a leading ~/ unexpanded.
func ExpandConfigDirIn(d, home string) string {
	if d == "" {
		d = "~/.claude"
	}
	if strings.HasPrefix(d, "~/") && home != "" {
		d = filepath.Join(home, d[2:])
	}
	return filepath.Clean(d)
}

// ConfigDirMismatch is the warning for an orchestrator running under
// orchDir (its CLAUDE_CONFIG_DIR) when round.accounts is set and names no such
// dir: slots take the configured accounts, so they would not run under the
// orchestrator's login. "" when orchDir is unset or one of the accounts. An
// empty account list is no mismatch: slots inherit orchDir.
func ConfigDirMismatch(orchDir string, accts []config.Account, home string) string {
	if orchDir == "" || len(accts) == 0 {
		return ""
	}
	for _, a := range accts {
		if a.ConfigDir != "" && SameConfigDirIn(a.ConfigDir, orchDir, home) {
			return ""
		}
	}
	return "the orchestrator runs under CLAUDE_CONFIG_DIR=" + orchDir + ", which is not in round.accounts; slots will use the configured accounts, not that login"
}

// AccountOf names the round.accounts entry whose configDir is dir, "" when
// none is.
func (a *Accounts) AccountOf(root, dir string) string {
	for _, ac := range a.Configured(root) {
		if ac.configDir != "" && a.same(ac.configDir, dir) {
			return ac.name
		}
	}
	return ""
}

// OrchestratorTarget is the account an orchestrator can move to when its own
// has used `threshold` percent of a window (D4). Unlike Pick it never keeps
// the current account, and it takes only an account that has a configDir, a
// `free` meter and known headroom above 100-threshold: an `unknown` meter is
// not a candidate, since a wrong guess costs a whole handoff cycle. The most
// headroom wins, ties by config order. others describes every account that
// was not taken, for the log.
func (a *Accounts) OrchestratorTarget(ctx context.Context, root, currentDir string, threshold int) (m Meter, ok bool, others []string) {
	for _, c := range a.Meters(ctx, root) {
		why := ""
		switch {
		case c.ConfigDir == "":
			why = "no configDir"
		case a.same(c.ConfigDir, currentDir):
			why = "current account"
		case c.Verdict != VerdictFree:
			why = c.Verdict
		case c.Headroom == nil:
			why = "no reading"
		case *c.Headroom <= float64(100-threshold):
			why = fmt.Sprintf("%g%% headroom", *c.Headroom)
		}
		if why != "" {
			others = append(others, c.Name+": "+why)
			continue
		}
		if !ok || *c.Headroom > *m.Headroom {
			if ok {
				others = append(others, m.Name+": less headroom")
			}
			m, ok = c, true
		} else {
			others = append(others, c.Name+": less headroom")
		}
	}
	return m, ok, others
}

// Assign writes the account's configDir onto the slot so dispatch launches
// that slot under it. An empty account means "pick one". ok is false when no
// account was usable (exit 4 for the verb).
func (a *Accounts) Assign(ctx context.Context, root, slot, account string) (name string, changed bool, err error) {
	reg, err := LoadRegistry(root)
	if err != nil {
		return "", false, err
	}
	if !reg.Exists {
		return "", false, fail(exitcode.ExitResolution, "no worker pool — run rota worker pool init first")
	}
	if account == "" {
		picked, ok := a.Pick(ctx, root, nil)
		if !ok {
			if why := a.NoPickReason(ctx, root, nil); why != "" {
				return "", false, fail(exitcode.ExitRefused, why)
			}
			return "", false, fail(exitcode.ExitRefused, "every configured account is cooling down")
		}
		account = picked
	}
	var match *acct
	for _, ac := range a.Configured(root) {
		if ac.name == account {
			c := ac
			match = &c
			break
		}
	}
	if match == nil {
		return "", false, fail(exitcode.ExitResolution, fmt.Sprintf("account '%s' is not in round.accounts", account))
	}
	found, err := UpdateSlot(root, slot, func(s *Slot) {
		if s.Account() != account || s.ConfigDir() != match.configDir {
			changed = true
		}
		s.SetAccount(account, match.configDir)
	})
	if err != nil {
		return "", false, err
	}
	if !found {
		return "", false, fail(exitcode.ExitResolution, fmt.Sprintf("slot '%s' is not in the pool", slot))
	}
	return account, changed, nil
}

// ISOFormat renders a time like Python's datetime.isoformat().
func ISOFormat(t time.Time) string {
	s := t.Format("2006-01-02T15:04:05")
	if us := t.Nanosecond() / 1000; us != 0 {
		s += fmt.Sprintf(".%06d", us)
	}
	return s + t.Format("-07:00")
}
