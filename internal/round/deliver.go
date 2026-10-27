package round

import (
	"context"
	"errors"

	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/worker"
)

// The worker-delivery tail shared by Assign and Transfer: the refusals that
// run before anything is marked, then the steps that pick an account, write the
// pointer brief and hand it to the slot (solo hand-off or dispatch). Both flows
// call it, so a refusal, the failure handling and the dispatched Kind are
// decided once. It owns no lease, registry binding or claim: those stay with
// the caller's saga.

// deliveryRefusals is what refuses a worker before anything is marked: solo
// runs Claude subagents only (E3, #70), and a worker that cannot start (launch
// flags, host, login) is refused here. dispatch runs the same preflight again.
// A refusal carrying block data comes back as a BlockedError.
func (e Env) deliveryRefusals(ctx context.Context, root, kind, slot, model string) (warnings []string, err error) {
	hz, err := worker.Harness(kind)
	if err != nil {
		return nil, err
	}
	if why := hz.SoloRefusal(); why != "" && worker.IsSolo(root) {
		return nil, usage("%s", why)
	}
	setup, err := e.Worker.Preflight(ctx, root, kind, slot, model)
	if err != nil {
		var we *exitcode.Error
		if errors.As(err, &we) && we.Exit == exitcode.ExitRefused {
			if bd, ok := exitcode.DataOf[worker.BlockData](err); ok {
				return nil, blocked(bd.BlockedBy, "%s", we.Message)
			}
		}
		return nil, err
	}
	return setup.Warnings, nil
}

// delivery is one hand-off of a brief to a slot.
type delivery struct {
	Slot, Task  string
	Kind, Model string
	Round       int
	// Branch is read when the dispatch runs: a transfer settles it in an
	// earlier step.
	Branch func() string
	// Brief is the pointer brief's text, built when the step runs (the caller's
	// earlier steps may settle what it names).
	Brief func() string
	// Wrap maps a step's failure onto the exit table; nil leaves it as is.
	Wrap func(error) error
	// LogRelay records the brief in the slot's relay log once dispatched. A
	// transfer sets it: its brief is the receiver's only signed message from
	// the orchestrator, so a PR citing it as a relay passes provenance. An
	// assignment brief is not a relay.
	LogRelay bool

	// Out: filled as the steps run.
	Host                         host.Kind
	Account, BriefText, Worktree string
	Shared                       []worker.SharedDir
	Dispatched, Changed          bool

	tmpName     string
	removeBrief func()
}

func (d *delivery) wrap(err error) error {
	if d.Wrap == nil {
		return err
	}
	return d.Wrap(err)
}

// steps are the delivery's saga steps: account, write the brief, then the solo
// hand-off or the dispatch. Call cleanup once they have run.
func (d *delivery) steps(ctx context.Context, e Env, root string) ([]step, error) {
	hz, err := worker.Harness(d.Kind)
	if err != nil {
		return nil, err
	}
	solo := worker.IsSolo(root)
	var text string
	steps := []step{
		// The account is the pane's CLAUDE_CONFIG_DIR: keep the slot's own while
		// it has headroom, else pick; none usable is a refusal to start.
		// round.accounts is Anthropic's: a codex slot's CODEX_HOME is its account.
		// Under solo every subagent runs on the orchestrator's own account.
		{name: "account", skip: func() bool {
			return solo || !hz.WorkAccounts() || !e.AccountPicking || len(e.Worker.Accounts().Configured(root)) == 0
		}, do: func() error {
			name, err := e.pickAccount(ctx, root, d.Slot)
			d.Account = name
			return d.wrap(err)
		}},
		{name: "write the brief", do: func() error {
			text = d.Brief()
			if solo {
				return nil
			}
			path, cleanup, err := fsio.WriteTemp(e.Worker.Getenv("TMPDIR"), "rota-round-brief-", []byte(text))
			if err != nil {
				return d.wrap(err)
			}
			d.tmpName, d.removeBrief = path, cleanup
			d.BriefText = text
			return nil
		}},
	}
	if solo {
		// No pane: mark the slot busy and hand the brief back.
		return append(steps, step{name: "solo hand-off", do: func() error {
			b, wt, err := e.soloHandOff(root, d.Slot, text, d.Round)
			if err != nil {
				return d.wrap(err)
			}
			d.Host, d.BriefText, d.Worktree = host.Solo, b, wt
			d.Changed = true
			return nil
		}}), nil
	}
	// keep: the pane may already hold the brief, so earlier steps stay. A failed
	// dispatch marks the slot idle, which is how a repeated call knows the work
	// was not delivered and resumes it.
	return append(steps, step{name: "dispatch", keep: true, do: func() error {
		rnd := d.Round
		dr, err := e.Worker.Dispatch(ctx, root, worker.DispatchOpts{Slot: d.Slot, BodyFile: d.tmpName, Task: d.Task, Round: &rnd,
			Branch: d.Branch(), Model: d.Model, Kind: d.Kind})
		d.Shared = dr.Shared
		if err != nil {
			editSlot(root, d.Slot, func(s *worker.Slot) error { return s.MarkState("idle", "") })
			return err
		}
		d.Dispatched, d.Changed = true, true
		if d.LogRelay {
			return d.wrap(worker.LogRelay(root, d.Slot, rnd, e.now(), d.BriefText))
		}
		return nil
	}}), nil
}

// cleanup removes the brief's temp file.
func (d *delivery) cleanup() {
	if d.removeBrief != nil {
		d.removeBrief()
	}
}
