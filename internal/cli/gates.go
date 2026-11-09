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
	// Fence is the verb's own Command.Fence, copied here for the fence gate.
	// Never inherited.
	Fence roundcfg.FenceClass
	// KeepDispatch keeps the verb's result data on an exit-5 dispatch failure
	// (blockedBy dispatch): it changed state and must name the unsent slot.
	// Never inherited.
	KeepDispatch bool
}

// gatesFor is the gate policy of the verb the dispatcher resolved: chain runs
// from the root down to it. The policies on the way down count, a verb's own
// over its group's. The root's policy is for bare rota alone.
func gatesFor(chain []*Command) Gates {
	if len(chain) == 1 {
		g := chain[0].Gates
		g.Fence = chain[0].Fence
		return g
	}
	var g Gates
	for _, cmd := range chain[1:] {
		g.LegacyExempt = g.LegacyExempt || cmd.Gates.LegacyExempt
		g.TrackedRoleExempt = g.TrackedRoleExempt || cmd.Gates.TrackedRoleExempt
		g.RoundMachine = g.RoundMachine || cmd.Gates.RoundMachine
		g.ConfigExempt = g.ConfigExempt || cmd.Gates.ConfigExempt
		if cmd.Gates.RoundKeys != RoundKeysWarn {
			g.RoundKeys = cmd.Gates.RoundKeys
		}
	}
	last := chain[len(chain)-1]
	g.Fence = last.Fence
	g.KeepDispatch = last.Gates.KeepDispatch
	return g
}

// preflight runs the gates that precede every verb, in order. The project
// root is resolved and its config loaded at most once, however many gates
// need them.
type preflight struct {
	c *Ctx
	g Gates

	rootDone bool
	root     string
	rootErr  error

	cfgDone bool
	cfg     any
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

// config is the merged config of root, loaded once.
func (p *preflight) config(root string) any {
	if !p.cfgDone {
		p.cfg = p.c.deps().Config(root)
		p.cfgDone = true
	}
	return p.cfg
}

// runGates is the gate sequence: the legacy state directory stop, a corrupt config, the old round keys, a
// role in the tracked config, an unnamed support machine, the support fence.
// A gate's refusal ends the sequence with its error and result.
func runGates(c *Ctx, g Gates) (Result, error) {
	p := &preflight{c: c, g: g}
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
