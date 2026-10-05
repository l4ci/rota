package cli

import (
	"context"
	"flag"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/worker"
)

// The C2 verbs `rota round status` and `rota round reconcile`; the assembly and
// the drift rules are internal/round. Tests swap Deps.RoundEnv for fakes.

// defaultRoundEnv wires the real git, host and forge. The host is herdr when
// the round recorded (C8); with none it is herdr when work.dispatch says so or
// the process runs inside herdr (rounds are started by hand with `herdr
// worktree create`, whatever the config says), else tmux.
// A host or forge that cannot be built or reached is left nil: the verbs
// report it as unavailable instead of failing.
func defaultRoundEnv(ctx context.Context, root string, d *Deps) round.Env {
	cfg := config.Load(filepath.Join(root, ".rota", "config.json"))
	e := round.Env{Git: d.Git, Base: "main"}
	if b, ok, err := (git.Repo{Dir: root}).Base(ctx, ""); err == nil && ok {
		e.Base = b
	}
	// A round started by hand (`herdr worktree create`) has no recorded host and
	// resolves from the environment like every other verb.
	hostKind := worker.ResolvePaneHost(root, nil, nil)
	if hostKind == host.Solo {
		e.HostName = host.Solo // no panes to snapshot, and not an unavailable host
	} else {
		h := host.New(hostKind, host.Deps{})
		if err := h.Require(); err != nil {
			e.HostErr = err.Error()
		} else if s, ok := h.(host.Snapshotter); ok {
			e.Snapshot, e.HostName = s.Snapshot, h.Name()
		}
	}
	e.NeedsHuman = config.Label(cfg, "needsHuman")
	e.Label = config.Label(cfg, "inProgress")
	if set, err := roundcfg.Load(root); err == nil {
		e.StallMinutes = set.StallMinutes
	}
	f, err := d.forge(ctx, cfg, "", root)
	if err != nil {
		e.ForgeErr = err.Error()
	} else {
		e.Forge = f
	}
	return e
}

// withBoard gives the env the backlog's claim side, which claim-mismatch drift
// reads. File mode has no claims, and a backlog that cannot be opened is a
// warning: the other drift kinds still report.
func withBoard(c *Ctx, root string, env round.Env) round.Env {
	if env.Board != nil {
		return env
	}
	if name, _, err := backendMode(root); err != nil || name != "issues" {
		return env
	}
	be, err := openBacklog(c, root, false, "")
	if err != nil {
		c.Warn("claim check skipped: %v", err)
		return env
	}
	if b, ok := be.(round.Board); ok {
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
		rep, err := withBoard(c, root, c.deps().RoundEnv(ctx, root)).Status(ctx, root)
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
			d.Set("host", rep.Host)
		}
		esc, escLines := escalationRows(rep.Escalations)
		d.Set("escalations", esc)
		lim, limLines := limitStatusRows(rep.Limits)
		d.Set("limits", lim)
		d.Set("review", queuedRows(rep.Queued))
		var lines []string
		if set, err := roundcfg.Load(root); err == nil {
			if a, err := architectureFor(c, root, set); err == nil {
				d.Set("architecture", architectureData(a))
				if l := a.Line(); l != "" {
					lines = append(lines, "architecture\t"+l)
				}
			}
		}
		for _, r := range rep.Rows {
			cols := []string{r.Name, dash(r.Issue), dash(r.Branch), dash(r.PR), dash(r.HostState), dash(strings.Join(r.Drift, ","))}
			if r.Bounces > 0 {
				cols = append(cols, fmt.Sprintf("bounces %d", r.Bounces))
			}
			lines = append(lines, strings.Join(cols, "\t"))
		}
		for _, q := range rep.Queued {
			lines = append(lines, fmt.Sprintf("review\t#%s\t%s\t%s\tfrom %s", q.Issue, dash(q.PR), dash(q.Branch), dash(q.From)))
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

func queuedRows(qs []round.QueuedPR) []any {
	out := make([]any, 0, len(qs))
	for _, q := range qs {
		o := jsonx.NewObject()
		o.Set("issue", q.Issue)
		setIf(o, "pr", q.PR)
		setIf(o, "branch", q.Branch)
		setIf(o, "from", q.From)
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
			lines = append(lines, fmt.Sprintf("drift\t%s\t%s\t%s", f.Kind, dash(firstOf(f.Slot, "#"+f.Issue)), f.Detail))
		}
		for _, f := range out.Repaired {
			lines = append(lines, fmt.Sprintf("repaired\t%s\t%s\t%s", f.Kind, dash(firstOf(f.Slot, "#"+f.Issue)), f.Repair))
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
		o.Set("branch", r.Branch)
		setIf(o, "pr", r.PR)
		setIf(o, "prState", r.PRState)
		setIf(o, "hostState", r.HostState)
		setIf(o, "tab", r.Tab)
		o.Set("registered", r.Registered)
		o.Set("drift", strs(r.Drift))
		o.Set("escalations", strs(r.Escalations))
		setIf(o, "kind", r.Kind)
		setIf(o, "tier", r.Tier)
		setIf(o, "model", r.Model)
		setIf(o, "tierReason", r.TierReason)
		o.Set("bounces", r.Bounces)
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

func firstOf(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
