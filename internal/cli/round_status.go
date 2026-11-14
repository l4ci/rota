package cli

import (
	"errors"
	"flag"
	"fmt"
	"github.com/l4ci/rota/internal/rotastate"
	"github.com/l4ci/rota/internal/strutil"
	"github.com/l4ci/rota/internal/tracker"
	"strings"

	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/landfreeze"
	"github.com/l4ci/rota/internal/limits"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/worker"
)

// The C2 verbs `rota round status` and `rota round reconcile`; the assembly and
// the drift rules are internal/round. Tests swap Deps.RoundEnv for fakes.

func roundStatus(fs *flag.FlagSet) RunFunc {
	needsYou := fs.Bool("needs-you", false, "list only decisions waiting on the maintainer")
	return func(c *Ctx, args []string) (Result, error) {
		if err := noArgs(args); err != nil {
			return Result{}, err
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		ctx := c.Context()
		renv := withBoard(c, root, roundEnv(c, ctx, root))
		readWaits := func() ([]any, []string) {
			waits, warnings := refreshMaintainerSnapshot(ctx, c, root, renv)
			for _, warning := range warnings {
				c.Warn("%s", warning)
			}
			return maintainerRows(waits, c.deps().Now())
		}
		if *needsYou {
			if renv.RoundErr != nil {
				return Result{}, Usage("%v", renv.RoundErr)
			}
			waitRows, waitLines := readWaits()
			d := jsonx.NewObject()
			d.Set("needsYou", waitRows)
			return Result{Data: d, Text: strings.Join(waitLines, "\n")}, nil
		}
		// Status refuses on a bad round.* key (exit 2), like assign and start.
		rep, err := renv.Status(ctx, root)
		if err != nil {
			return Result{}, err
		}
		waitRows, waitLines := readWaits()
		for _, w := range rep.Warnings {
			c.Warn("%s", w)
		}
		warnRepoConfigDrift(c, ctx, root)
		d := jsonx.NewObject()
		d.Set("needsYou", waitRows)
		d.Set("slots", rowList(rep.Rows))
		d.Set("drift", len(rep.Findings))
		d.Set("unavailable", jsonx.Strings(rep.Unavailable))
		if rep.Host != "" {
			d.Set("host", rep.Host.String())
		}
		esc, escLines := escalationRows(rep.Escalations)
		d.Set("escalations", esc)
		lim, limLines := limitStatusRows(rep.Limits)
		d.Set("limits", lim)
		d.Set("review", queuedRows(rep.Queued))
		var lines []string
		if holding := trainHoldingLines(c, root); len(holding) > 0 {
			d.Set("holding", jsonx.Strings(holding))
			lines = append(lines, holding...)
		}
		trainLines := trainStatus(c, root, d)
		lines = append(lines, trainLines...)
		frozen, ferr := landfreeze.Load(ctx, c.deps().Git, root)
		if ferr != nil && !errors.Is(ferr, rotastate.ErrNotRepo) {
			c.Warn("%s", landfreeze.UnreadableLine(ferr))
		}
		if frozen != nil && frozen.Active {
			d.Set("freeze", freezeData(frozen))
			lines = append(lines, frozen.Line())
		}
		if set, ok := c.deps().tolerantRoundSettings(root); ok {
			// Status ran without accounts; the quota read gets its own accounted env.
			qc := accountedRoundEnv(c, ctx, root).QuotaCap(ctx, root, set)
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
			if address := host.HandleAddress(r.Tab); address != r.Tab {
				cols = append(cols, "terminal "+address)
			}
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

		var staleClaims []round.Finding
		for _, finding := range rep.Findings {
			if finding.Kind == round.StaleClaimKind {
				staleClaims = append(staleClaims, finding)
				lines = append(lines, round.StaleClaimKind+"\t"+finding.Detail)
			}
		}
		if len(staleClaims) > 0 {
			d.Set("staleClaims", findingList(staleClaims))
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
		for _, l := range waitLines {
			lines = append(lines, "needs-you\t"+l)
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
			o.Set("bundle", jsonx.Strings(q.Bundle))
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
		renv := withBoard(c, root, roundEnv(c, ctx, root))
		renv.SweepClosedClaims = true
		out, err := renv.Reconcile(ctx, root, *apply)
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
		d.Set("unavailable", jsonx.Strings(out.Report.Unavailable))
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
			o.Set("bundle", jsonx.Strings(r.Bundle))
		}
		setIf(o, "bestOf", r.BestOf)
		o.Set("branch", r.Branch)
		setIf(o, "pr", r.PR)
		setIf(o, "prState", r.PRState)
		setIf(o, "hostState", r.HostState)
		if address := host.HandleAddress(r.Tab); address != r.Tab {
			setIf(o, "address", address)
		}
		if r.HostState == worker.LivenessUnverifiable { // the reason (#820); other rows' evidence is the UI's
			setIf(o, "evidence", r.Evidence)
		}
		setIf(o, "state", r.State)
		setIf(o, "outcome", r.Outcome)
		setIf(o, "tab", r.Tab)
		o.Set("registered", r.Registered)
		o.Set("drift", jsonx.Strings(r.Drift))
		o.Set("escalations", jsonx.Strings(r.Escalations))
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

func dash(s string) string {
	if s == "" || s == "#" {
		return "-"
	}
	return s
}

// hostStateText is the row's host state, with an unverifiable slot's reason
// beside it.
func hostStateText(r round.Row) string {
	if r.HostState == worker.LivenessUnverifiable && r.Evidence != "" {
		return r.HostState + ":" + r.Evidence
	}
	// A partial or failed report shows beside the state; a plain done adds nothing.
	if worker.OutcomeOpen(r.Outcome) {
		return strutil.FirstNonEmpty(r.HostState, "done") + "/" + r.Outcome
	}
	return r.HostState
}
