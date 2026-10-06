package doctor

import (
	"context"
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/harness"
)

// CodexHome is one slot's CODEX_HOME.
type CodexHome struct{ Slot, Dir string }

// codex is the E1 check: the Codex CLI runs and every slot home that
// exists is logged in (and, under herdr, has the integration). The readiness
// logic is the harness package's, shared with dispatch; this renders its
// findings as a report line.
func (d *runner) codex() Check {
	look := func(name string) (string, bool) { return d.in.Look(name) }
	bin, have := look("codex")
	homes := d.in.CodexHomes
	if !have && len(homes) == 0 {
		return skip("codex", "codex not on PATH and no slot has a codex home")
	}
	if !have {
		return fail("codex", "codex not found on PATH, but slot homes exist: "+d.homeNames(), harness.CodexInstallHint)
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
	default:
		return fail("codex", "codex not runnable: `codex --version` failed", harness.CodexInstallHint)
	}
	head := "codex"
	if v != (harness.CodexVersion{}) {
		head += " " + v.String()
	}
	// The tier map is optional for codex (#68): unset, a worker runs on
	// Codex's own default model.
	tiers := ""
	if !d.in.CodexTiers {
		tiers = "; round.tiers.codex unset (optional): workers use Codex's default model"
	}
	if len(homes) == 0 {
		return pass("codex", head+", no slot homes yet"+tiers)
	}
	herdr := ""
	if d.in.Dispatch == "herdr" {
		herdr, _ = look("herdr")
	}
	var bad []string
	var hint string
	note := func(msg, h string) {
		bad = append(bad, msg)
		if hint == "" {
			hint = h
		}
	}
	for _, h := range homes {
		found := harness.CheckCodexHome(d.ctx, p, bin, herdr, harness.Home(h))
		// A home's login problem reads before its integration problem.
		for _, want := range []harness.Code{harness.NotLoggedIn, harness.IntegrationStale, harness.IntegrationUnrunnable} {
			for _, f := range found {
				if f.Code != want {
					continue
				}
				if want == harness.NotLoggedIn {
					note(h.Slot+": not logged in", "CODEX_HOME="+h.Dir+" codex login")
				} else {
					note(h.Slot+": herdr integration not current", "CODEX_HOME="+h.Dir+" herdr integration install codex")
				}
			}
		}
	}
	if len(bad) > 0 {
		return fail("codex", head+"; "+strings.Join(bad, "; "), hint)
	}
	return pass("codex", fmt.Sprintf("%s; homes checked: %s%s", head, d.homeNames(), tiers))
}

func (d *runner) homeNames() string {
	var names []string
	for _, h := range d.in.CodexHomes {
		names = append(names, h.Slot)
	}
	return strings.Join(names, ", ")
}
