package harness

import "context"

// LoginProber is the optional facet of a kind whose accounts log in outside
// round.accounts' config dirs (Codex): it says why the home has no usable
// login, "" when it has one. Claude's logins are read by worker.Credentials.
type LoginProber interface {
	LoginProblem(ctx context.Context, p Probe, home string) string
}

// LookupLoginProber selects the optional facet; ok is false for a kind
// without one.
func LookupLoginProber(kind string) (LoginProber, bool) {
	h, ok := lookup(kind)
	if !ok {
		return nil, false
	}
	l, ok := h.(LoginProber)
	return l, ok
}

// LoginProblem runs `codex login status` in the home ("" is the default home).
func (codex) LoginProblem(ctx context.Context, p Probe, home string) string {
	bin, ok := p.Look(Codex)
	if !ok {
		return "codex is not installed or not runnable"
	}
	var env []string
	if home != "" {
		env = []string{"CODEX_HOME=" + home}
	}
	if r, err := p.Run(ctx, bin, []string{"login", "status"}, env); err != nil || r.ExitCode != 0 {
		return "not logged in: run `" + homeEnv(home) + "codex login`"
	}
	return ""
}
