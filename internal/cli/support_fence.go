package cli

import (
	"errors"
	"io/fs"
	"os"
	"strings"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/rotatree"
	"github.com/l4ci/rota/internal/roundcfg"
)

// The support tables classify every verb of the tree (a test walks it): a
// support machine (round.role support) never merges and never writes tracked
// content that only the main orchestrator commits. A support round runs on an
// issue backend only (D3), so a verb that writes the tracker there is allowed.

// supportFenced are refused before they run on a support machine.
var supportFenced = []string{
	// Merge.
	"rota worker gate", "rota worker train",
	"rota ship merge", "rota ship pr-merge", "rota ship undo",
	// Tracked .rota/ writes.
	"rota knowledge add", "rota knowledge amend", "rota knowledge rename-topic", "rota knowledge replace", "rota knowledge rm",
	"rota knowledge tier fold", "rota knowledge tier set",
	"rota knowledge contradiction add", "rota knowledge contradiction clear",
	"rota glossary write", "rota glossary import",
	"rota map index", "rota qa index",
	"rota milestone add", "rota milestone index", "rota milestone overview", "rota milestone put", "rota milestone status",
	"rota backlog archive", "rota backlog backfill",
	"rota config fill",
	"rota id next", "rota refactor reset",
	"rota spike add", "rota spike finish",
	"rota init", "rota init umbrella", "rota setup", "rota update",
	"rota migrate config", "rota migrate hv", "rota migrate issues", "rota migrate milestone-files", "rota migrate milestones", "rota migrate source",
	// Tracked instruction and agent files, which main regenerates and commits.
	"rota block", "rota instructions init", "rota agents write",
	// Main only: the work split.
	"rota round support offer",
	// Release.
	"rota release bump", "rota release changelog", "rota release close-milestone", "rota release publish", "rota release push",
}

// supportInVerb are fenced inside the verb, which knows what it writes:
// config set and config edit refuse a tracked key on a support machine and
// allow --global and round.role (config.local.json).
var supportInVerb = []string{"rota config set", "rota config edit"}

