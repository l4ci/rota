package round

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/limits"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/worker"
)

// Cap is how many roster slots the round may fill right now. It is the roster
// size until every account a worker could run under is cooling down; then it is
// the slots already holding an issue, so nothing new starts until a reset.
type Cap struct {
	Roster, Effective int
	// Reason says why Effective is below Roster; empty otherwise.
	Reason string
	// ResumesAt is the earliest reset among the cooling accounts, zero when
	// the cap is not reduced.
	ResumesAt time.Time
	// why says per harness kind that every configured account of its pool is
	// cooling down. An unconfigured pool is never closed.
	why map[string]string
	// resets is the earliest reset per kind whose pool is closed.
	resets map[string]time.Time
}

// Reduced reports whether the cap is below the roster size.
func (c Cap) Reduced() bool { return c.Effective < c.Roster }

// Closed says why a worker of the harness kind has no account with headroom,
// so assigning it would only park the slot; empty when it has.
func (c Cap) Closed(kind string) string { return c.why[kindOrDefault(kind)] }

// kindOrDefault is the kind of an adapter, the default one for "" or a kind
// that is not a worker harness.
func kindOrDefault(kind string) string { return harness.ResolveLauncher(kind).Kind() }

// pool is one account pool: the resets of its cooling accounts and whether
// every configured account is cooling (an unconfigured pool is never closed).
type pool struct {
	resets []time.Time
	// names are the cooling accounts, in config order.
	names  []string
	closed bool
}

// meterPool reads a metered kind's accounts through their usage meters.
// Accounts whose usage cannot be read count as free.
func (e Env) meterPool(ctx context.Context, root string) pool {
	var p pool
	if e.Worker.Accounts() == nil {
		return p
	}
	meters := e.Worker.Accounts().Meters(ctx, root)
	for _, m := range meters {
		if m.Verdict == worker.VerdictCooling && m.ResetsAt != nil {
			p.resets = append(p.resets, *m.ResetsAt)
			p.names = append(p.names, m.Name)
		}
	}
	p.closed = len(meters) > 0 && len(p.resets) == len(meters)
	return p
}

// loggedPool reads the accounts of a kind with no usage meter through the
// limits the watcher logged.
func (e Env) loggedPool(root string, h harness.Accounts) (p pool, configured bool) {
	logins := h.Accounts(harness.ConfigFor(e.Worker.Config(root), h.Kind())).Entries
	cooling := limits.Cooling(limits.Load(root), h.Kind(), e.now())
	for _, l := range logins {
		if at, ok := cooling[l.Name]; ok {
			p.resets = append(p.resets, at)
			p.names = append(p.names, l.Name)
		}
	}
	p.closed = len(logins) > 0 && len(p.resets) == len(logins)
	return p, len(logins) > 0
}

// poolOf is the pool of a kind, and whether it has configured accounts.
func (e Env) poolOf(ctx context.Context, root string, h harness.Accounts) (pool, bool) {
	if h.UsageMeter() {
		return e.meterPool(ctx, root), false
	}
	return e.loggedPool(root, h)
}

// poolKey is the config key of the kind's accounts.
func (e Env) poolKey(root string, h harness.Accounts) string {
	return h.Accounts(harness.ConfigFor(e.Worker.Config(root), h.Kind())).Key
}

// Headroom says why a worker of the harness kind has no account to run under,
// reading only that kind's pool; empty when it has one.
func (e Env) Headroom(ctx context.Context, root, kind string) string {
	h := harness.ResolveAccounts(kind)
	if p, _ := e.poolOf(ctx, root, h); p.closed {
		return coolingWhy(e.poolKey(root, h), earliest(p.resets)) + " (" + strings.Join(p.names, ", ") + ")"
	}
	return ""
}

// QuotaCap reads the account headroom behind the roster. The cap drops to the
// slots already holding an issue when no pool a worker could run under has
// headroom. A pool counts when its kind is one the round may use
// (allowedKinds: round.harnesses, else every kind with a round.accounts entry,
// else claude).
func (e Env) QuotaCap(ctx context.Context, root string, set roundcfg.Settings) Cap {
	reg := worker.LoadRegistryTolerant(root) // no error return: a cap is advisory, the assign verb reads the registry strictly
	active, _ := Members(reg, set)
	c := Cap{Roster: len(active), Effective: len(active), why: map[string]string{}, resets: map[string]time.Time{}}
	allowed := allowedKinds(e.Worker.Config(root), set)
	var open bool
	var resets []time.Time
	var pools []string
	for _, k := range harness.Kinds() {
		h := harness.ResolveAccounts(k)
		p, _ := e.poolOf(ctx, root, h)
		key := e.poolKey(root, h)
		if p.closed {
			c.why[k] = coolingWhy(key, earliest(p.resets))
			c.resets[k] = earliest(p.resets)
		}
		if !slices.Contains(allowed, k) {
			continue
		}
		if !p.closed {
			open = true
		}
		resets, pools = append(resets, p.resets...), append(pools, key)
	}
	if open || len(pools) == 0 {
		return c
	}
	c.ResumesAt = earliest(resets)
	c.Reason = coolingWhy(strings.Join(pools, " and "), c.ResumesAt)
	held := 0
	for _, name := range active {
		if s := reg.Slot(name); s != nil && !SlotIdle(s) {
			held++
		}
	}
	c.Effective = held
	return c
}

// coolingWhy is the reason text: which accounts, and when the first resets.
func coolingWhy(pool string, resumes time.Time) string {
	return fmt.Sprintf("every %s account is cooling down; slots fill again at %s", pool, resumes.UTC().Format("2006-01-02 15:04 MST"))
}

func earliest(ts []time.Time) time.Time {
	out := ts[0]
	for _, t := range ts[1:] {
		if t.Before(out) {
			out = t
		}
	}
	return out
}
