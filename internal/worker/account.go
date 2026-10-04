package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/jsonx"
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

// Accounts reads and assigns accounts.
type Accounts struct {
	Env Env
	// Fetch defaults to the fixture dir or the OAuth endpoint.
	Fetch Fetcher
	// Now defaults to time.Now.
	Now func() time.Time
	// Getenv defaults to os.Getenv.
	Getenv func(string) string
	// HTTP is the client for the usage endpoint; nil means a 5s-timeout client.
	HTTP *http.Client
}

func (a *Accounts) now() time.Time {
	if a.Now != nil {
		return a.Now().UTC()
	}
	return time.Now().UTC()
}

func (a *Accounts) getenv(k string) string {
	if a.Getenv != nil {
		return a.Getenv(k)
	}
	return os.Getenv(k)
}

type acct struct{ name, configDir string }

// Configured returns work.accounts of the project config.
func Configured(root string) []acct {
	cfg := config.Load(filepath.Join(root, ".rota", "config.json"))
	raw, _ := config.Lookup(cfg, "work.accounts")
	list, _ := raw.([]any)
	var out []acct
	for _, e := range list {
		if o, ok := e.(*jsonx.Object); ok {
			out = append(out, acct{Str(o, "name"), Str(o, "configDir")})
		}
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
	for _, ac := range Configured(root) {
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
		extraLive = truthy(en) && !truthy(lim)
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

func truthy(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case nil:
		return false
	case string:
		return x != ""
	case json.Number:
		f, _ := x.Float64()
		return f != 0
	}
	return true
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

// token reads the access token without refreshing it.
func (a *Accounts) token(configDir string) (string, string) {
	dir := configDir
	if strings.HasPrefix(dir, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			dir = filepath.Join(home, dir[2:])
		}
	}
	b, err := os.ReadFile(filepath.Join(dir, ".credentials.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return "", "no credentials file"
		}
		return "", "unreadable credentials"
	}
	v, err := jsonx.Decode(b)
	blob, ok := v.(*jsonx.Object)
	if err != nil || !ok {
		return "", "unreadable credentials"
	}
	ov, _ := blob.Get("claudeAiOauth")
	oauth, _ := ov.(*jsonx.Object)
	if oauth == nil {
		return "", "no access token"
	}
	tok := Str(oauth, "accessToken")
	if tok == "" {
		return "", "no access token"
	}
	if exp, ok := oauth.Get("expiresAt"); ok && truthy(exp) {
		if n, ok := exp.(json.Number); ok {
			if ms, err := n.Int64(); err == nil && !time.UnixMilli(ms).After(a.now()) {
				return "", "token expired"
			}
		}
	}
	return tok, ""
}

// Pick returns the account with the most headroom, skipping cooling and
// excluded accounts. Unknown meters sort last but stay eligible, so a fleet
// with no readable meters still rotates instead of stalling. ok is false when
// no account is usable.
func (a *Accounts) Pick(ctx context.Context, root string, exclude []string) (name string, ok bool) {
	skip := map[string]bool{}
	for _, n := range exclude {
		skip[strings.TrimSpace(n)] = true
	}
	var usable []Meter
	for _, m := range a.Meters(ctx, root) {
		if !skip[m.Name] && m.Verdict != VerdictCooling {
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

// SameConfigDir reports whether two CLAUDE_CONFIG_DIR values name one
// directory: compared after Clean, with a leading ~/ expanded and empty
// meaning ~/.claude (what Claude Code uses when the variable is unset).
func SameConfigDir(a, b string) bool { return ExpandConfigDir(a) == ExpandConfigDir(b) }

// ExpandConfigDir is the cleaned absolute form of a configDir.
func ExpandConfigDir(d string) string {
	home, _ := os.UserHomeDir()
	if d == "" {
		d = "~/.claude"
	}
	if strings.HasPrefix(d, "~/") && home != "" {
		d = filepath.Join(home, d[2:])
	}
	return filepath.Clean(d)
}

// AccountOf names the work.accounts entry whose configDir is dir, "" when
// none is.
func AccountOf(root, dir string) string {
	for _, ac := range Configured(root) {
		if ac.configDir != "" && SameConfigDir(ac.configDir, dir) {
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
		case SameConfigDir(c.ConfigDir, currentDir):
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
	reg := LoadRegistry(root)
	if !reg.Exists {
		return "", false, fail(ExitResolution, "no worker pool — run rota worker pool init first")
	}
	if account == "" {
		picked, ok := a.Pick(ctx, root, nil)
		if !ok {
			return "", false, fail(ExitRefused, "every configured account is cooling down")
		}
		account = picked
	}
	var match *acct
	for _, ac := range Configured(root) {
		if ac.name == account {
			c := ac
			match = &c
			break
		}
	}
	if match == nil {
		return "", false, fail(ExitResolution, fmt.Sprintf("account '%s' is not in work.accounts", account))
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
		return "", false, fail(ExitResolution, fmt.Sprintf("slot '%s' is not in the pool", slot))
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
