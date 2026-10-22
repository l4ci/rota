package cli

import (
	"strings"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/rotatree"
	"github.com/l4ci/rota/internal/roundcfg"
)

// supportFenced are the verbs a support machine (round.role support) must not
// run: the ones that merge and the ones that write tracked .rota/ content.
// Only the main orchestrator does either.
var supportFenced = []string{
	"rota worker gate",
}

// supportBackendHint is the hint for a support key under issues.source file.
const supportBackendHint = "set issues.source to a tracker (github, gitlab, …) first; a support round needs labels, comments and PRs"

// supportFileModeStop refuses a support key set or in use under issues.source
// file: a support round needs labels, comments and PRs (D3).
func supportFileModeStop() (Result, error) {
	d := jsonObj("blockedBy", "backend", "changed", false)
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
	if _, ok := config.Lookup(project, "round.role"); !ok {
		return Result{}, nil
	}
	d := jsonObj("blockedBy", "config", "key", "round.role", "changed", false)
	return Result{Data: d}, Refused("round.role is set in .rota/config.json, which every machine shares").
		WithHint("remove it from .rota/config.json, then run: rota config set round.role <value>, which writes .rota/config.local.json")
}

// supportRoleStop refuses a fenced verb when this machine's round.role is
// support. A missing root or an unreadable config is a no-op: the verb's own
// checks report it.
func supportRoleStop(c *Ctx) (Result, error) {
	if !supportFencedPath(c.Path) {
		return Result{}, nil
	}
	root, err := c.Root()
	if err != nil {
		return Result{}, nil
	}
	set, err := roundcfg.Load(root)
	if err != nil || set.Role != roundcfg.RoleSupport {
		return Result{}, nil
	}
	d := jsonObj("blockedBy", "role", "role", roundcfg.RoleSupport, "changed", false)
	return Result{Data: d}, Refused("%s is refused on a support machine (round.role support): only the main orchestrator merges and writes tracked .rota/ content", c.Path).
		WithHint("run this on the main machine, or unset round.role in .rota/config.local.json")
}

func supportFencedPath(path string) bool {
	for _, p := range supportFenced {
		if path == p {
			return true
		}
	}
	return false
}

// supportRoundStartStop refuses `round start` for a support configuration that
// cannot run: file mode (D3), or role support without a support name.
func supportRoundStartStop(root string, set roundcfg.Settings) (Result, error) {
	if set.Support == "" && set.Role != roundcfg.RoleSupport {
		return Result{}, nil
	}
	issue, err := issueBackend(root)
	if err != nil {
		return Result{}, err
	}
	if !issue {
		return supportFileModeStop()
	}
	if set.Role == roundcfg.RoleSupport && set.Support == "" {
		d := jsonObj("blockedBy", "config", "changed", false)
		return Result{Data: d}, Refused("round.role is support but round.support is empty").WithHint("set round.support to this machine's name")
	}
	return Result{}, nil
}

// supportKeyNonDefault is whether setting key to raw turns the support feature
// on: round.support non-empty, or round.role anything but main.
func supportKeyNonDefault(key, raw string) bool {
	s, _ := config.Coerce(raw).(string)
	switch key {
	case "round.support":
		return strings.TrimSpace(s) != ""
	case "round.role":
		return s != roundcfg.RoleMain
	}
	return false
}
