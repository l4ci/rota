package cli

import (
	"errors"
	"io/fs"
	"os"
	"strings"

	"github.com/l4ci/rota/internal/blockedby"
	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/rotatree"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/worker"
)

// supportBackendHint is the hint for a support key under issues.source file.
const supportBackendHint = "set issues.source to a tracker (github, gitlab, …) first; a support round needs labels, comments and PRs"

// supportFileModeStop refuses a support key set or in use under issues.source
// file: a support round needs labels, comments and PRs (D3).
func supportFileModeStop() (Result, error) {
	d := jsonObj("blockedBy", blockedby.Backend, "changed", false)
	return Result{Data: d}, Refused("a support round needs an issue tracker, but issues.source is file").WithHint(supportBackendHint)
}

// roleInTrackedConfigStop refuses every verb but `rota config` and `rota
// migrate` when the tracked project layer names round.role: the role belongs
// to one machine, so it lives in .rota/config.local.json only. Outside a
// project there is nothing to check.
func roleInTrackedConfigStop(c *Ctx) (Result, error) {
	if pathUnder(c.Path, []string{"rota config", "rota migrate"}) {
		return Result{}, nil
	}
	root, err := c.Root()
	if err != nil {
		return Result{}, nil
	}
	project := fsio.LoadJSON(rotatree.Config(root), jsonx.NewObject())
	if _, ok := config.Lookup(project, config.RoundRoleKey); !ok {
		return Result{}, nil
	}
	d := jsonObj("blockedBy", blockedby.Config, "key", config.RoundRoleKey, "changed", false)
	return Result{Data: d}, Refused("round.role is set in .rota/config.json, which every machine shares").
		WithHint("remove it from .rota/config.json, then run: rota config set round.role <value>, which writes .rota/config.local.json")
}

// supportRoleStop refuses a fenced verb when this machine's round.role is
// support. It reads round.role alone, so a bad unrelated round.* key cannot
// open the fence, and a role it cannot read refuses rather than runs.
func supportRoleStop(c *Ctx) (Result, error) {
	if roundcfg.MayRun(roundcfg.RoleSupport, c.Path) {
		return Result{}, nil
	}
	root, err := c.Root()
	if err != nil {
		return Result{}, nil
	}
	return supportRefusal(c, root, c.Path)
}

// supportRefusal is the exit-4 refusal of what on a support machine, or of any
// fenced verb when round.role cannot be read; nil on main.
func supportRefusal(c *Ctx, root, what string) (Result, error) {
	support, res, err := supportMachine(c, root, what)
	if err != nil || !support {
		return res, err
	}
	d := jsonObj("blockedBy", blockedby.Role, "role", roundcfg.RoleSupport, "changed", false)
	return Result{Data: d}, Refused("%s is refused on a support machine (round.role support): only the main orchestrator merges and writes tracked .rota/ content", what).
		WithHint("run this on the main machine, or unset round.role in .rota/config.local.json")
}

// supportMachine is whether this machine's round.role is support. A role it
// cannot read is the exit-4 refusal, so a verb never runs on a guess.
func supportMachine(c *Ctx, root, what string) (bool, Result, error) {
	unknown := func(why, hint string) (bool, Result, error) {
		d := jsonObj("blockedBy", blockedby.Config, "key", config.RoundRoleKey, "changed", false)
		return false, Result{Data: d}, Refused("%s needs to know this machine's role: %s", what, why).WithHint(hint)
	}
	// Reads the layer from disk on purpose, not through Deps.Config.
	// config.Load skips a local layer it cannot read or decode, which would
	// read as role main and open the fence.
	if raw, err := os.ReadFile(rotatree.ConfigLocal(root)); err == nil {
		if _, err := jsonx.Decode(raw); err != nil {
			return unknown(".rota/config.local.json does not decode: "+err.Error(), "fix the JSON in .rota/config.local.json, or remove the file on the main machine")
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return unknown(".rota/config.local.json cannot be read: "+err.Error(), "make .rota/config.local.json readable")
	}
	role, err := roundcfg.MachineRole(c.deps().Config(root))
	if err != nil {
		return unknown(err.Error(), "set round.role to main or support: rota config set round.role main")
	}
	return role == roundcfg.RoleSupport, Result{}, nil
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
		d := jsonObj("blockedBy", blockedby.Config, "changed", false)
		return Result{Data: d}, Refused("round.role is support but round.support is empty").WithHint("set round.support to this machine's name")
	}
	if set.Support != "" {
		if _, err := c.deps().workerEnv().BounceStore(root); errors.Is(err, worker.ErrNoPushTrust) {
			d := jsonObj("blockedBy", blockedby.Forge, "changed", false)
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
