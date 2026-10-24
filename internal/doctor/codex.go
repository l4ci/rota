package doctor

import (
	"context"
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/host"
)

// KindInput is what one worker harness has configured.
type KindInput struct {
	// AccountsKey is the config key of the kind's accounts (Harness.Accounts).
	AccountsKey string
	// Homes are the homes a worker can run under: the kind's accounts, else
	// the default home (a zero Home).
	Homes []Home
	// Tiers is whether any round.tiers.<kind>.<tier> is set.
	Tiers bool
	// Models are the "tier=model" pairs a worker starts with when Tiers is
	// false: the shipped defaults, or none once all three keys are emptied.
	Models []string
}

// Home is one account home a worker can run under: a named account (Slot is
// its name), or the default home (empty Slot and Dir).
type Home struct{ Slot, Dir string }

func (h Home) label() string {
	if h.Slot == "" {
		return "default home"
	}
	return h.Slot
}

func (h Home) env(plan harness.Doctor) string {
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
		if plan := harness.Resolve(k).Doctor(); plan.Check != "" {
			out = append(out, d.harnessCheck(k, plan))
		}
	}
	return out
}

func (d *runner) harnessCheck(kind string, plan harness.Doctor) Check {
	in := d.in.Kinds[kind]
	name := plan.Check
	look := func(name string) (string, bool) { return d.in.Look(name) }
	bin, have := look(plan.Binary)
	homes := in.Homes
	if !have {
		if !hasAccounts(homes) {
			return skip(name, plan.Binary+" not on PATH and no "+plan.Binary+" account in "+in.AccountsKey)
		}
		return fail(name, plan.Binary+" not found on PATH, but "+in.AccountsKey+" has "+plan.Binary+" accounts: "+homeNames(homes), plan.InstallHint)
	}
	p := harness.Probe{Look: look, Run: func(ctx context.Context, bin string, args, env []string) (harness.Result, error) {
		r, err := d.in.Exec(ctx, bin, args, env, d.in.Dir)
		return harness.Result{Stdout: r.Stdout, Stderr: r.Stderr, ExitCode: r.ExitCode}, err
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
	// The tier map is optional for codex (#68). Unset, a worker runs on the
	// shipped tier defaults, or on Codex's own model once all three are emptied.
	tiers := ""
	if !in.Tiers {
		tiers = "; round.tiers." + name + " unset (optional): workers use "
		if len(in.Models) > 0 {
			tiers += strings.Join(in.Models, ", ")
		} else {
			tiers += plan.Title + "'s default model"
		}
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
	return pass(name, fmt.Sprintf("%s; homes checked: %s%s", head, homeNames(homes), tiers))
}

func hasAccounts(homes []Home) bool {
	for _, h := range homes {
		if h.Slot != "" {
			return true
		}
	}
	return false
}

func homeNames(homes []Home) string {
	var names []string
	for _, h := range homes {
		names = append(names, h.label())
	}
	return strings.Join(names, ", ")
}
