package harness

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// The supported Codex CLI range (E1, #68): 0.159.0 inclusive up to 0.160.0
// exclusive. `rota worker dispatch`, `rota round assign` and the doctor
// `codex` check all read it from here.
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

// ParseIntegration reads the line for agent from `herdr integration status`
// (`claude: current (v10) (/path)`, `codex: not installed (/path)`) and
// returns "current", "not installed", the raw remainder for any other state
// (an outdated install), or "no status" when there is no such line. The
// format is herdr's own, so this stays tolerant: case, spacing and trailing
// version or path detail are ignored.
func ParseIntegration(out, agent string) string {
	for _, line := range strings.Split(out, "\n") {
		name, rest, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok || !strings.EqualFold(strings.TrimSpace(name), agent) {
			continue
		}
		rest = strings.ToLower(strings.TrimSpace(rest))
		switch {
		case strings.HasPrefix(rest, "not installed"), strings.HasPrefix(rest, "missing"):
			return "not installed"
		case strings.HasPrefix(rest, "current"), strings.HasPrefix(rest, "up to date"), strings.HasPrefix(rest, "installed"):
			return "current"
		case rest == "":
			return "no status"
		}
		if i := strings.Index(rest, " ("); i > 0 {
			rest = rest[:i]
		}
		return rest
	}
	return "no status"
}

// Home is one CODEX_HOME to probe: Slot labels it (the account name) and an
// empty Dir is the default Codex home.
type Home struct{ Slot, Dir string }

// Code names what a readiness check found wrong. Doctor renders the findings
// as a report line, dispatch turns them into a refusal; the check is one.
type Code string

const (
	// VersionUnreadable: `codex --version` failed or printed no version.
	VersionUnreadable Code = "version-unreadable"
	// VersionOutOfRange: the version is outside CodexRange (Finding.Version).
	VersionOutOfRange Code = "version-out-of-range"
	// IntegrationStale: herdr's codex integration is not current in the home.
	IntegrationStale Code = "integration-stale"
	// IntegrationUnrunnable: `herdr integration status` could not run (Err).
	IntegrationUnrunnable Code = "integration-unrunnable"
	// NotLoggedIn: `codex login status` failed in the home.
	NotLoggedIn Code = "not-logged-in"
)

// Finding is one thing a readiness check found wrong.
type Finding struct {
	Code    Code
	Slot    string
	Dir     string // the CODEX_HOME probed
	Version string // VersionOutOfRange: the version found
	Err     error  // IntegrationUnrunnable
}

// CheckCodexVersion runs `codex --version` under home (the empty string means
// no CODEX_HOME) and returns the version, or why it cannot be accepted.
func CheckCodexVersion(ctx context.Context, p Probe, bin, home string) (CodexVersion, *Finding) {
	var env []string
	if home != "" {
		env = []string{"CODEX_HOME=" + home}
	}
	r, err := p.Run(ctx, bin, []string{"--version"}, env)
	v, ok := ParseCodexVersion(r.Stdout + "\n" + r.Stderr)
	switch {
	case err != nil || r.ExitCode != 0 || !ok:
		return v, &Finding{Code: VersionUnreadable}
	case !v.InRange():
		return v, &Finding{Code: VersionOutOfRange, Version: v.String()}
	}
	return v, nil
}

// CheckCodexHome probes one home: with a herdr binary, whether its codex
// integration is current there, then whether the home is logged in. Findings
// come in that order; a caller that cannot go on past one stops at it.
func CheckCodexHome(ctx context.Context, p Probe, bin, herdr string, h Home) []Finding {
	var henv []string
	if h.Dir != "" {
		henv = []string{"CODEX_HOME=" + h.Dir}
	}
	var out []Finding
	if herdr != "" {
		st, err := p.Run(ctx, herdr, []string{"integration", "status"}, henv)
		switch {
		case err != nil:
			out = append(out, Finding{Code: IntegrationUnrunnable, Slot: h.Slot, Dir: h.Dir, Err: err})
		case st.ExitCode != 0 || ParseIntegration(st.Stdout, "codex") != "current":
			out = append(out, Finding{Code: IntegrationStale, Slot: h.Slot, Dir: h.Dir})
		}
	}
	if lg, err := p.Run(ctx, bin, []string{"login", "status"}, henv); err != nil || lg.ExitCode != 0 {
		out = append(out, Finding{Code: NotLoggedIn, Slot: h.Slot, Dir: h.Dir})
	}
	return out
}
