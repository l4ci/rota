package worker

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/stamp"
	"github.com/l4ci/rota/internal/usagelimit"
)

// Account choice is policy over per-account readings. Each slot can run under
// its own CLAUDE_CONFIG_DIR, so "how much headroom does this account have" is
// a per-config-dir question; the reading itself (the OAuth usage endpoint, its
// cache and backoff, the credentials, the offline fixture) lives behind the
// harness.QuotaReporter seam, read through Quotas. An unreadable account is
// `unknown`, which callers treat as "rotate", never as "free" and never as
// "exhausted".

// Accounts reads and assigns accounts. Its clock and environment come from Env
// (Env.WithDefaults), the same source as the worker env that holds it.
type Accounts struct {
	Env Env
	// Login reads each account's credentials; the zero value reads files only.
	Login harness.Credentials
	// Options carries the reader's remaining seams (HTTP client, cache dir,
	// file-owner check); Env and Login fill the rest.
	Options harness.QuotaOptions
	// Read replaces the quota reporter call (tests); nil reads for real.
	Read func(context.Context, harness.QuotaReporter, harness.QuotaRequest, harness.QuotaOptions) harness.QuotaSnapshot

	mu sync.Mutex
	q  *Quotas
}

// quotas is the reader behind Meters, built on first use so its short-lived
// readings are shared by every call of this Accounts.
func (a *Accounts) quotas() *Quotas {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.q == nil {
		opts := a.Options
		opts.Login = a.Login
		a.q = &Quotas{Env: a.Env, Options: opts, Read: a.Read}
	}
	return a.q
}

// home is the home dir Env reports; "" when it has none, which leaves a
// leading ~/ unexpanded.
func (a *Accounts) home() string {
	h, _ := a.Env.WithDefaults().Home()
	return h
}

// expand is ExpandConfigDir against Env's home.
func (a *Accounts) expand(d string) string { return usagelimit.ExpandConfigDirIn(d, a.home()) }

// same is SameConfigDir against Env's home.
func (a *Accounts) same(x, y string) bool { return a.expand(x) == a.expand(y) }

// login is Login with Env's home and clock unless Login names its own.
func (a *Accounts) login() harness.Credentials {
	c := a.Login
	e := a.Env.WithDefaults()
	if c.Home == nil {
		c.Home = e.Home
	}
	if c.Now == nil {
		c.Now = func() time.Time { return e.Now().UTC() }
	}
	return c
}

// LoginProblem says why the config dir has no usable login ("" when it has
// one), reading credentials as the account picker does.
func (a *Accounts) LoginProblem(configDir string) string {
	return a.login().Problem(a.expand(configDir))
}

type acct struct{ name, configDir string }

// Configured returns round.accounts of the project config, each configDir
// expanded: the path a slot caches and CLAUDE_CONFIG_DIR carries is absolute.
// An unset configDir stays "" (the entry has none).
func (a *Accounts) Configured(root string) []acct {
	cfg := a.Env.WithDefaults().Config(root)
	var out []acct
	for _, ac := range config.AccountsOf(cfg, config.KindClaude) {
		dir := ac.Dir
		if dir != "" {
			dir = a.expand(dir)
		}
		out = append(out, acct{ac.Name, dir})
	}
	return out
}

// Meters reads every configured account.
func (a *Accounts) Meters(ctx context.Context, root string) []usagelimit.Meter {
	return a.MetersWith(ctx, root, nil)
}

// MetersWith is Meters that also reads the Claude entries of extra (such as
// review.accounts, #3315) that round.accounts does not already list by name.
// Rows come in round.accounts order, then the extras. Each is read through the
// Claude quota reporter (Quotas), so a failed read is an unknown meter.
func (a *Accounts) MetersWith(ctx context.Context, root string, extra []config.Account) []usagelimit.Meter {
	kinds := meterKinds()
	e := a.Env.WithDefaults()
	cfg := e.Config(root)
	home, _ := e.Home()
	var list []acct
	for _, k := range kinds {
		_, ok := harness.LookupAccounts(k)
		if !ok {
			continue
		}
		for _, en := range harness.AccountsFor(cfg, k, home).Entries {
			list = append(list, acct{en.Name, en.Dir})
		}
	}
	configured := len(list)
	var extras []config.Account
	for _, x := range extra {
		if !slices.Contains(kinds, x.Kind) || x.Name == "" || slices.ContainsFunc(list, func(c acct) bool { return c.name == x.Name }) {
			continue
		}
		extras = append(extras, x)
		list = append(list, acct{x.Name, ""})
	}
	// Without configured accounts the reader would add the ambient login; this
	// meters named accounts only.
	if configured == 0 {
		kinds = nil
	}
	read := a.quotas().MetersWith(ctx, root, kinds, extras)
	var rows []usagelimit.Meter
	for _, ac := range list {
		if ac.name == "" {
			continue
		}
		m, ok := meterOf(read, ac.name)
		if !ok {
			m = usagelimit.Meter{Name: ac.name, Verdict: usagelimit.VerdictUnknown, Reason: "account not read"}
		}
		rows = append(rows, m)
	}
	return rows
}

