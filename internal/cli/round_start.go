package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/roundlease"
	"github.com/l4ci/rota/internal/worker"
	"path/filepath"
)

// The C3 verbs `rota round start` and `rota round candidates`; the lease,
// provisioning and readiness are internal/round, the config is internal/roundcfg.

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func checkList(cs []round.Check) []any {
	out := make([]any, 0, len(cs))
	for _, c := range cs {
		o := jsonx.NewObject()
		o.Set("name", c.Name)
		o.Set("ok", c.OK)
		o.Set("detail", strs(c.Detail))
		out = append(out, o)
	}
	return out
}

func candidateList(cs []round.Candidate) []any {
	out := make([]any, 0, len(cs))
	for _, c := range cs {
		o := jsonx.NewObject()
		o.Set("id", c.ID)
		o.Set("title", c.Title)
		setIf(o, "milestone", c.Milestone)
		o.Set("ready", c.Ready())
		o.Set("checks", checkList(c.Checks))
		out = append(out, o)
	}
	return out
}

func candidateLines(cs []round.Candidate) []string {
	var lines []string
	for _, c := range cs {
		state := "ready"
		if !c.Ready() {
			var bad []string
			for _, ch := range c.Checks {
				if !ch.OK {
					bad = append(bad, ch.Name)
				}
			}
			state = "not ready: " + strings.Join(bad, ",")
		}
		lines = append(lines, fmt.Sprintf("%s\t%s\t%s", c.ID, state, c.Title))
	}
	return lines
}

// emptyNote adds the reason and next command to d and to lines when the scope
// offers no candidates, and says when a milestone scope fell back to every
// open item. An empty list is never silent.
func emptyNote(ctx context.Context, d *jsonx.Object, lines []string, env round.Env, root string, be backlog.Backend, scope string, slate []string, cands []round.Candidate) []string {
	if round.FellBack(root, scope) {
		d.Set("fellBack", true)
		lines = append(lines, fmt.Sprintf("note\tno unfinished milestone: scope %s offers every open item", scope))
	}
	if len(cands) > 0 {
		return lines
	}
	why, err := env.WhyEmpty(ctx, root, be, scope, slate)
	if err != nil {
		return lines
	}
	e := jsonx.NewObject()
	e.Set("reason", why.Reason)
	e.Set("next", why.Next)
	d.Set("empty", e)
	return append(lines, fmt.Sprintf("no candidates: %s\tnext: %s", why.Reason, why.Next))
}

func leaseData(l round.Lease, st round.LeaseState) *jsonx.Object {
	o := jsonx.NewObject()
	o.Set("pid", l.PID)
	if l.Start != 0 {
		o.Set("start", int64(l.Start))
	}
	o.Set("host", l.Host)
	setIf(o, "pane", l.Pane)
	setIf(o, "paneHost", l.PaneHost)
	o.Set("root", l.Root)
	o.Set("round", l.Round)
	o.Set("startedAt", l.StartedAt)
	o.Set("state", string(st))
	return o
}

