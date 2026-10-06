package doctor

import (
	"context"
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/harness"
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

func (h CodexHome) env() string {
	if h.Dir == "" {
		return ""
	}
	return "CODEX_HOME=" + h.Dir + " "
}

// codex is the E1 check: the Codex CLI is in range and every home a worker
// can run under is logged in (and, under herdr, has the integration). The
// readiness logic is the harness package's, shared with dispatch; this renders
// its findings as a report line. A configured account that is not ready fails
// the check; the default home only notes it, since a project that never runs
// Codex workers has no reason to log in.
func (d *runner) codex() Check {
	look := func(name string) (string, bool) { return d.in.Look(name) }
	bin, have := look("codex")
	homes := d.in.CodexHomes
	if !have {
		if !d.hasCodexAccounts() {
			return skip("codex", "codex not on PATH and no work.codexAccounts configured")
		}
		return fail("codex", "codex not found on PATH, but work.codexAccounts is configured: "+d.homeNames(), harness.CodexInstallHint)
	}
	p := harness.Probe{Look: look, Run: func(ctx context.Context, bin string, args, env []string) (harness.Result, error) {
		r, err := d.in.Exec(ctx, bin, args, env, d.in.Dir)
		return harness.Result(r), err
	}}
	home := ""
	if len(homes) > 0 {
		home = homes[0].Dir
	}
	v, f := harness.CheckCodexVersion(d.ctx, p, bin, home)
	switch {
	case f == nil:
	case f.Code == harness.VersionOutOfRange:
		return fail("codex", fmt.Sprintf("codex %s, need %s", v, harness.CodexRange), harness.CodexInstallHint)
	default:
		return fail("codex", "codex version unreadable", harness.CodexInstallHint)
	}
	head := "codex " + v.String()
	// The tier map is optional for codex (#68): unset, a worker runs on
	// Codex's own default model.
	tiers := ""
	if !d.in.CodexTiers {
		tiers = "; round.tiers.codex unset (optional): workers use Codex's default model"
	}
	if len(homes) == 0 {
		return pass("codex", head+tiers)
	}
	herdr := ""
	if d.in.Dispatch == "herdr" {
		herdr, _ = look("herdr")
	}
	var bad, notes []string
	var hint string
	for _, h := range homes {
		found := harness.CheckCodexHome(d.ctx, p, bin, herdr, harness.Home(h))
		// A home's login problem reads before its integration problem.
		for _, want := range []harness.Code{harness.NotLoggedIn, harness.IntegrationStale, harness.IntegrationUnrunnable} {
			for _, f := range found {
				if f.Code != want {
					continue
				}
				msg, fix := h.label()+": not logged in", h.env()+"codex login"
				if want != harness.NotLoggedIn {
					msg, fix = h.label()+": herdr integration not current", h.env()+"herdr integration install codex"
				}
				if h.Slot == "" {
					notes = append(notes, msg+" (run `"+fix+"` before a Codex worker)")
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
		return fail("codex", head+"; "+strings.Join(bad, "; "), hint)
	}
	if len(notes) > 0 {
		return pass("codex", head+"; "+strings.Join(notes, "; ")+tiers)
	}
	return pass("codex", fmt.Sprintf("%s; homes checked: %s%s", head, d.homeNames(), tiers))
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
