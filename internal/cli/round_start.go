package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/blockedby"
	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/hook"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/pystr"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/roundlease"
	"github.com/l4ci/rota/internal/worker"
)

// The C3 verbs `rota round start` and `rota round candidates`; the lease,
// provisioning and readiness are internal/round, the config is internal/roundcfg.

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
		setIf(o, "harness", c.Pick.Harness)
		setIf(o, "model", c.Pick.Model)
		setIf(o, "pickError", c.PickErr)
		setIf(o, "decision", c.Decision)
		setIf(o, "blockedBy", c.BlockedBy)
		if c.StaleHolder != "" {
			o.Set("staleHolder", c.StaleHolder)
			o.Set("lastActivity", c.LastActivity.Format(time.RFC3339))
		}
		o.Set("ready", c.Ready())
		o.Set("scopeSource", c.ScopeSource)
		if c.OpenPR != 0 {
			o.Set("openPr", c.OpenPR)
		}
		if c.DependsOnPR != 0 {
			o.Set("dependsOnPr", c.DependsOnPR)
		}
		o.Set("checks", checkList(c.Checks))
		if len(c.Overlaps) > 0 {
			o.Set("overlaps", overlapList(c.Overlaps))
		}
		out = append(out, o)
	}
	return out
}

func warnCandidateMigrations(c *Ctx, candidates []round.Candidate) {
	for _, candidate := range candidates {
		for _, warning := range candidate.Warnings {
			// A watch reuses its context across ticks; warn once per message.
			if slices.Contains(c.warnings, warning) {
				continue
			}
			c.Warn("%s", warning)
		}
	}
}

