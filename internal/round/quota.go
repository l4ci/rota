package round

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/limits"
	"github.com/l4ci/rota/internal/rotatree"
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
	// claudeWhy and codexWhy are set when every configured account of the pool
	// is cooling down. An unconfigured pool is never closed.
	claudeWhy, codexWhy string
}

// Reduced reports whether the cap is below the roster size.
func (c Cap) Reduced() bool { return c.Effective < c.Roster }

// Closed says why a worker of the harness kind (claude or codex) has no account
// with headroom, so assigning it would only park the slot; empty when it has.
func (c Cap) Closed(kind string) string {
	if kind == harness.Codex {
		return c.codexWhy
	}
	return c.claudeWhy
}

// pool is one account pool: the resets of its cooling accounts and whether
// every configured account is cooling (an unconfigured pool is never closed).
type pool struct {
	resets []time.Time
	closed bool
}

// claudePool reads work.accounts through their usage meters. Accounts whose
// usage cannot be read count as free.
func (e Env) claudePool(ctx context.Context, root string) pool {
	var p pool
	if e.Accounts == nil {
		return p
	}
	meters := e.Accounts.Meters(ctx, root)
	for _, m := range meters {
		if m.Verdict == worker.VerdictCooling && m.ResetsAt != nil {
			p.resets = append(p.resets, *m.ResetsAt)
		}
	}
	p.closed = len(meters) > 0 && len(p.resets) == len(meters)
	return p
}

// codexPool reads work.codexAccounts through the limits the watcher logged:
// Codex has no usage meter.
func (e Env) codexPool(root string) (p pool, configured bool) {
	logins := config.CodexAccounts(config.Load(rotatree.Config(root)))
	cooling := limits.Cooling(root, limits.KindCodex, e.now())
	for _, l := range logins {
		if at, ok := cooling[l.Name]; ok {
			p.resets = append(p.resets, at)
		}
	}
	p.closed = len(logins) > 0 && len(p.resets) == len(logins)
	return p, len(logins) > 0
}

// Headroom says why a worker of the harness kind has no account to run under,
// reading only that kind's pool; empty when it has one.
func (e Env) Headroom(ctx context.Context, root, kind string) string {
	if kind == harness.Codex {
		if p, _ := e.codexPool(root); p.closed {
			return coolingWhy("work.codexAccounts", earliest(p.resets))
		}
		return ""
	}
	if p := e.claudePool(ctx, root); p.closed {
		return coolingWhy("work.accounts", earliest(p.resets))
	}
	return ""
}

// QuotaCap reads the account headroom behind the roster. The cap drops to the
// slots already holding an issue when no pool a worker could run under has
// headroom: Claude's counts unless round.workerKind is codex, Codex's when it
// has logins or is the worker kind.
func (e Env) QuotaCap(ctx context.Context, root string, set roundcfg.Settings) Cap {
	c := Cap{Roster: len(set.Roster), Effective: len(set.Roster)}
	claude := e.claudePool(ctx, root)
	codex, codexConfigured := e.codexPool(root)
	if claude.closed {
		c.claudeWhy = coolingWhy("work.accounts", earliest(claude.resets))
	}
	if codex.closed {
		c.codexWhy = coolingWhy("work.codexAccounts", earliest(codex.resets))
	}
	claudeIn, codexIn := set.WorkerKind != harness.Codex, codexConfigured || set.WorkerKind == harness.Codex
	if (claudeIn && !claude.closed) || (codexIn && !codex.closed) || !(claudeIn || codexIn) {
		return c
	}
	var resets []time.Time
	var pools []string
	if claudeIn {
		resets, pools = append(resets, claude.resets...), append(pools, "work.accounts")
	}
	if codexIn {
		resets, pools = append(resets, codex.resets...), append(pools, "work.codexAccounts")
	}
	c.ResumesAt = earliest(resets)
	c.Reason = coolingWhy(strings.Join(pools, " and "), c.ResumesAt)
	held := 0
	reg := worker.LoadRegistry(root)
	for _, name := range set.Roster {
		if s := reg.Slot(name); s != nil && s.HeldID() != "" {
			held++
		}
	}
	c.Effective = held
	return c
}

// coolingWhy is the reason text: which accounts, and when the first resets.
func coolingWhy(pool string, resumes time.Time) string {
	return fmt.Sprintf("every %s account is cooling down; slots fill again at %s", pool, resumes.UTC().Format("15:04 MST"))
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
