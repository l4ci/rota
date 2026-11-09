package cli

import (
	"strings"
)

// applyGates declares each verb's pre-verb gate policy on the tree. A group's
// policy covers the verbs under it unless a verb or sub-group sets its own,
// except the fence class, which each verb declares on its own Command
// (Command.Fence): an unclassified verb is refused on a support machine. A path missing from the tree is
// a programming error, so it panics rather than leave a policy unapplied.
func applyGates(root *Command) {
	// Bare rota is the orchestrator launcher: with an old work.* round key in
	// the config it refuses like a round verb. The root's policy is its own and
	// is not inherited by the verbs under it.
	root.Gates.RoundKeys = RoundKeysRefuse
	for _, d := range gateDecls {
		for _, p := range d.paths {
			d.apply(&gateTarget(root, p).Gates)
		}
	}
}

func gateTarget(root *Command, path string) *Command {
	cmd := findVerb(root, strings.Fields(path))
	if cmd == nil || cmd == root {
		panic("gate declaration names " + path + ", which is not in the tree")
	}
	return cmd
}

// gateDecls are the policies other than the support fence, which each verb
// declares on its Command.
var gateDecls = []struct {
	paths []string
	apply func(*Gates)
}{
	// Run on a project that still holds the legacy state directory: the migration itself and doctor
	// (which reports it), the verbs that never read project state and the hooks
	// that must not fail a Claude session.
	{[]string{"migrate hv", "doctor", "version", "update", "skills", "hook", "statusline"}, func(g *Gates) { g.LegacyExempt = true }},
	// Read the round-only keys: with an old work.* name still in the config
	// they refuse (exit 4) rather than run on a key nothing reads any more
	// (#1097 D4).
	{[]string{"round", "worker", "doctor", "orchestrate", "keepalive", "layout", "limit"}, func(g *Gates) { g.RoundKeys = RoundKeysRefuse }},
	// Neither refuse nor warn: the ones that migrate or edit the config, and
	// those that must stay silent (hooks, the statusline) or never read project
	// state. `worker prompt-check` is the Codex prompt hook, which reads no
	// config and must not block a worker's prompt. `worker done|inbox|send|reply`
	// run from a worker's own worktree, whose tracked config.json can predate
	// the rename: a refusal there would send the worker to rewrite tracked
	// config on its branch (#1163). They never read the worktree's config.
	// `worker done` does read round.accounts (for the ledger headroom), but
	// through the pool root, the main checkout that holds the slot registry
	// (returnRoot), so the old names in the worktree never reach it.
	{[]string{"migrate", "config", "init", "update", "setup", "hook", "statusline", "version", "skills",
		"worker prompt-check", "worker done", "worker inbox", "worker send", "worker reply"}, func(g *Gates) { g.RoundKeys = RoundKeysQuiet }},
	// May run with round.role in the tracked config, which they are there to fix.
	{[]string{"config", "migrate"}, func(g *Gates) { g.TrackedRoleExempt = true }},
	// Run with a corrupt project or local config, each for its own reason:
	// config and migrate edit it to repair it; init refuses a bad file on its
	// own terms; setup, version, update and statusline never load it; hook
	// loads it and degrades silently, since a hook must not break the session;
	// skills reads round.accounts and runs on defaults; worker prompt-check is
	// a Codex hook that only verifies a signature.
	{[]string{"config", "migrate", "init", "setup", "version", "update", "skills", "hook", "statusline",
		"worker prompt-check"}, func(g *Gates) { g.ConfigExempt = true }},
	// Refused for an unnamed support machine before any round verb reads or
	// claims issues; config verbs stay usable to repair the name.
	{[]string{"round"}, func(g *Gates) { g.RoundMachine = true }},
	// round assign changes state before it dispatches: an exit-5 dispatch
	// failure keeps its result so the slot it could not reach is named.
	{[]string{"round assign"}, func(g *Gates) { g.KeepDispatch = true }},
}
