package cli

import (
	"errors"
	"strings"

	"github.com/l4ci/rota/internal/blockedby"
	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/worker"
)

// supportBackendHint is the hint for a support key under issues.source file.
const supportBackendHint = "set issues.source to a tracker (github, gitlab, …) first; a support round needs labels, comments and PRs"

// supportFileModeStop refuses a support key set or in use under issues.source
// file: a support round needs labels, comments and PRs (D3).
func supportFileModeStop() (Result, error) {
	d := jsonx.Obj("blockedBy", blockedby.Backend, "changed", false)
	return Result{Data: d}, Refused("a support round needs an issue tracker, but issues.source is file").WithHint(supportBackendHint)
}

// roleInTrackedConfig refuses every verb but `rota config` and `rota
// migrate` when the tracked project layer names round.role: the role belongs
// to one machine, so it lives in .rota/config.local.json only. Outside a
// project there is nothing to check.
func (p *preflight) roleInTrackedConfig() (Result, error) {
	if p.g.TrackedRoleExempt {
		return Result{}, nil
	}
	root, ok := p.project()
	if !ok {
		return Result{}, nil
	}
	if !roundcfg.RoleInTrackedConfig(root) {
		return Result{}, nil
	}
	d := jsonx.Obj("blockedBy", blockedby.Config, "key", config.RoundRoleKey, "changed", false)
	return Result{Data: d}, Refused("round.role is set in .rota/config.json, which every machine shares").
		WithHint("remove it from .rota/config.json, then run: rota config set round.role <value>, which writes .rota/config.local.json")
}

// supportRole refuses a fenced verb when this machine's round.role is
// support. It reads round.role alone, so a bad unrelated round.* key cannot
// open the fence, and a role it cannot read refuses rather than runs.
func (p *preflight) supportRole() (Result, error) {
	if roundcfg.MayRun(roundcfg.RoleSupport, p.g.Fence) {
		return Result{}, nil
	}
	root, ok := p.project()
	if !ok {
		return Result{}, nil
	}
	return supportRefusalWith(p.c, root, p.c.Path, p.config)
}

// supportRefusal is the exit-4 refusal of what on a support machine, or of any
// fenced verb when round.role cannot be read; nil on main.
func supportRefusal(c *Ctx, root, what string) (Result, error) {
	return supportRefusalWith(c, root, what, c.deps().Config)
}

// supportRefusalWith is supportRefusal reading the config through load.
func supportRefusalWith(c *Ctx, root, what string, load func(string) any) (Result, error) {
	support, res, err := supportMachineWith(root, what, load)
	if err != nil || !support {
		return res, err
	}
	d := jsonx.Obj("blockedBy", blockedby.Role, "role", roundcfg.RoleSupport, "changed", false)
	return Result{Data: d}, Refused("%s is refused on a support machine (round.role support): only the main orchestrator merges and writes tracked .rota/ content", what).
		WithHint("run this on the main machine, or unset round.role in .rota/config.local.json")
}

// supportMachine is whether this machine's round.role is support. A role it
// cannot read is the exit-4 refusal, so a verb never runs on a guess.
func supportMachine(c *Ctx, root, what string) (bool, Result, error) {
	return supportMachineWith(root, what, c.deps().Config)
}

// supportMachineWith is supportMachine reading the config through load.
func supportMachineWith(root, what string, load func(string) any) (bool, Result, error) {
	support, rerr := roundcfg.SupportMachine(root, load)
	if rerr != nil {
		d := jsonx.Obj("blockedBy", blockedby.Config, "key", config.RoundRoleKey, "changed", false)
		return false, Result{Data: d}, Refused("%s needs to know this machine's role: %s", what, rerr.Why).WithHint(rerr.Hint)
	}
	return support, Result{}, nil
}

// supportRoundStartStop refuses `round start` for a support configuration that
// cannot run: file mode (D3), role support without a support name, or a forge
// that cannot say who has write access (bounces on the issue trust only
// writers' markers).
func supportRoundStartStop(c *Ctx, root string, set roundcfg.Settings) (Result, error) {
	if set.Support == "" && set.Role != roundcfg.RoleSupport {
		return Result{}, nil
	}
	issue, err := issueBackend(c.deps(), root)
	if err != nil {
		return Result{}, err
	}
	if !issue {
		return supportFileModeStop()
	}
	if set.Role == roundcfg.RoleSupport && set.Support == "" {
		d := jsonx.Obj("blockedBy", blockedby.Config, "changed", false)
		return Result{Data: d}, Refused("round.role is support but round.support is empty").WithHint("set round.support to this machine's name")
	}
	if set.Support != "" {
		if _, err := c.deps().workerEnv().BounceStore(root); errors.Is(err, worker.ErrNoPushTrust) {
			d := jsonx.Obj("blockedBy", blockedby.Forge, "changed", false)
			return Result{Data: d}, Refused("%s", err.Error()).WithHint("a support round needs GitHub: only GitHub can say who has write access, which the bounce markers rely on")
		} else if err != nil {
			return Result{}, err
		}
	}
	return Result{}, nil
}

// supportKeyNonDefault is whether setting key to raw turns the support feature
// on: round.support non-empty, or round.role anything but main.
func supportKeyNonDefault(key, raw string) bool {
	s, _ := config.Coerce(raw).(string)
	switch key {
	case config.RoundSupportKey:
		return strings.TrimSpace(s) != ""
	case config.RoundRoleKey:
		return s != roundcfg.RoleMain
	}
	return false
}
