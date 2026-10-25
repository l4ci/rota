package cli

import (
	"context"
	"flag"
	"fmt"
	"github.com/l4ci/rota/internal/strutil"
	"github.com/l4ci/rota/internal/tracker"
	"strings"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/limits"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/worker"
)

// The C2 verbs `rota round status` and `rota round reconcile`; the assembly and
// the drift rules are internal/round. Tests swap Deps.RoundEnv for fakes.

// defaultRoundEnv wires the real git, host and forge. The host is herdr when
// the round recorded (C8); with none it is herdr when round.dispatch says so or
// the process runs inside herdr (rounds are started by hand with `herdr
// worktree create`, whatever the config says), else tmux.
// Account picking stays off: it is opt-in (accountedRoundEnv), so start, status
// and the other verbs never take that path by accident.
// A host or forge that cannot be built or reached is left nil: the verbs
// report it as unavailable instead of failing. withForge false builds no
// forge at all (Forge and ForgeErr stay empty), for a caller that reads only
// the local sources and would not wait on the network.
func defaultRoundEnv(ctx context.Context, root string, d *Deps, withForge bool) round.Env {
	cfg := d.Config(root)
	e := d.baseRoundEnv()
	e.Worker, e.AccountPicking = e.Worker.WithAccounts(nil), false
	e.Base = "main"
	configured, _ := config.Lookup(cfg, "git.baseBranch")
	baseCfg, _ := configured.(string)
	if b, ok, err := d.Repo(root).Base(ctx, baseCfg); err == nil && ok {
		e.Base = b
	} else if err == nil && baseCfg != "" {
		// A set but missing base is not replaced by main: the verbs refuse.
		e.Base = ""
		e.BaseErr = fmt.Sprintf("git.baseBranch %q does not resolve to a branch (fix or unset it in .rota/config.json)", baseCfg)
	}
	// A round started by hand (`herdr worktree create`) has no recorded host and
	// resolves from the environment like every other verb.
	hostKind := d.resolvePaneHost(root, cfg)
	if hostKind == host.Solo {
		e.HostName = host.Solo // no panes to snapshot, and not an unavailable host
	} else {
		h := d.Host(hostKind)
		if err := h.Require(); err != nil {
			e.HostErr = err.Error()
		} else if s := h.Capabilities().Snapshotter; s != nil {
			e.Snapshot, e.HostName = s.Snapshot, h.Kind()
		}
	}
	labels := roundcfg.LabelsOf(cfg)
	e.NeedsHuman, e.Label, e.SupportLabel, e.NeedsReview = labels.NeedsHuman, labels.InProgress, roundcfg.MachineLabel(roundcfg.SupportName(cfg)), labels.NeedsReview
	e.Round, e.RoundErr = d.roundSettings(root, roundConfigUsage)
	if !withForge {
		return e
	}
	f, err := d.forgeAndIssues(ctx, cfg, root)
	if err != nil {
		e.ForgeErr = err.Error()
	} else {
		e.Forge = f
	}
	return e
}

// accountedRoundEnv is RoundEnv with account picking on, for a verb that reads
// quota or picks an account: the one place a verb opts in, chosen when the env
// is built. The worker env comes from the one constructor (Deps.workerEnv), so
// Git, host and clock match baseRoundEnv.
func accountedRoundEnv(c *Ctx, ctx context.Context, root string) round.Env {
	env := c.deps().RoundEnv(ctx, root)
	env.Worker = round.FromWorkerEnv(workerEnvCtx(c, ctx))
	return env.WithAccounts(c.deps().WorkerAccounts())
}

// withBoard gives the env the backlog's claim side, which claim-mismatch drift
// reads. File mode has no claims, and a backlog that cannot be opened is a
// warning: the other drift kinds still report.
func withBoard(c *Ctx, root string, env round.Env) round.Env {
	if env.Board != nil {
		return env
	}
	if name, _, err := backendMode(c.deps(), root); err != nil || name != "issues" {
		return env
	}
	be, err := openBacklog(c, root, false, "")
	if err != nil {
		c.Warn("claim check skipped: %v", err)
		return env
	}
	if b, ok := round.BoardOf(be); ok {
		env.Board = b
	}
	return env
}

