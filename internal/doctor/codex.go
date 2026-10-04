package doctor

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// The supported Codex CLI range (E1, #68): 0.159.0 inclusive up to 0.160.0
// exclusive. It lives here, beside ParseIntegration, because `rota worker
// dispatch`, `rota round assign` and the `codex` check all read it and this
// package imports nothing from the worker layer.
const (
	CodexMin   = "0.159.0"
	CodexMax   = "0.160.0"
	CodexRange = ">=" + CodexMin + " <" + CodexMax
	// CodexInstallHint is the doctor hint for a missing or unsupported codex.
	CodexInstallHint = "install codex-cli 0.159.x"
)

// CodexVersion is a parsed `codex --version` result.
type CodexVersion struct{ Major, Minor, Patch int }

func (v CodexVersion) String() string { return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch) }

var codexVersionLine = regexp.MustCompile(`^codex-cli (\d+)\.(\d+)\.(\d+)$`)

// ParseCodexVersion reads `codex --version` output: a `codex-cli X.Y.Z` line,
// possibly among WARNING lines (the caller joins stdout and stderr). Anything
// else, a prerelease suffix included, is not a version.
func ParseCodexVersion(out string) (CodexVersion, bool) {
	for _, line := range strings.Split(out, "\n") {
		m := codexVersionLine.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		var v CodexVersion
		v.Major, _ = strconv.Atoi(m[1])
		v.Minor, _ = strconv.Atoi(m[2])
		v.Patch, _ = strconv.Atoi(m[3])
		return v, true
	}
	return CodexVersion{}, false
}

// InRange reports whether v is inside CodexMin..CodexMax.
func (v CodexVersion) InRange() bool {
	min, _ := ParseCodexVersion("codex-cli " + CodexMin)
	max, _ := ParseCodexVersion("codex-cli " + CodexMax)
	return !v.less(min) && v.less(max)
}

func (v CodexVersion) less(o CodexVersion) bool {
	if v.Major != o.Major {
		return v.Major < o.Major
	}
	if v.Minor != o.Minor {
		return v.Minor < o.Minor
	}
	return v.Patch < o.Patch
}

// CodexHome is one slot's CODEX_HOME.
type CodexHome struct{ Slot, Dir string }

// codex is the E1 check: the Codex CLI is in range and every slot home that
// exists is logged in (and, under herdr, has the integration).
func (d *runner) codex() Check {
	bin, have := d.in.Look("codex")
	homes := d.in.CodexHomes
	if !have && len(homes) == 0 {
		return skip("codex", "codex not on PATH and no slot has a codex home")
	}
	if !have {
		return fail("codex", "codex not found on PATH, but slot homes exist: "+d.homeNames(), CodexInstallHint)
	}
	var env []string
	if len(homes) > 0 {
		env = []string{"CODEX_HOME=" + homes[0].Dir}
	}
	r, ran := d.run(bin, []string{"--version"}, env)
	v, ok := ParseCodexVersion(r.Stdout + "\n" + r.Stderr)
	switch {
	case !ran || r.ExitCode != 0 || !ok:
		return fail("codex", "codex version unreadable", CodexInstallHint)
	case !v.InRange():
		return fail("codex", fmt.Sprintf("codex %s, need %s", v, CodexRange), CodexInstallHint)
	}
	head := "codex " + v.String()
	// The tier map is optional for codex (#68): unset, a worker runs on
	// Codex's own default model.
	tiers := ""
	if !d.in.CodexTiers {
		tiers = "; round.tiers.codex unset (optional): workers use Codex's default model"
	}
	if len(homes) == 0 {
		return pass("codex", head+", no slot homes yet"+tiers)
	}
	herdr, haveHerdr := d.in.Look("herdr")
	var bad []string
	var hint string
	note := func(msg, h string) {
		bad = append(bad, msg)
		if hint == "" {
			hint = h
		}
	}
	for _, h := range homes {
		henv := []string{"CODEX_HOME=" + h.Dir}
		if r, ran := d.run(bin, []string{"login", "status"}, henv); !ran || r.ExitCode != 0 {
			note(h.Slot+": not logged in", "CODEX_HOME="+h.Dir+" codex login")
		}
		if d.in.Dispatch != "herdr" || !haveHerdr {
			continue
		}
		r, ran := d.run(herdr, []string{"integration", "status"}, henv)
		if state := ParseIntegration(r.Stdout, "codex"); !ran || r.ExitCode != 0 || state != "current" {
			note(h.Slot+": herdr integration not current", "CODEX_HOME="+h.Dir+" herdr integration install codex")
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
