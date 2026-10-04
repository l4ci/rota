package round

import (
	"context"
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/jsonx"
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
	Getenv     func(string) string
	DefaultNum int // slots when Slots is 0
	// Dispatch is work.dispatch and LookPath the PATH probe; together with
	// Getenv they resolve the round's host (C8). A nil LookPath is exec.LookPath.
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
		return res, &worker.Error{Exit: worker.ExitUsage,
			Message: fmt.Sprintf("%d slots asked for but round.roster names %d agents; extend round.roster or lower --slots", n, len(set.Roster))}
	}
	if o.Scope == roundcfg.ScopeSlate && len(o.Items) == 0 {
		return res, &worker.Error{Exit: worker.ExitUsage, Message: "scope slate needs --items <ID>[,<ID>…]"}
	}
	if o.Scope != roundcfg.ScopeSlate && len(o.Items) > 0 {
		return res, &worker.Error{Exit: worker.ExitUsage, Message: "--items only applies to scope slate"}
	}
	base := o.Base
	if base == "" {
		base = e.Base
	}
	res.Scope, res.Base = o.Scope, base

	cd, err := e.commonDir(ctx, root)
	if err != nil {
		return res, err
	}
	le := e.leaseEnv()
	holder := le.Discover(o.HolderPID, o.Getenv)
	prev := 0
	if v, ok := worker.LoadRegistry(root).Doc.Get("round"); ok {
		switch t := v.(type) {
		case float64:
			prev = int(t)
		case interface{ Int64() (int64, error) }:
			i, _ := t.Int64()
			prev = int(i)
		}
	}
	l, out, stale, err := le.Acquire(cd, root, holder, prev+1)
	if err != nil {
		if held, ok := err.(*roundlease.HeldError); ok {
			return res, &worker.Error{Exit: worker.ExitRefused, Message: held.Error(), Data: held}
		}
		return res, &worker.Error{Exit: worker.ExitUnavailable, Message: err.Error()}
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

	pool, err := worker.Env{Git: e.Git}.PoolInit(ctx, root, worker.InitOpts{
		Base: base, Session: "rota", Names: set.Roster[:n], BranchPrefix: "park/",
	}, nil)
	if err != nil {
		return res, err
	}
	res.Changed = pool.Changed || out != roundlease.Renewed
	res.Warnings = append(res.Warnings, pool.Warnings...)

	slate := normaliseSlate(o.Items)
	if err := worker.Update(root, slotsDefault(), func(doc *jsonx.Object) {
		if out != roundlease.Renewed { // taken, reclaimed or numbered
			doc.Set("round", l.Round)
		}
		// The host is chosen once per round: a restarted start keeps it, a new
		// round (a newly taken lease) resolves again.
		if rec := worker.Str(doc, "host"); rec != "" && out == roundlease.Renewed {
			res.Host = rec
		} else {
			res.Host = host.ResolveRound(o.Dispatch, o.Getenv, o.LookPath)
			doc.Set("host", res.Host)
		}
		doc.Set("scope", o.Scope)
		if o.Scope == roundcfg.ScopeSlate {
			doc.Set("slate", strs2any(slate))
		} else {
			doc.Delete("slate")
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
	for _, s := range worker.LoadRegistry(root).Slots() {
		if want[worker.Str(s, "name")] {
			res.Slots = append(res.Slots, worker.SlotData(s))
		}
	}
	return res, nil
}

// SlateOf is the recorded slate and scope of the round, "" and nil when none.
func SlateOf(root string) (scope string, slate []string) {
	doc := worker.LoadRegistry(root).Doc
	if v, ok := doc.Get("scope"); ok {
		scope, _ = v.(string)
	}
	if v, ok := doc.Get("slate"); ok {
		if l, ok := v.([]any); ok {
			for _, e := range l {
				if s, ok := e.(string); ok {
					slate = append(slate, s)
				}
			}
		}
	}
	return
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
