package cli

import (
	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/exitmap"
	"github.com/l4ci/rota/internal/roundcfg"
)

// RoundKeyPolicy is what a verb does when the config still names a round-only
// key under work.*.
type RoundKeyPolicy uint8

const (
	// RoundKeysWarn, the default, warns and runs.
	RoundKeysWarn RoundKeyPolicy = iota
	// RoundKeysRefuse refuses (exit 4): the verb reads the round keys.
	RoundKeysRefuse
	// RoundKeysQuiet neither refuses nor warns.
	RoundKeysQuiet
)

// Gates is how the pre-verb gates treat a verb. The zero value is the
// default: every gate applies as it does to any verb. tree_gates.go declares
// the policies; a verb takes its group's unless it sets its own.
type Gates struct {
	// LegacyExempt runs on a project that still holds the legacy state directory. Inherited.
	LegacyExempt bool
	// RoundKeys is the verb's answer to an old work.* round key. The nearest
	// non-default policy up the tree wins.
	RoundKeys RoundKeyPolicy
	// TrackedRoleExempt runs with round.role in the tracked config. Inherited.
	TrackedRoleExempt bool
	// RoundMachine refuses an unnamed support machine. Inherited.
	RoundMachine bool
	// ConfigExempt runs with a project or local config that is corrupt: the
	// verbs that repair it, refuse it on their own (init), never load it, or
	// load it and degrade silently (hook, skills). Inherited.
	ConfigExempt bool
}

// merge folds the group policy g under child: a boolean is set when either
// side sets it, and a non-default RoundKeys policy of child beats g's.
func (g Gates) merge(child Gates) Gates {
	out := Gates{
		LegacyExempt:      g.LegacyExempt || child.LegacyExempt,
		TrackedRoleExempt: g.TrackedRoleExempt || child.TrackedRoleExempt,
		RoundMachine:      g.RoundMachine || child.RoundMachine,
		ConfigExempt:      g.ConfigExempt || child.ConfigExempt,
		RoundKeys:         g.RoundKeys,
	}
	if child.RoundKeys != RoundKeysWarn {
		out.RoundKeys = child.RoundKeys
	}
	return out
}

// gatesFor is the gate policy of the verb the dispatcher resolved: chain runs
// from the root down to it. The policies on the way down count, a verb's own
// over its group's. The root's policy is for bare rota alone.
func gatesFor(chain []*Command) Gates {
	var g Gates
	if len(chain) == 1 {
		g = chain[0].Gates
	} else {
		for _, cmd := range chain[1:] {
			g = g.merge(cmd.Gates)
		}
	}
	return g
}

// preflight runs the gates that precede every verb, in order. The project
// root is resolved and its config loaded at most once, however many gates
// need them.
type preflight struct {
	c *Ctx
	g Gates
	// fence is the verb's own Command.Fence, never inherited from a group.
	fence roundcfg.FenceClass

	rootDone bool
	root     string
	rootErr  error
}

// corruptConfig refuses (exit 5, naming the file) a project or local config
// that exists but does not parse or is not a JSON object: the merged load would
// otherwise skip it and run on defaults, dropping every configured key. A blank
// file reads as {}. Outside a project there is nothing to check; the global
// layer keeps its warn-and-skip.
func (p *preflight) corruptConfig() (Result, error) {
	if p.g.ConfigExempt {
		return Result{}, nil
	}
	root, ok := p.project()
	if !ok {
		return Result{}, nil
	}
	for _, layer := range []config.Layer{config.LayerProject, config.LayerLocal} {
		if err := config.CheckLayer(root, layer); err != nil {
			return Result{}, configFail(err, exitmap.ConfigInternal)
		}
	}
	return Result{}, nil
}

// project is the project root, false outside one: a gate with nothing to
// check there passes.
func (p *preflight) project() (string, bool) {
	if !p.rootDone {
		p.root, p.rootErr = p.c.Root()
		p.rootDone = true
	}
	return p.root, p.rootErr == nil
}

// config is the merged config of root, the one the verb reads through Ctx.Project.
func (p *preflight) config(root string) any {
	return p.c.projectConfig(root)
}

// runGates is the gate sequence: the legacy state directory stop, a corrupt config, the old round keys, a
// role in the tracked config, an unnamed support machine, the support fence.
// A gate's refusal ends the sequence with its error and result.
func runGates(c *Ctx, g Gates, fence roundcfg.FenceClass) (Result, error) {
	p := &preflight{c: c, g: g, fence: fence}
	if err := p.legacyState(); err != nil {
		return Result{}, err
	}
	for _, gate := range []func() (Result, error){p.corruptConfig, p.roundKeys, p.roleInTrackedConfig, p.roundMachine, p.supportRole} {
		if res, err := gate(); err != nil {
			return res, err
		}
	}
	return Result{}, nil
}

// The support-fence classes, short for the verb literals that declare one.
const (
	fenceRefused = roundcfg.FenceRefused
	fenceInVerb  = roundcfg.FenceInVerb
	fenceAllowed = roundcfg.FenceAllowed
)