// meterKinds are the harness kinds whose usage is a built-in scheduling meter
// (Claude's): the accounts Meters reads.
func meterKinds() []string {
	var out []string
	for _, k := range harness.Kinds() {
		if hz, ok := harness.LookupAccounts(k); ok && hz.UsageMeter() {
			out = append(out, k)
		}
	}
	return out
}

func meterOf(read map[QuotaAccount]usagelimit.Meter, name string) (usagelimit.Meter, bool) {
	for _, k := range meterKinds() {
		if m, ok := read[QuotaAccount{k, name}]; ok {
			return m, true
		}
	}
	return usagelimit.Meter{}, false
}

// Pick returns the account with the most headroom, skipping cooling and
// excluded accounts. Unknown meters sort last but stay eligible, so a fleet
// with no readable meters still rotates instead of stalling. An account whose
// config dir has no usable login (Login.Problem) is never picked: it would
// dispatch into a login prompt. ok is false when no account is usable.
func (a *Accounts) Pick(ctx context.Context, root string, exclude []string) (name string, ok bool) {
	names := make([]string, len(exclude))
	for i, n := range exclude {
		names[i] = strings.TrimSpace(n)
	}
	return a.PickFrom(a.Meters(ctx, root), names)
}

// PickFrom is Pick over one Meters reading, so a caller that already holds the
// meters (account list) names the account assign would take without reading
// them again.
func (a *Accounts) PickFrom(meters []usagelimit.Meter, exclude []string) (name string, ok bool) {
	m, ok, _ := usagelimit.SelectAccount(meters, usagelimit.AccountSelection{
		Exclude: exclude,
		LoginProblem: func(m usagelimit.Meter) string {
			return a.login().Problem(a.expand(m.ConfigDir))
		},
	})
	return m.Name, ok
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
		if m.Verdict == usagelimit.VerdictCooling {
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
		if a.Dir != "" && usagelimit.SameConfigDirIn(a.Dir, orchDir, home) {
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
func (a *Accounts) OrchestratorTarget(ctx context.Context, root, currentDir string, threshold int) (m usagelimit.Meter, ok bool, others []string) {
	return usagelimit.SelectAccount(a.Meters(ctx, root), usagelimit.AccountSelection{
		Handoff: &usagelimit.AccountHandoff{CurrentDir: currentDir, Home: a.home(), Threshold: threshold},
	})
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
		if s.MeteredAccount() != account || s.ConfigDir() != match.configDir {
			changed = true
		}
		s.SetMeteredAccount(account, match.configDir)
	})
	if err != nil {
		return "", false, err
	}
	if !found {
		return "", false, fail(exitcode.ExitResolution, fmt.Sprintf("slot '%s' is not in the pool", slot))
	}
	return account, changed, nil
}

// accountLimited says why the round.accounts account behind configDir cannot
// take a new session: its meter is cooling, the verdict placement
// (round assign) uses. "" when it can, when the harness has no config-dir accounts, when
// configDir is no configured account or when no meter reader is set.
func (e Env) accountLimited(ctx context.Context, root string, hz harness.Accounts, configDir string) string {
	if e.Accounts == nil || !hz.WorkAccounts() || configDir == "" {
		return ""
	}
	name := e.Accounts.AccountOf(root, configDir)
	if name == "" {
		return ""
	}
	for _, m := range e.Accounts.Meters(ctx, root) {
		if m.Name == name && m.Verdict == usagelimit.VerdictCooling {
			why := "account " + name + " is at its usage limit"
			if m.ResetsAt != nil {
				why += "; it resets at " + stamp.ISO(*m.ResetsAt)
			}
			return why
		}
	}
	return ""
}

// Headroom is the account's headroom percentage now, nil when no meter reads
// it (an unconfigured account, an unknown verdict): unknown is never 0.
func (a *Accounts) Headroom(ctx context.Context, root, account string) *float64 {
	if account == "" {
		return nil
	}
	return HeadroomOf(a.Meters(ctx, root), account)
}

// HeadroomOf picks the account's headroom out of one Meters reading, so a
// caller with several slots fetches once.
func HeadroomOf(ms []usagelimit.Meter, account string) *float64 {
	for _, m := range ms {
		if m.Name == account {
			return m.Headroom
		}
	}
	return nil
}

// LedgerHeadroom is the headroom of an assign or done entry:
// set only for a claude slot whose meter reads it. Codex has no meter.
func LedgerHeadroom(ctx context.Context, a *Accounts, root, kind, account string) *float64 {
	if a == nil || account == "" {
		return nil
	}
	if !harness.ResolveAccounts(kind).UsageMeter() {
		return nil
	}
	return a.Headroom(ctx, root, account)
}
