package doctor

import (
	"context"
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/host"
)

// CodexHome is one CODEX_HOME a worker can run under: a work.codexAccounts
// account (Slot is its name), or the default Codex home (empty Slot and Dir).
type CodexHome struct{ Slot, Dir string }

func (h CodexHome) label() string {
	if h.Slot == "" {
		return "default home"
	}
	return h.Slot
}

func (h CodexHome) env(plan harness.Doctor) string {
	if h.Dir == "" {
		return ""
	}
	return plan.HomeEnv + h.Dir + " "
}

// harnessChecks is one check per worker harness that has probes of its own
// (Harness.Doctor): the binary is in range and every home a worker can run
// under is logged in (and, under herdr, has the integration). The readiness
// logic is the harness package's, shared with dispatch; this renders its
// findings as a report line. A configured account that is not ready fails the
// check; the default home only notes it, since a project that never runs the
// kind's workers has no reason to log in.
func (d *runner) harnessChecks() []Check {
	var out []Check
	for _, k := range harness.Kinds() {
		h, _ := harness.Lookup(k)
		if plan := h.Doctor(); plan.Check != "" {
			out = append(out, d.harnessCheck(plan))
		}
	}
	return out
}

func (d *runner) harnessCheck(plan harness.Doctor) Check {
	name := plan.Check
	look := func(name string) (string, bool) { return d.in.Look(name) }
	bin, have := look(plan.Binary)
	homes := d.in.CodexHomes
	if !have {
		if !d.hasCodexAccounts() {
			return skip(name, plan.Binary+" not on PATH and no work.codexAccounts configured")
		}
		return fail(name, plan.Binary+" not found on PATH, but work.codexAccounts is configured: "+d.homeNames(), plan.InstallHint)
	}
	p := harness.Probe{Look: look, Run: func(ctx context.Context, bin string, args, env []string) (harness.Result, error) {
		r, err := d.in.Exec(ctx, bin, args, env, d.in.Dir)
		return harness.Result(r), err
	}}
	home := ""
	if len(homes) > 0 {
		home = homes[0].Dir
	}
	v, ok := plan.Runnable(d.ctx, p, bin, home)
	if !ok {
		return fail(name, plan.Binary+" not runnable: `"+plan.Binary+" --version` failed", plan.InstallHint)
	}
	head := name
	if v != "" {
		head += " " + v
	}
	// The tier map is optional for codex (#68): unset, a worker runs on
	// Codex's own default model.
	tiers := ""
	if !d.in.CodexTiers {
		tiers = "; round.tiers." + name + " unset (optional): workers use " + plan.Title + "'s default model"
	}
	if len(homes) == 0 {
		return pass(name, head+tiers)
	}
	herdr := ""
	if host.KindOf(d.in.Dispatch) == host.Herdr {
		herdr, _ = look(host.Herdr.Binary())
	}
	var bad, notes []string
	var hint string
	for _, h := range homes {
		found := plan.Home(d.ctx, p, bin, herdr, harness.Home(h))
		// A home's login problem reads before its integration problem.
		for _, want := range []harness.Code{harness.NotLoggedIn, harness.IntegrationStale, harness.IntegrationUnrunnable} {
			for _, f := range found {
				if f.Code != want {
					continue
				}
				msg, fix := h.label()+": not logged in", h.env(plan)+plan.LoginFix
				if want != harness.NotLoggedIn {
					msg, fix = h.label()+": herdr integration not current", h.env(plan)+plan.IntegrationFix
				}
				if h.Slot == "" {
					notes = append(notes, msg+" (run `"+fix+"` before a "+plan.Title+" worker)")
					continue
				}
				bad = append(bad, msg)
				if hint == "" {
					hint = fix
				}
			}
		}
	}
	if len(bad) > 0 {
		return fail(name, head+"; "+strings.Join(bad, "; "), hint)
	}
	if len(notes) > 0 {
		return pass(name, head+"; "+strings.Join(notes, "; ")+tiers)
	}
	return pass(name, fmt.Sprintf("%s; homes checked: %s%s", head, d.homeNames(), tiers))
}

func (d *runner) hasCodexAccounts() bool {
	for _, h := range d.in.CodexHomes {
		if h.Slot != "" {
			return true
		}
	}
	return false
}

func (d *runner) homeNames() string {
	var names []string
	for _, h := range d.in.CodexHomes {
		names = append(names, h.label())
	}
	return strings.Join(names, ", ")
}
