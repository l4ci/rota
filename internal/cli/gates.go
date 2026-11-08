package cli

import (
	"strings"

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
	// LegacyExempt runs on a project that still holds .hv/. Inherited.
	LegacyExempt bool
	// RoundKeys is the verb's answer to an old work.* round key. The nearest
	// non-default policy up the tree wins.
	RoundKeys RoundKeyPolicy
	// TrackedRoleExempt runs with round.role in the tracked config. Inherited.
	TrackedRoleExempt bool
	// RoundMachine refuses an unnamed support machine. Inherited.
	RoundMachine bool
	// Fence is the support-fence class. Never inherited: each verb declares
	// its own, so a new verb cannot be allowed on a support machine by default.
	Fence roundcfg.FenceClass
}

// gatesFor is the gate policy of the verb at path ("rota group verb"): the
// policies on the way down from the root, a verb's own over its group's. The
// root's policy is for bare rota alone.
func gatesFor(root *Command, path string) Gates {
	words := strings.Fields(path)
	if len(words) <= 1 {
		return root.Gates
	}
	var g Gates
	cmd := root
	for _, w := range words[1:] {
		if cmd = cmd.sub(w); cmd == nil {
			break
		}
		g.LegacyExempt = g.LegacyExempt || cmd.Gates.LegacyExempt
		g.TrackedRoleExempt = g.TrackedRoleExempt || cmd.Gates.TrackedRoleExempt
		g.RoundMachine = g.RoundMachine || cmd.Gates.RoundMachine
		if cmd.Gates.RoundKeys != RoundKeysWarn {
			g.RoundKeys = cmd.Gates.RoundKeys
		}
	}
	if cmd != nil {
		g.Fence = cmd.Gates.Fence
	}
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

// runGates is the gate sequence: the legacy .hv/ stop, the old round keys, a
// role in the tracked config, an unnamed support machine, the support fence.
// A gate's refusal ends the sequence with its error and result.
func runGates(c *Ctx, root *Command) (Result, error) {
	p := &preflight{c: c, g: gatesFor(root, c.Path)}
	if err := p.legacyState(); err != nil {
		return Result{}, err
	}
	for _, gate := range []func() (Result, error){p.roundKeys, p.roleInTrackedConfig, p.roundMachine, p.supportRole} {
		if res, err := gate(); err != nil {
			return res, err
		}
	}
	return Result{}, nil
}