func roundStatus(*flag.FlagSet) RunFunc {
	return func(c *Ctx, args []string) (Result, error) {
		if err := noArgs(args); err != nil {
			return Result{}, err
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		ctx := c.Context()
		renv := withBoard(c, root, c.deps().RoundEnv(ctx, root))
		// Status refuses on a bad round.* key (exit 2), like assign and start.
		rep, err := renv.Status(ctx, root)
		if err != nil {
			return Result{}, err
		}
		for _, w := range rep.Warnings {
			c.Warn("%s", w)
		}
		d := jsonx.NewObject()
		d.Set("slots", rowList(rep.Rows))
		d.Set("drift", len(rep.Findings))
		d.Set("unavailable", strs(rep.Unavailable))
		if rep.Host != "" {
			d.Set("host", rep.Host.String())
		}
		esc, escLines := escalationRows(rep.Escalations)
		d.Set("escalations", esc)
		lim, limLines := limitStatusRows(rep.Limits)
		d.Set("limits", lim)
		d.Set("review", queuedRows(rep.Queued))
		var lines []string
		if set, ok := c.deps().tolerantRoundSettings(root); ok {
			// Status ran without accounts; the quota read gets its own accounted env.
			qc := accountedRoundEnv(c, ctx, root).QuotaCap(ctx, root, set) // one meter read serves the fallback and the cap
			if st := qc.Fallback(set); st.Active {
				fb, _ := fallbackData(st)
				d.Set("fallback", fb)
				lines = append(lines, fmt.Sprintf("fallback\t%s -> %s until %s", st.From, st.To, st.ResetsAt.UTC().Format(fallbackTime)))
			}
			if qc.Reduced() {
				co := jsonx.NewObject()
				co.Set("effective", qc.Effective)
				co.Set("roster", qc.Roster)
				co.Set("reason", qc.Reason)
				co.Set("resumesAt", limits.Time(qc.ResumesAt))
				d.Set("cap", co)
				lines = append(lines, fmt.Sprintf("cap\t%d/%d\t%s", qc.Effective, qc.Roster, qc.Reason))
			}
			if a, err := architectureFor(c, root, set); err == nil {
				d.Set("architecture", architectureData(a))
				if l := a.Line(); l != "" {
					lines = append(lines, "architecture\t"+l)
				}
			}
		}
		for _, r := range rep.Rows {
			issue := dash(r.Issue) + bundleSuffix(r.Bundle)
			if r.BestOf != "" {
				issue += " (best-of: " + r.BestOf + ")"
			}
			cols := []string{r.Name, issue, dash(r.Branch), dash(r.PR), dash(hostStateText(r)), dash(strings.Join(r.Drift, ","))}
			if r.Kind != "" {
				col := strings.TrimSuffix(r.Kind+"/"+r.Model, "/")
				if r.KindSource != "" {
					col += " (" + r.KindSource + ")"
				}
				cols = append(cols, col)
			}
			if r.Account != "" {
				cols = append(cols, "account "+r.Account+" ("+r.AccountKind+")")
			}
			if r.State != "" {
				cols = append(cols, "state "+r.State)
			}
			if r.Bounces > 0 {
				cols = append(cols, fmt.Sprintf("bounces %d", r.Bounces))
			}
			if r.Draining {
				cols = append(cols, "draining")
			}
			if r.NoLogin != "" {
				cols = append(cols, "no login: "+r.NoLogin)
			}
			lines = append(lines, strings.Join(cols, "\t"))
		}
		for _, q := range rep.Queued {
			lines = append(lines, fmt.Sprintf("review\t%s\t%s\t%s\tfrom %s", tracker.RefOf(q.Issue)+bundleSuffix(q.Bundle), dash(q.PR), dash(q.Branch), dash(q.From)))
		}
		for _, l := range escLines {
			lines = append(lines, "escalation\t"+l)
		}
		for _, l := range limLines {
			lines = append(lines, "limit\t"+l)
		}
		return Result{Data: d, Text: strings.Join(lines, "\n"), Source: rep}, nil
	}
}

// bundleSuffix is the status text after a bundle lead: ` (+#1563, +#1564)`,
// nothing for a single issue.
func bundleSuffix(members []string) string {
	if len(members) == 0 {
		return ""
	}
	refs := make([]string, len(members))
	for i, m := range members {
		refs[i] = "+" + tracker.RefOf(m)
	}
	return " (" + strings.Join(refs, ", ") + ")"
}

func queuedRows(qs []round.QueuedPR) []any {
	out := make([]any, 0, len(qs))
	for _, q := range qs {
		o := jsonx.NewObject()
		o.Set("issue", q.Issue)
		setIf(o, "pr", q.PR)
		setIf(o, "branch", q.Branch)
		setIf(o, "from", q.From)
		if len(q.Bundle) > 0 {
			o.Set("bundle", strs(q.Bundle))
		}
		out = append(out, o)
	}
	return out
}

func roundReconcile(fs *flag.FlagSet) RunFunc {
	apply := fs.Bool("apply", false, "make the safe repairs (default: report only)")
	return func(c *Ctx, args []string) (Result, error) {
		if err := noArgs(args); err != nil {
			return Result{}, err
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		ctx := c.Context()
		out, err := withBoard(c, root, c.deps().RoundEnv(ctx, root)).Reconcile(ctx, root, *apply)
		if err != nil {
			return Result{}, err
		}
		for _, w := range out.Report.Warnings {
			c.Warn("%s", w)
		}
		d := jsonx.NewObject()
		d.Set("drift", findingList(out.Drift))
		d.Set("repaired", findingList(out.Repaired))
		d.Set("clean", out.Clean())
		d.Set("unavailable", strs(out.Report.Unavailable))
		d.Set("changed", len(out.Repaired) > 0)
		esc, escLines := escalationRows(out.Report.Escalations)
		d.Set("escalations", esc)
		lim, limLines := limitStatusRows(out.Report.Limits)
		d.Set("limits", lim)
		var lines []string
		for _, f := range out.Drift {
			lines = append(lines, fmt.Sprintf("drift\t%s\t%s\t%s", f.Kind, dash(strutil.FirstNonEmpty(f.Slot, tracker.RefOf(f.Issue))), f.Detail))
		}
		for _, f := range out.Repaired {
			lines = append(lines, fmt.Sprintf("repaired\t%s\t%s\t%s", f.Kind, dash(strutil.FirstNonEmpty(f.Slot, tracker.RefOf(f.Issue))), f.Repair))
		}
		for _, l := range escLines {
			lines = append(lines, "escalation\t"+l)
		}
		for _, l := range limLines {
			lines = append(lines, "limit\t"+l)
		}
		return Result{Data: d, Text: strings.Join(lines, "\n")}, nil
	}
}

func rowList(rows []round.Row) []any {
	out := make([]any, 0, len(rows))
	for _, r := range rows {
		o := jsonx.NewObject()
		o.Set("name", r.Name)
		setIf(o, "agent", r.Agent)
		setIf(o, "issue", r.Issue)
		if len(r.Bundle) > 0 {
			o.Set("bundle", strs(r.Bundle))
		}
		setIf(o, "bestOf", r.BestOf)
		o.Set("branch", r.Branch)
		setIf(o, "pr", r.PR)
		setIf(o, "prState", r.PRState)
		setIf(o, "hostState", r.HostState)
		if r.HostState == "unverifiable" { // the reason (#820); other rows' evidence is the UI's
			setIf(o, "evidence", r.Evidence)
		}
		setIf(o, "state", r.State)
		setIf(o, "outcome", r.Outcome)
		setIf(o, "tab", r.Tab)
		o.Set("registered", r.Registered)
		o.Set("drift", strs(r.Drift))
		o.Set("escalations", strs(r.Escalations))
		setIf(o, "kind", r.Kind)
		setIf(o, "kindSource", r.KindSource)
		setIf(o, "account", r.Account)
		if r.Account != "" {
			o.Set("accountKind", r.AccountKind)
		}
		setIf(o, "tier", r.Tier)
		setIf(o, "model", r.Model)
		setIf(o, "tierReason", r.TierReason)
		o.Set("bounces", r.Bounces)
		if r.Draining {
			o.Set("draining", true)
		}
		setIf(o, "noLogin", r.NoLogin)
		if ev := r.Event; ev != nil {
			eo := jsonx.NewObject()
			eo.Set("seq", ev.Seq)
			eo.Set("kind", ev.Kind)
			setIf(eo, "body", ev.Body)
			eo.Set("ts", ev.TS)
			o.Set("event", eo)
		}
		out = append(out, o)
	}
	return out
}

func findingList(fs []round.Finding) []any {
	out := make([]any, 0, len(fs))
	for _, f := range fs {
		o := jsonx.NewObject()
		o.Set("kind", f.Kind)
		setIf(o, "slot", f.Slot)
		setIf(o, "issue", f.Issue)
		o.Set("detail", f.Detail)
		setIf(o, "repair", f.Repair)
		out = append(out, o)
	}
	return out
}

func setIf(o *jsonx.Object, k, v string) {
	if v != "" {
		o.Set(k, v)
	}
}

func strs(l []string) []any {
	out := make([]any, 0, len(l))
	for _, s := range l {
		out = append(out, s)
	}
	return out
}

func dash(s string) string {
	if s == "" || s == "#" {
		return "-"
	}
	return s
}

// hostStateText is the row's host state, with an unverifiable slot's reason
// beside it.
func hostStateText(r round.Row) string {
	if r.HostState == "unverifiable" && r.Evidence != "" {
		return r.HostState + ":" + r.Evidence
	}
	// A partial or failed report shows beside the state; a plain done adds nothing.
	if worker.OutcomeOpen(r.Outcome) {
		return strutil.FirstNonEmpty(r.HostState, "done") + "/" + r.Outcome
	}
	return r.HostState
}

// quotaFallback reads the fallback state with the account meters.
func quotaFallback(c *Ctx, ctx context.Context, root string, set roundcfg.Settings) round.FallbackState {
	return accountedRoundEnv(c, ctx, root).Fallback(ctx, root, set)
}

// fallbackTime is how a fallback's reset prints in text.
const fallbackTime = "2006-01-02 15:04 MST"

// fallbackData is the `fallback` object of round status and candidates; ok is
// false while no fallback is active.
func fallbackData(st round.FallbackState) (*jsonx.Object, bool) {
	if !st.Active {
		return nil, false
	}
	o := jsonx.NewObject()
	o.Set("from", st.From)
	o.Set("to", st.To)
	o.Set("resetsAt", limits.Time(st.ResetsAt))
	return o, true
}
