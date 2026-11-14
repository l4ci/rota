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

// allowedKinds is the harnesses a round may use, in tie-break order:
// round.harnesses, else every kind with a round.accounts entry, else claude.
func allowedKinds(cfg any, set roundcfg.Settings) []string {
	if len(set.Harnesses) > 0 {
		return slices.Clone(set.Harnesses)
	}
	var out []string
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
	now := e.now()
	var meters []worker.Meter
	if a := e.Worker.Accounts(); a != nil {
		meters = a.Meters(ctx, root)
	}
	var quotas map[worker.QuotaAccount]worker.Meter
	if e.Quotas != nil {
		var metered []string
		for _, k := range in.Kinds {
			if !harness.ResolveAccounts(k).UsageMeter() {
				metered = append(metered, k)
			}
		}
		quotas = e.Quotas.Meters(ctx, root, metered)
	}
	load := e.accountLoad(root, in.Set, in.Slot)
	log := limits.Load(root)

	var entries []worker.PoolEntry
	for _, kind := range in.Kinds {
		hz := harness.ResolveAccounts(kind)
		kind = hz.Kind()
		list := hz.Accounts(harness.ConfigFor(cfg, kind)).In(home).Entries
		if len(list) == 0 {
			list = []harness.AccountEntry{{}}
		}
		coolingByLog := map[string]time.Time{}
		if !hz.UsageMeter() {
			coolingByLog = limits.Cooling(log, kind, now)
		}
		rank := slices.Index(in.Set.Harnesses, kind)
		if rank < 0 {
			rank = len(in.Set.Harnesses)
		}
		for _, acct := range list {
			pe := worker.PoolEntry{Kind: kind, Name: acct.Name, Rank: rank, Order: len(entries), Load: load[worker.PoolRef{Kind: kind, Name: acct.Name}],
				Meter: worker.Meter{Name: acct.Name, ConfigDir: acct.Dir, Verdict: worker.VerdictUnknown}}
			if hz.UsageMeter() {
				for _, m := range meters {
					if acct.Name != "" && m.Name == acct.Name {
						pe.Meter = m
					}
				}
				if acct.Name != "" && e.AccountPicking {
					if why := e.Worker.Accounts().LoginProblem(acct.Dir); why != "" {
						pe.Skip = "no login (" + why + ")"
					}
				}
			} else {
				if m, ok := quotas[worker.QuotaAccount{Kind: kind, Name: acct.Name}]; ok {
					pe.Meter = m
					pe.Meter.Name = acct.Name
				}
				if why := e.Worker.LoginProblem(ctx, kind, acct.Dir); why != "" {
					pe.Skip = "no login (" + why + ")"
				}
			}
			if at, ok := coolingByLog[acct.Name]; ok && acct.Name != "" && pe.Skip == "" {
				pe.Skip = "limited until " + at.UTC().Format("2006-01-02 15:04 MST")
			}
			entries = append(entries, pe)
		}
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
		case pe.Meter.Verdict == worker.VerdictCooling:
			cooling = append(cooling, accountLabel(pe.Name))
			if pe.Meter.ResetsAt != nil {
				resets = append(resets, *pe.Meter.ResetsAt)
			}
			keys[harness.ResolveAccounts(pe.Kind).Accounts(harness.ConfigFor(cfg, pe.Kind)).Key] = true
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
			text += "; slots fill again at " + earliest(resets).UTC().Format("2006-01-02 15:04 MST")
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
