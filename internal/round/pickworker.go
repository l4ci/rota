package round

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/blockedby"
	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/limits"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/stamp"
	"github.com/l4ci/rota/internal/usagelimit"
	"github.com/l4ci/rota/internal/worker"
)

// Where a worker goes: pickWorker ranks every account of every harness the
// round may use by headroom, as `rota worker account pick` ranks Claude's.
// preflight asks it before anything is claimed, so a refusal moves nothing;
// the delivery's account step applies the answer.

// Placement is the account and harness a worker was put on.
type Placement struct {
	Kind, Account string
	// Headroom is the account's headroom now, nil when no meter reads it.
	Headroom *float64
	// Kept: the slot's own account stayed. Gap is how far the best usable
	// account was ahead of it, nil when either reading is unknown.
	Kept bool
	Gap  *float64
	// Fresh: the slot had no recorded harness, so this is its first placement.
	Fresh bool
	// Margin is round.accountRebalanceMargin in force.
	Margin int
}

// String says where the worker went and why, for the verb's output.
func (p Placement) String() string {
	who := p.Account
	if who == "" {
		who = "ambient login"
	}
	head := "headroom unknown"
	if p.Headroom != nil {
		head = fmt.Sprintf("headroom %g%%", *p.Headroom)
	}
	verdict := p.verdict()
	gap := ""
	if p.Gap != nil {
		gap = fmt.Sprintf(", best is %g points ahead (margin %d)", *p.Gap, p.Margin)
	}
	return fmt.Sprintf("%s (%s, %s) %s%s", who, p.Kind, head, verdict, gap)
}

// allowedKinds is the harnesses a round may use for workers, in tie-break order:
// round.harnesses (less a kind whose logins are all in review.accounts; none left is an empty list), else every kind with a round.accounts entry, else claude.
func allowedKinds(cfg any, set roundcfg.Settings) []string {
	var out []string
	if len(set.Harnesses) > 0 {
		for _, k := range set.Harnesses {
			// A harness whose only logins are review.accounts takes no worker.
			if !config.ReviewOnlyKind(cfg, k) {
				out = append(out, k)
			}
		}
		// Empty when every listed harness is review-only: the caller refuses
		// rather than quietly placing a worker on harness.Default.
		return out
	}
	for _, k := range harness.Kinds() {
		if len(config.AccountsOf(cfg, k)) > 0 {
			out = append(out, k)
		}
	}
	if len(out) == 0 {
		return []string{harness.Default}
	}
	return out
}

// poolConfigured says whether the project asks for balanced placement at all:
// round.harnesses or at least one round.accounts entry. Without either a
// worker runs on its slot's kind and the orchestrator's own login, as before.
func poolConfigured(cfg any, set roundcfg.Settings) bool {
	return len(set.Harnesses) > 0 || len(config.Accounts(cfg)) > 0
}

type workerPickIn struct {
	// Kinds are the harnesses the worker may go to, in tie-break order.
	Kinds []string
	Set   roundcfg.Settings
	Slot  string
	// SlotKind and SlotAccount are the account the slot ran under last: the
	// one a worker stays on unless a better one is far enough ahead.
	SlotKind, SlotAccount string
	// Margin is round.accountRebalanceMargin; Keep never moves a usable
	// current account (a transfer, a resume).
	Margin int
	Keep   bool
	// What the worker's model resolves from: a kind with no model where one is
	// needed cannot take it.
	Tier, FlagModel, LabelModel string
	// Why names the restriction in a refusal.
	Restriction string
}

// pickWorker picks the account and harness for a worker. No usable account is
// the quota (or no-login) refusal.
func (e Env) pickWorker(ctx context.Context, root string, in workerPickIn) (Placement, error) {
	cfg := e.Worker.Config(root)
	home, _ := e.Worker.Home()
	ms := e.readMeters(ctx, root, in.Kinds)
	load := e.accountLoad(root, in.Set, in.Slot)

	var entries []worker.PoolEntry
	for _, kind := range in.Kinds {
		hz := harness.ResolveAccounts(kind)
		kind = hz.Kind()
		list := harness.AccountsFor(cfg, kind, home).Entries
		entries = append(entries, e.poolEntries(ctx, ms, kind, list, in.Set, len(entries), load)...)
	}

	var cur *worker.PoolRef
	if in.SlotKind != "" {
		cur = &worker.PoolRef{Kind: harness.ResolveAccounts(in.SlotKind).Kind(), Name: in.SlotAccount}
	}
	margin := in.Margin
	if in.Keep {
		margin = 0
	}
	choice, ok := worker.ChoosePool(entries, cur, margin)
	if !ok {
		return Placement{}, noAccount(entries, in.Restriction, cfg)
	}
	p := Placement{Kind: choice.Entry.Kind, Account: choice.Entry.Name, Headroom: choice.Entry.Meter.Headroom, Kept: choice.Kept, Gap: choice.Gap, Fresh: cur == nil, Margin: in.Margin}
	return p, nil
}

// meterReadings is every reading a pool needs, taken once per placement.
type meterReadings struct {
	meters []usagelimit.Meter
	quotas map[worker.QuotaAccount]usagelimit.Meter
	log    []limits.Entry
	now    time.Time
}

