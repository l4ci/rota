package round

import (
	"context"
	"fmt"
	"github.com/l4ci/rota/internal/exitcode"
	"strings"

	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/ledger"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/roundlease"
	"github.com/l4ci/rota/internal/worker"
)

// StartOpts are the flags of `rota round start`, with the config already read.
type StartOpts struct {
	Scope      string
	Items      []string // the slate, for scope slate
	Slots      int      // 0 means work.workerSlots, capped by the caller
	Base       string
	HolderPID  int
	Settings   roundcfg.Settings
	DefaultNum int // slots when Slots is 0
	// Dispatch is work.dispatch and LookPath the PATH probe; together with
	// the env's Getenv they resolve the round's host (C8). A nil LookPath is exec.LookPath.
	Dispatch string
	LookPath func(string) (string, error)
}

// Started is what Start did.
type Started struct {
	Round      int
	Scope      string
	Base       string
	Slots      []*jsonx.Object
	Slate      []string
	Host       string // the round's host: herdr, tmux or solo
	Lease      Lease
	LeaseState LeaseState
	Reclaimed  Lease // the stale lease replaced, zero when none
	Outcome    roundlease.Outcome
	Changed    bool
	Warnings   []string
}

// Start takes the lease, provisions the roster slots and records the scope.
// It starts no agent and does not touch the backlog. The caller computes
// candidates and drift afterwards.
func (e Env) Start(ctx context.Context, root string, o StartOpts) (Started, error) {
	var res Started
	set := o.Settings
	n := o.Slots
	if n <= 0 {
		n = o.DefaultNum
	}
	if n <= 0 || n > len(set.Roster) {
		return res, &exitcode.Error{Exit: exitcode.ExitUsage,
			Message: fmt.Sprintf("%d slots asked for but round.roster names %d agents; extend round.roster or lower --slots", n, len(set.Roster))}
	}
	// An empty Scope means --scope was not given. Items alone make a slate; a
	// round that is not yet known to renew is checked before it takes the lease.
	scope := o.Scope
	if scope == "" && len(o.Items) > 0 {
		scope = roundcfg.ScopeSlate
	}
	checkScope := func(scope string) error {
		if scope == roundcfg.ScopeSlate && len(o.Items) == 0 {
			return &exitcode.Error{Exit: exitcode.ExitUsage, Message: "scope slate needs --items <ID>[,<ID>…]"}
		}
		if scope != roundcfg.ScopeSlate && len(o.Items) > 0 {
			return &exitcode.Error{Exit: exitcode.ExitUsage, Message: "--items only applies to scope slate"}
		}
		return nil
	}
	recScope, recSlate := SlateOf(root)
	if scope != "" {
		if err := checkScope(scope); err != nil {
			return res, err
		}
	} else if recScope == "" {
		if err := checkScope(set.Scope); err != nil {
			return res, err
		}
	}
	base := o.Base
	if base == "" {
		base = e.Base
	}
	res.Base = base

	cd, err := e.commonDir(ctx, root)
	if err != nil {
		return res, err
	}
	le := e.Lease
	holder := le.Discover(o.HolderPID, e.getenv())
	reg, err := worker.LoadRegistry(root)
	if err != nil {
		return res, err
	}
	prev, _ := reg.Round()
	l, out, stale, err := le.Acquire(cd, root, holder, prev+1)
	if err != nil {
		if held, ok := err.(*roundlease.HeldError); ok {
			return res, &exitcode.Error{Exit: exitcode.ExitRefused, Message: held.Error(), Data: held}
		}
		return res, &exitcode.Error{Exit: exitcode.ExitUnavailable, Message: err.Error()}
	}
	res.Lease, res.Outcome, res.LeaseState = l, out, roundlease.Live
	if out == roundlease.Reclaimed {
		res.Reclaimed = stale
		who := "an unreadable lease file"
		if stale.PID > 0 {
			who = fmt.Sprintf("pid %d", stale.PID)
		}
		res.Warnings = append(res.Warnings, "reclaimed stale lease held by "+who)
	}

	// A new round ages the ledger out; a renewed one has nothing new to age.
	if out != roundlease.Renewed {
		if _, err := ledger.Trim(root, l.Round, set.LedgerKeep); err != nil {
			res.Warnings = append(res.Warnings, "round ledger not trimmed: "+err.Error())
		}
	}

	// A renewed start without --scope keeps the recorded scope and slate; a new
	// round falls back to round.scope.
	keep := scope == "" && out == roundlease.Renewed && recScope != ""
	var slate []string
	switch {
	case keep:
		scope, slate = recScope, recSlate
	default:
		if scope == "" {
			scope = set.Scope
		}
		if err := checkScope(scope); err != nil {
			return res, err
		}
		slate = normaliseSlate(o.Items)
	}
	res.Scope = scope

	pool, err := worker.Env{Git: e.Git}.PoolInit(ctx, root, worker.InitOpts{
		Base: base, Session: "rota", Names: set.Roster[:n], BranchPrefix: worker.ParkPrefix,
	}, nil)
	if err != nil {
		return res, err
	}
	res.Changed = pool.Changed || out != roundlease.Renewed
	res.Warnings = append(res.Warnings, pool.Warnings...)

	if err := worker.Update(root, func(doc *worker.Doc) {
		if out != roundlease.Renewed { // taken, reclaimed or numbered
			doc.SetRound(l.Round)
			// The layout belongs to one round. The CLI pane is kept: `rota
			// orchestrate` records it just before the orchestrator runs this.
			doc.SetLayout("")
		}
		// The host is chosen once per round: a restarted start keeps it, a new
		// round (a newly taken lease) resolves again.
		if rec := doc.Host(); rec != "" && out == roundlease.Renewed {
			res.Host = rec
		} else {
			res.Host = host.Resolve("", o.Dispatch, e.getenv(), o.LookPath)
			doc.SetHost(res.Host)
		}
		doc.SetScope(scope)
		if scope == roundcfg.ScopeSlate {
			doc.SetSlate(slate)
		} else {
			doc.ClearSlate()
		}
	}); err != nil {
		return res, err
	}
	res.Round, res.Slate = l.Round, slate
	// Only roster slots are the round's: slots from `pool init` stay out.
	want := map[string]bool{}
	for _, name := range set.Roster {
		want[name] = true
	}
	reg, err = worker.LoadRegistry(root)
	if err != nil {
		return res, err
	}
	for _, s := range reg.Slots() {
		if want[s.Name()] {
			res.Slots = append(res.Slots, worker.SlotData(s))
		}
	}
	return res, nil
}

// SlateOf is the recorded slate and scope of the round, "" and nil when none.
func SlateOf(root string) (scope string, slate []string) {
	reg := worker.LoadRegistryTolerant(root) // a display and slate-guard read; round start reads the registry strictly
	return reg.Scope(), reg.Slate()
}

func normaliseSlate(items []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, it := range items {
		it = strings.ToUpper(strings.TrimPrefix(strings.TrimSpace(it), "#"))
		if it != "" && !seen[it] {
			seen[it] = true
			out = append(out, it)
		}
	}
	return out
}

func strs2any(l []string) []any {
	out := make([]any, 0, len(l))
	for _, s := range l {
		out = append(out, s)
	}
	return out
}
