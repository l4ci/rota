package doctor

import (
	"context"

	"github.com/l4ci/rota/internal/host"
)

// run runs a looked-up tool; ok is false when it could not run at all.
func (d *runner) run(bin string, args []string, env []string) (Result, bool) {
	r, err := d.in.Exec(d.ctx, bin, args, env, d.in.Dir)
	return r, err == nil
}

func (d *runner) git() Check {
	bin, ok := d.in.Look("git")
	if !ok {
		return fail("git", "git not found on PATH", "install git")
	}
	r, ran := d.run(bin, []string{"check-ignore", "-q", ".worktrees/x"}, nil)
	switch {
	case !ran:
		return fail("git", "git could not run", "install git")
	case r.ExitCode == 0:
		return pass("git", ".worktrees/ is gitignored")
	case r.ExitCode == 1:
		return fail("git", ".worktrees/ is not gitignored", "add .worktrees/ to .gitignore (rota init does this)")
	default:
		return fail("git", "not inside a git repository", "run: git init")
	}
}

// jq is a skill dependency, not a round one: the skills read fields out of
// `rota … --json` with it, and a missing jq fails them halfway through a write.
func (d *runner) jq() Check {
	if _, ok := d.in.Look("jq"); !ok {
		return fail("jq", "jq not found on PATH", "install jq (skills read rota --json output with it)")
	}
	return pass("jq", "jq on PATH")
}

// roundHost is the host a round runs on, resolved by worker.ResolveHost when
// the input was gathered, so doctor checks what the round verbs will drive.
func (d *runner) roundHost() host.Kind { return d.in.RoundHost }

func (d *runner) host() Check {
	if diagnose := host.CapabilitiesForKind(d.roundHost()).Diagnose; diagnose != nil {
		h := diagnose(host.Probe{
			Getenv: d.in.Getenv,
			Dial:   d.in.Dial,
			Look:   d.in.Look,
			Exec: func(_ context.Context, bin string, args []string) (host.Result, error) {
				return d.in.Exec(d.ctx, bin, args, nil, d.in.Dir)
			},
		})
		switch h.Status {
		case host.HealthOK:
			return pass("host", h.Detail)
		case host.HealthWarn:
			return Check{Name: "host", Status: Warn, Detail: h.Detail, Hint: h.Hint}
		}
		return fail("host", h.Detail, h.Hint)
	}
	if d.in.RegistrySolo {
		return skip("host", "the recorded round is solo: workers are subagents, no terminal host needed")
	}
	name := d.in.Dispatch
	if name == "" {
		name = "subagent"
	}
	return skip("host", "round.dispatch is "+name+", not in herdr or tmux: solo, no terminal host needed")
}