// readMeters reads the scheduling meters of the given harness kinds: Claude's
// usage meters, the display-only kinds' quota snapshots, and the watcher's
// limit log.
func (e Env) readMeters(ctx context.Context, root string, kinds []string, extra ...config.Account) meterReadings {
	ms := meterReadings{now: e.now(), log: limits.Load(root)}
	if a := e.Worker.Accounts(); a != nil {
		ms.meters = a.MetersWith(ctx, root, extra)
	}
	if e.Quotas != nil {
		var metered []string
		for _, k := range kinds {
			if !harness.ResolveAccounts(k).UsageMeter() {
				metered = append(metered, k)
			}
		}
		ms.quotas = e.Quotas.MetersWith(ctx, root, metered, extra)
	}
	return ms
}

// poolEntries is one kind's accounts as pool entries with their meter and
// skip reason. An empty list is the orchestrator's own login (Name ""). order
// is the pool position of the first entry; load counts slots per account.
func (e Env) poolEntries(ctx context.Context, ms meterReadings, kind string, list []harness.AccountEntry, set roundcfg.Settings, order int, load map[worker.PoolRef]int) []worker.PoolEntry {
	hz := harness.ResolveAccounts(kind)
	if len(list) == 0 {
		list = []harness.AccountEntry{{}}
	}
	coolingByLog := map[string]time.Time{}
	if !hz.UsageMeter() {
		coolingByLog = limits.Cooling(ms.log, kind, ms.now)
	}
	rank := slices.Index(set.Harnesses, kind)
	if rank < 0 {
		rank = len(set.Harnesses)
	}
	var entries []worker.PoolEntry
	for _, acct := range list {
		pe := worker.PoolEntry{Kind: kind, Name: acct.Name, Rank: rank, Order: order + len(entries), Load: load[worker.PoolRef{Kind: kind, Name: acct.Name}],
			Meter: usagelimit.Meter{Name: acct.Name, ConfigDir: acct.Dir, Verdict: usagelimit.VerdictUnknown}}
		if hz.UsageMeter() {
			for _, m := range ms.meters {
				if acct.Name != "" && m.Name == acct.Name {
					pe.Meter = m
				}
			}
			if e.AccountPicking {
				dir := acct.Dir
				if acct.Name == "" { // the orchestrator's own login
					dir = hz.AmbientAccount(e.Worker.Getenv).ConfigDir
				}
				if why := e.Worker.Accounts().LoginProblem(dir); why != "" {
					pe.Skip = "no login (" + why + ")"
				}
			}
		} else {
			if m, ok := ms.quotas[worker.QuotaAccount{Kind: kind, Name: acct.Name}]; ok {
				pe.Meter = m
				pe.Meter.Name = acct.Name
			}
			if why := e.Worker.LoginProblem(ctx, kind, acct.Dir); why != "" {
				pe.Skip = "no login (" + why + ")"
			}
		}
		if at, ok := coolingByLog[acct.Name]; ok && acct.Name != "" && pe.Skip == "" {
			pe.Skip = "limited until " + stamp.Display(at)
		}
		entries = append(entries, pe)
	}
	return entries
}

// accountLoad counts the slots holding work on each account, so a batch of
// fresh slots spreads over accounts that are within the margin of the best.
func (e Env) accountLoad(root string, set roundcfg.Settings, except string) map[worker.PoolRef]int {
	reg := worker.LoadRegistryTolerant(root)
	out := map[worker.PoolRef]int{}
	for _, name := range set.Roster {
		s := reg.Slot(name)
		if s == nil || name == except || SlotIdle(s) {
			continue
		}
		out[worker.PoolRef{Kind: harness.ResolveAccounts(s.Kind()).Kind(), Name: s.AccountFor("")}]++
	}
	return out
}

// noAccount is the refusal when nothing is usable: every account cooling is
// the quota refusal, accounts with no login the no-login one.
func noAccount(entries []worker.PoolEntry, restriction string, cfg any) error {
	if len(entries) == 0 {
		return blocked(blockedby.Quota, "no account is configured for %s", restriction)
	}
	var resets []time.Time
	var cooling, other []string
	keys := map[string]bool{}
	for _, pe := range entries {
		switch {
		case pe.Meter.Verdict == usagelimit.VerdictCooling:
			cooling = append(cooling, accountLabel(pe.Name))
			if pe.Meter.ResetsAt != nil {
				resets = append(resets, *pe.Meter.ResetsAt)
			}
			keys[harness.AccountsFor(cfg, pe.Kind, "").Key] = true
		case pe.Skip != "":
			other = append(other, pe.Kind+" "+accountLabel(pe.Name)+": "+pe.Skip)
		}
	}
	by := blockedby.Quota
	if len(cooling) == 0 {
		by = blockedby.NoLogin
	}
	var why []string
	if len(cooling) > 0 {
		pool := make([]string, 0, len(keys))
		for k := range keys {
			pool = append(pool, k)
		}
		slices.Sort(pool)
		text := "every " + strings.Join(pool, " and ") + " account is cooling down"
		if len(resets) > 0 {
			text += "; slots fill again at " + stamp.Display(earliest(resets))
		}
		why = append(why, text+" ("+strings.Join(cooling, ", ")+")")
	}
	why = append(why, other...)
	return blocked(by, "no account can take %s: %s", restriction, strings.Join(why, "; "))
}

func accountLabel(name string) string {
	if name == "" {
		return "ambient login"
	}
	return name
}

// placesAccounts says whether assign puts a fresh slot on an account of its
// own choosing (and checks that account's login), so the login of the account
// the slot ran under last no longer decides whether it can take work.
func (e Env) placesAccounts(root string) bool {
	return root != "" && e.AccountPicking && !worker.IsSolo(root) && poolConfigured(e.Worker.Config(root), e.Round)
}