// supportAllowed run on a support machine. Each writer here writes the
// tracker, gitignored per-machine state or files outside the repo.
var supportAllowed = []string{
	// Reads.
	"rota backlog drift", "rota backlog ids", "rota backlog list", "rota backlog milestones", "rota backlog stale",
	"rota config check", "rota config show",
	"rota connect list", "rota connect test",
	"rota debug counter show", "rota debug counter summary", "rota debug session list", "rota debug session show",
	"rota decisions query", "rota decisions stats", "rota decisions topics",
	"rota design list", "rota design show",
	"rota doctor", "rota gate list",
	"rota git base", "rota git branch", "rota git guard clean", "rota git guard feature-branch", "rota git worktree-path",
	"rota glossary read",
	"rota init check",
	"rota issues imported", "rota issues provider",
	"rota item comment list", "rota item field get", "rota item field list", "rota item note show", "rota item show",
	"rota keepalive status", "rota layout", "rota limit status",
	"rota knowledge contradiction has", "rota knowledge contradiction list", "rota knowledge query", "rota knowledge stats",
	"rota knowledge tier get", "rota knowledge tier list", "rota knowledge topics",
	"rota map query", "rota map stats",
	"rota milestone active", "rota milestone check", "rota milestone list", "rota milestone show",
	"rota plan check", "rota plan list", "rota plan rename-check", "rota plan show", "rota plan uncertain", "rota plan validate-docs",
	"rota projects", "rota proof show", "rota qa query", "rota receipt show",
	"rota refactor age", "rota refactor targets",
	"rota release host", "rota release milestone-check", "rota release notes", "rota release pending", "rota release version",
	"rota repo resolve", "rota repo umbrella", "rota repo which",
	"rota review brief", "rota review depth", "rota review package", "rota review queue", "rota review scaffolding", "rota review scope",
	"rota round candidates", "rota round status", "rota round summary", "rota round watch", "rota round wait",
	"rota ship body", "rota ship strategy",
	"rota skills get", "rota skills list", "rota skills status",
	"rota spike list", "rota spike show",
	"rota status show", "rota statusline dump", "rota summary",
	"rota test ledger check", "rota test run",
	"rota tracker suggest-upstream", "rota verdict show", "rota version",
	"rota worker account list", "rota worker pool list", "rota worker prompt-check", "rota worker session check",
	// The tracker: item, design, plan and proof writers keep their text on the
	// issue (notes and comments) on an issue backend.
	"rota design add", "rota design amend", "rota design put", "rota design rm",
	"rota plan add", "rota plan pass", "rota plan put", "rota plan rm",
	"rota proof add", "rota proof record",
	"rota item claim", "rota item comment add", "rota item complete", "rota item create", "rota item field set",
	"rota item note add", "rota item note rm", "rota item ready", "rota item release", "rota item reopen", "rota item rm",
	"rota item section set", "rota item shipped", "rota item state",
	"rota issues close", "rota issues label", "rota tracker call",
	// Opens a PR: the support side's whole output.
	"rota ship pr",
	// Rotates a support round: workers.json, the lease, the ledger, receipts
	// and status are gitignored; claims, labels and the architecture review
	// item are tracker writes.
	"rota round accept", "rota round architecture", "rota round assign", "rota round bounce", "rota round escalate check",
	"rota round escalate send", "rota round pick", "rota round reclaim", "rota round reconcile", "rota round report",
	"rota round return", "rota round review-relay", "rota round scope", "rota round start", "rota round tick",
	"rota round transfer", "rota round wind-down",
	"rota worker account assign", "rota worker account pick", "rota worker adopt", "rota worker dispatch", "rota worker done",
	"rota worker inbox", "rota worker poll", "rota worker pool init", "rota worker pool reap", "rota worker reply",
	"rota worker reset", "rota worker send", "rota worker session ensure",
	"rota reap", "rota orchestrate", "rota keepalive run", "rota layout split", "rota layout tabs", "rota limit watch",
	// Gitignored per-machine state: hits buffer, bugreports.jsonl, verdicts.json,
	// status.json and handoff, .rota/debug, repos.json. Bug reports are where
	// an issue-less learning goes (D2).
	"rota knowledge hit",
	"rota bugreport add", "rota bugreport dismiss", "rota bugreport draft", "rota bugreport duplicates", "rota bugreport file", "rota bugreport list",
	"rota verdict add", "rota verdict route",
	"rota status add", "rota status handoff", "rota status rm",
	"rota debug counter clear", "rota debug counter fail", "rota debug counter inc-cycle", "rota debug counter init", "rota debug counter pass",
	"rota debug counter record-attempt", "rota debug reset", "rota debug session put", "rota debug session rm", "rota debug verdict",
	"rota repo add", "rota repo rm",
	// Outside the repo: the global config, machine-wide connections and
	// project registry, the harness's hooks and skills.
	"rota config save-global",
	"rota connect add", "rota connect remove",
	"rota projects cleanup", "rota projects remove",
	"rota hook guard", "rota hook install", "rota hook prompt", "rota hook session-start", "rota hook stop", "rota hook uninstall",
	"rota skills install", "rota skills uninstall", "rota skills update",
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
// support. It reads round.role alone, so a bad unrelated round.* key cannot
// open the fence, and a role it cannot read refuses rather than runs.
func supportRoleStop(c *Ctx) (Result, error) {
	if !supportFencedPath(c.Path) {
		return Result{}, nil
	}
	root, err := c.Root()
	if err != nil {
		return Result{}, nil
	}
	return supportRefusal(root, c.Path)
}

// supportRefusal is the exit-4 refusal of what on a support machine, or of any
// fenced verb when round.role cannot be read; nil on main.
func supportRefusal(root, what string) (Result, error) {
	unknown := func(why, hint string) (Result, error) {
		d := jsonObj("blockedBy", "config", "key", "round.role", "changed", false)
		return Result{Data: d}, Refused("%s needs to know this machine's role: %s", what, why).WithHint(hint)
	}
	// config.Load skips a local layer it cannot read or decode, which would
	// read as role main and open the fence.
	if raw, err := os.ReadFile(rotatree.ConfigLocal(root)); err == nil {
		if _, err := jsonx.Decode(raw); err != nil {
			return unknown(".rota/config.local.json does not decode: "+err.Error(), "fix the JSON in .rota/config.local.json, or remove the file on the main machine")
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return unknown(".rota/config.local.json cannot be read: "+err.Error(), "make .rota/config.local.json readable")
	}
	role, err := roundcfg.MachineRole(config.Load(rotatree.Config(root)))
	if err != nil {
		return unknown(err.Error(), "set round.role to main or support: rota config set round.role main")
	}
	if role != roundcfg.RoleSupport {
		return Result{}, nil
	}
	d := jsonObj("blockedBy", "role", "role", roundcfg.RoleSupport, "changed", false)
	return Result{Data: d}, Refused("%s is refused on a support machine (round.role support): only the main orchestrator merges and writes tracked .rota/ content", what).
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
