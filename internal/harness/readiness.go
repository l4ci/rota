package harness

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/l4ci/rota/internal/shlex"
)

// CodexInstallHint is the doctor hint for a missing or unrunnable codex.
const CodexInstallHint = "install codex-cli and put it on PATH"

// CodexVersion is a parsed `codex --version` result, shown in reports. rota
// never gates on it: the launch flags are probed instead (CheckCodexFlags).
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
	// VersionUnreadable: `codex --version` could not run, so codex is not
	// installed or not runnable.
	VersionUnreadable Code = "version-unreadable"
	// FlagMissing: `codex --help` does not list a flag the launch line uses
	// (Finding.Flag).
	FlagMissing Code = "flag-missing"
	// IntegrationStale: herdr's codex integration is not current in the home.
	IntegrationStale Code = "integration-stale"
	// IntegrationUnrunnable: `herdr integration status` could not run (Err).
	IntegrationUnrunnable Code = "integration-unrunnable"
	// NotLoggedIn: `codex login status` failed in the home.
	NotLoggedIn Code = "not-logged-in"
)

// Finding is one thing a readiness check found wrong.
type Finding struct {
	Code Code
	Slot string
	Dir  string // the CODEX_HOME probed
	Flag string // FlagMissing: the launch flag codex does not list
	Err  error  // IntegrationUnrunnable
}

// CheckCodexVersion runs `codex --version` under home (the empty string means
// no CODEX_HOME). It passes whenever codex runs; the returned version is for
// display and is the zero value when the output holds none.
func CheckCodexVersion(ctx context.Context, p Probe, bin, home string) (CodexVersion, *Finding) {
	r, err := p.Run(ctx, bin, []string{"--version"}, codexEnv(home))
	if err != nil || r.ExitCode != 0 {
		return CodexVersion{}, &Finding{Code: VersionUnreadable}
	}
	v, _ := ParseCodexVersion(r.Stdout + "\n" + r.Stderr)
	return v, nil
}

func codexEnv(home string) []string {
	if home == "" {
		return nil
	}
	return []string{"CODEX_HOME=" + home}
}

// LaunchFlags are the long flags of a launch line, `--flag=value` cut to
// `--flag`, in order and without repeats. A line that cannot be split has none.
func LaunchFlags(launch string) []string {
	toks, err := shlex.Split(launch)
	if err != nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, t := range toks {
		if !strings.HasPrefix(t, "--") || len(t) == 2 {
			continue
		}
		f, _, _ := strings.Cut(t, "=")
		if !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	return out
}

// CheckCodexFlags runs `codex --help` and returns a FlagMissing finding for
// each of flags it does not list. Codex's version is never consulted: a
// release that keeps the flags rota launches with needs no rota bump. If help
// cannot run, every flag counts as missing.
func CheckCodexFlags(ctx context.Context, p Probe, bin, home string, flags []string) []Finding {
	if len(flags) == 0 {
		return nil
	}
	r, err := p.Run(ctx, bin, []string{"--help"}, codexEnv(home))
	if err != nil || r.ExitCode != 0 {
		r = Result{}
	}
	help := r.Stdout + "\n" + r.Stderr
	var out []Finding
	for _, f := range flags {
		if !helpHasFlag(help, f) {
			out = append(out, Finding{Code: FlagMissing, Flag: f})
		}
	}
	return out
}

// helpHasFlag reports whether help lists flag as a whole word, so `--no-daemon`
// is not satisfied by `--no-daemon-foo`.
func helpHasFlag(help, flag string) bool {
	for i := 0; ; {
		j := strings.Index(help[i:], flag)
		if j < 0 {
			return false
		}
		at, end := i+j, i+j+len(flag)
		before := at == 0 || !isFlagChar(help[at-1])
		after := end == len(help) || !isFlagChar(help[end])
		if before && after {
			return true
		}
		i = end
	}
}

func isFlagChar(c byte) bool {
	return c == '-' || c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
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