func roundStart(fs *flag.FlagSet) RunFunc {
	scope := fs.String("scope", "", "which issues the round may take: slate, milestone, next or open (default round.scope; a re-run keeps the round's)")
	items := fs.String("items", "", "the approved slate, for scope slate")
	slots := fs.Int("slots", 0, "roster slots to provision (default work.workerSlots)")
	base := fs.String("base", "", "base branch (default git.baseBranch, else the current branch)")
	pid := fs.Int("holder-pid", 0, "orchestrator pid for the lease, when its ancestry cannot be read")
	return func(c *Ctx, args []string) (Result, error) {
		if err := noArgs(args); err != nil {
			return Result{}, err
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		set, err := roundcfg.Load(root)
		if err != nil {
			return Result{}, &Error{Exit: ExitInternal, Message: err.Error()}
		}
		// Empty when --scope was not given: Start keeps a renewed round's scope.
		sc := *scope
		if sc != "" && !roundcfg.ValidScope(sc) {
			return Result{}, Usage("--scope must be one of %s", strings.Join(roundcfg.Scopes, ", "))
		}
		if *slots < 0 {
			return Result{}, Usage("--slots must be a positive integer")
		}
		ctx := c.Context()
		cfg := config.Load(filepath.Join(root, ".rota", "config.json"))
		def := 3
		if v, err := config.Value(cfg, "work.workerSlots"); err == nil {
			if n, ok := v.(interface{ Int64() (int64, error) }); ok {
				if i, _ := n.Int64(); i > 0 {
					def = int(i)
				}
			}
		}
		env := roundEnv(ctx, root)
		dispatch, _ := config.Lookup(cfg, "work.dispatch")
		dispatchStr, _ := dispatch.(string)
		st, err := env.Start(ctx, root, round.StartOpts{
			Scope: sc, Items: splitList(*items), Slots: *slots, Base: *base, HolderPID: *pid,
			Settings: set, Getenv: os.Getenv, DefaultNum: def, Dispatch: dispatchStr,
		})
		for _, w := range st.Warnings {
			c.Warn("%s", w)
		}
		if err != nil {
			var we *worker.Error
			if errors.As(err, &we) && we.Exit == worker.ExitRefused {
				if held, ok := we.Data.(*roundlease.HeldError); ok {
					d := jsonx.NewObject()
					d.Set("blockedBy", "lease held")
					d.Set("lease", leaseData(held.Lease, held.State))
					d.Set("changed", false)
					return Result{Data: d}, &Error{Exit: ExitRefused, Message: we.Message}
				}
			}
			return Result{}, fromWorker(err)
		}
		// Start has just recorded the round's host (C8): rebuild the env so the
		// drift count asks that host, not the guess made before it existed.
		env = roundEnv(ctx, root)
		be, err := a4Open(c, root, false, "")
		if err != nil {
			return a4Fail(err)
		}
		cands, err := env.Candidates(ctx, root, be, round.CandidateOpts{Scope: st.Scope, Slate: st.Slate, Shared: set.SharedPaths})
		if err != nil {
			return a4Fail(err)
		}
		drift := 0
		if b, ok := be.(round.Board); ok && be.Name() == "issues" {
			env.Board = b
		}
		if rep, err := env.Status(ctx, root); err == nil {
			drift = len(rep.Findings)
		}
		d := jsonx.NewObject()
		d.Set("round", st.Round)
		d.Set("scope", st.Scope)
		d.Set("base", st.Base)
		d.Set("slots", slotList(st.Slots))
		if st.Scope == roundcfg.ScopeSlate {
			d.Set("slate", strs(st.Slate))
		}
		d.Set("candidates", candidateList(cands))
		var archLine string
		if a, err := env.Architecture(ctx, root, be, set, cands); err == nil {
			d.Set("architecture", architectureData(a))
			archLine = a.Line()
		}
		d.Set("drift", drift)
		d.Set("lease", leaseData(st.Lease, st.LeaseState))
		d.Set("reclaimed", st.Outcome == roundlease.Reclaimed)
		d.Set("changed", st.Changed)
		lines := []string{fmt.Sprintf("round %d\t%s\t%s", st.Round, st.Scope, st.Base)}
		for _, s := range st.Slots {
			lines = append(lines, slotLine(s))
		}
		lines = append(lines, candidateLines(cands)...)
		lines = emptyNote(ctx, d, lines, env, root, be, st.Scope, st.Slate, cands)
		if archLine != "" {
			lines = append(lines, "architecture\t"+archLine)
		}
		return Result{Data: d, Text: strings.Join(lines, "\n")}, nil
	}
}

func roundCandidates(fs *flag.FlagSet) RunFunc {
	scope := fs.String("scope", "", "slate, milestone, next or open (default the round's scope, then round.scope)")
	return func(c *Ctx, args []string) (Result, error) {
		if err := noArgs(args); err != nil {
			return Result{}, err
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		set, err := roundcfg.Load(root)
		if err != nil {
			return Result{}, &Error{Exit: ExitInternal, Message: err.Error()}
		}
		recScope, slate := round.SlateOf(root)
		sc := set.Scope
		if recScope != "" {
			sc = recScope
		}
		if *scope != "" {
			sc = *scope
		}
		if !roundcfg.ValidScope(sc) {
			return Result{}, Usage("--scope must be one of %s", strings.Join(roundcfg.Scopes, ", "))
		}
		ctx := c.Context()
		be, err := a4Open(c, root, false, "")
		if err != nil {
			return a4Fail(err)
		}
		env := roundEnv(ctx, root)
		cands, err := env.Candidates(ctx, root, be, round.CandidateOpts{Scope: sc, Slate: slate, Shared: set.SharedPaths})
		if err != nil {
			return a4Fail(err)
		}
		d := jsonx.NewObject()
		d.Set("scope", sc)
		d.Set("candidates", candidateList(cands))
		lines := emptyNote(ctx, d, candidateLines(cands), env, root, be, sc, slate, cands)
		if a, err := env.Architecture(ctx, root, be, set, cands); err == nil {
			d.Set("architecture", architectureData(a))
			if l := a.Line(); l != "" {
				lines = append(lines, "architecture\t"+l)
			}
		}
		return Result{Data: d, Text: strings.Join(lines, "\n")}, nil
	}
}