func candidateLines(cs []round.Candidate) []string {
	var lines []string
	for _, c := range cs {
		state := "ready"
		if why := c.NotReady(); len(why) > 0 {
			state = "not ready: " + strings.Join(why, "; ")
		}
		if p := c.Pick.String(); p != "" {
			state += " [" + p + "]"
		}
		if c.Ready() {
			for _, o := range c.Overlaps {
				state += "; " + o.String()
			}
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
	setIf(o, "paneHost", l.PaneHost.String())
	o.Set("root", l.Root)
	o.Set("round", l.Round)
	o.Set("startedAt", l.StartedAt)
	o.Set("state", string(st))
	return o
}

func roundStart(fs *flag.FlagSet) RunFunc {
	scope := fs.String("scope", "", "which issues the round may take: slate, milestone, next or open (default round.scope; a re-run keeps the round's)")
	items := fs.String("items", "", "the approved slate, for scope slate")
	slots := fs.Int("slots", 0, "roster slots the round uses, the first N names (default round.slots); slots past N that hold work drain")
	base := fs.String("base", "", "base branch (default git.baseBranch, else the current branch)")
	consume := fs.Bool("consume-handoff", false, "after reporting the orchestrator handoff note, archive it to <note>.consumed")
	pid := fs.Int("holder-pid", 0, "orchestrator pid for the lease, when its ancestry cannot be read")
	return func(c *Ctx, args []string) (Result, error) {
		if err := noArgs(args); err != nil {
			return Result{}, err
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		set, err := c.deps().roundSettings(root, roundConfigUsage)
		if err != nil {
			return Result{}, err
		}
		if res, err := supportRoundStartStop(c, root, set); err != nil {
			return res, err
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
		cfg := c.deps().Config(root)
		env := c.deps().RoundEnv(ctx, root)
		st, err := env.Start(ctx, root, round.StartOpts{
			Scope: sc, Items: pystr.SplitCSV(*items), Slots: *slots, Base: *base, HolderPID: *pid,
			Settings: set, DefaultNum: set.Slots, Dispatch: config.Dispatch(cfg),
		})
		for _, w := range st.Warnings {
			c.Warn("%s", w)
		}
		if w := worker.ConfigDirMismatch(c.deps().Getenv("CLAUDE_CONFIG_DIR"), config.AccountsOf(cfg, config.KindClaude), homeOf(c.deps().UserHomeDir)); w != "" {
			c.Warn("%s", w)
		}
		if err != nil {
			var we *exitcode.Error
			if errors.As(err, &we) && we.Exit == exitcode.ExitRefused {
				if held, ok := we.Data.(*roundlease.HeldError); ok {
					d := jsonx.NewObject()
					d.Set("blockedBy", blockedby.LeaseHeld)
					d.Set("lease", leaseData(held.Lease, held.State))
					d.Set("changed", false)
					return Result{Data: d}, &Error{Exit: ExitRefused, Message: we.Message}
				}
			}
			return Result{}, err
		}
		// Start has just recorded the round's host (C8): rebuild the env so the
		// drift count asks that host, not the guess made before it existed.
		env = c.deps().RoundEnv(ctx, root)
		be, err := openBacklog(c, root, false, "")
		if err != nil {
			return backlogFail(err)
		}
		cands, err := env.RoundCandidates(ctx, root, be, set, "")
		warnCandidateMigrations(c, cands)
		if err != nil {
			return backlogFail(err)
		}
		drift := 0
		if b, ok := round.BoardOf(be); ok && be.Capabilities().ClaimWrites {
			env.Board = b
		}
		if rep, err := env.Status(ctx, root); err != nil {
			c.Warn("drift count skipped: %v", err)
		} else {
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
		notePath := handoffFile(c.deps(), root, cfg)
		if notePath == "" {
			c.Warn("%s: the orchestrator handoff note was not read", noHandoffBaseMsg(cfg))
		}
		note, noteWarn := hook.ReadOrchestratorNote(notePath, *consume)
		if noteWarn != "" {
			c.Warn("%s", noteWarn)
		}
		var hn *handoffNote
		if note != nil {
			hn = &handoffNote{path: notePath, heading: note.Heading, consumed: note.Consumed, consumeFailed: note.ConsumeFailed}
			d.Set("handoff", hn.data())
		}
		var archLine string
		if a, err := env.Architecture(ctx, root, be, set, cands); err == nil {
			d.Set("architecture", architectureData(a))
			archLine = a.Line()
		}
		d.Set("drift", drift)
		cwd, _ := c.deps().Getwd()
		stale, isStale := staleBinary(ctx, c.deps().Git, cwd)
		if isStale {
			d.Set("staleBinary", staleData(stale))
			c.Warn("%s; rebuild: %s", stale.Detail(), stale.Rebuild)
		}
		noVerify := c.deps().workerEnv().NothingToVerify(root)
		d.Set("noVerify", noVerify)
		if noVerify {
			c.Warn("%s", noVerifyWarning)
		}
		if c.deps().workerEnv().GateConfig(root).FullOnly() {
			d.Set("noFast", true)
			c.Warn("%s", noFastWarning)
		}
		d.Set("lease", leaseData(st.Lease, st.LeaseState))
		d.Set("reclaimed", st.Outcome == roundlease.Reclaimed)
		d.Set("changed", st.Changed)
		lines := []string{fmt.Sprintf("round %d\t%s\t%s", st.Round, st.Scope, st.Base)}
		for _, s := range st.Slots {
			line := slotLine(s)
			if jsonx.Bool(s, "draining") {
				line += "\tdraining"
			}
			lines = append(lines, line)
		}
		lines = append(lines, candidateLines(cands)...)
		lines = emptyNote(ctx, d, lines, env, root, be, st.Scope, st.Slate, cands)
		if hn != nil {
			lines = append(lines, hn.line())
		}
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
		set, err := c.deps().roundSettings(root, roundConfigRequired)
		if err != nil {
			return Result{}, err
		}
		sc, slate := round.ScopeOf(root, set, *scope)
		if !roundcfg.ValidScope(sc) {
			return Result{}, Usage("--scope must be one of %s", strings.Join(roundcfg.Scopes, ", "))
		}
		ctx := c.Context()
		be, err := openBacklog(c, root, false, "")
		if err != nil {
			return backlogFail(err)
		}
		env := c.deps().RoundEnv(ctx, root)
		cands, err := env.RoundCandidates(ctx, root, be, set, *scope)
		warnCandidateMigrations(c, cands)
		if err != nil {
			return backlogFail(err)
		}
		d := jsonx.NewObject()
		d.Set("scope", sc)
		d.Set("candidates", candidateList(cands))
		lines := emptyNote(ctx, d, candidateLines(cands), env, root, be, sc, slate, cands)
		if st := quotaFallback(c, ctx, root, set); st.Active {
			fb, _ := fallbackData(st)
			d.Set("fallback", fb)
			lines = append([]string{fmt.Sprintf("fallback\tnew work goes to %s; %s accounts reset at %s", st.To, st.From, st.ResetsAt.UTC().Format(fallbackTime))}, lines...)
		}
		if a, err := env.Architecture(ctx, root, be, set, cands); err == nil {
			d.Set("architecture", architectureData(a))
			if l := a.Line(); l != "" {
				lines = append(lines, "architecture\t"+l)
			}
		}
		return Result{Data: d, Text: strings.Join(lines, "\n")}, nil
	}
}

// noFastWarning tells the orchestrator what an empty test.fast costs a round.
const noFastWarning = "test.fast is empty: workers run test.full before `rota worker done` and record no proof rows, so acceptance shows unproven; reviewers treat that as minor at most"

// noVerifyWarning is the early form of the gate's no-verify refusal, shared by
// `rota init` and `rota round start`.
const noVerifyWarning = "test.full is empty: the merge gate will refuse to merge until it is set (" + worker.NoVerifyHint + ")"

// handoffNote is the orchestrator note `rota round start` reports.
type handoffNote struct {
	path, heading string
	consumed      bool
	consumeFailed bool
}

func (n *handoffNote) data() *jsonx.Object {
	o := jsonx.NewObject()
	o.Set("path", n.path)
	setIf(o, "heading", n.heading)
	if n.consumed {
		o.Set("consumed", true)
	}
	return o
}

func (n *handoffNote) line() string {
	l := "handoff\t" + n.path
	if n.heading != "" {
		l += "\t" + n.heading
	}
	if n.consumed {
		return l + "\tconsumed: read " + n.path + ".consumed"
	}
	if n.consumeFailed {
		return l + "\tconsume failed: note left in place"
	}
	return l + "\tread it before choosing the slate, then rerun with --consume-handoff"
}
