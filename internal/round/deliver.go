package round

import (
	"context"
	"errors"
	"os"

	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/worker"
)

// The worker-delivery tail shared by Assign and Transfer: the refusals that
// run before anything is marked, then the steps that pick an account, write the
// pointer brief and hand it to the slot (solo hand-off or dispatch). Both flows
// call it, so a refusal, the failure handling and the dispatched Kind are
// decided once. It owns no lease, registry binding or claim: those stay with
// the caller's saga.

// readBody reads --body-file once: "" is no file, a read error is a usage error.
func readBody(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", usage("--body-file %s: %v", path, err)
	}
	return string(b), nil
}

// deliveryRefusals is what refuses a worker before anything is marked: solo
// runs Claude subagents only (E3, #70), and a worker that cannot start (launch
// flags, host, login) is refused here. dispatch runs the same preflight again.
// A refusal carrying block data comes back as a BlockedError.
func (e Env) deliveryRefusals(ctx context.Context, root, kind, slot, model string) (warnings []string, err error) {
	hz, err := worker.Harness(kind)
	if err != nil {
		return nil, err
	}
	if why := hz.SoloRefusal(); why != "" && isSolo(root) {
		return nil, usage("%s", why)
	}
	setup, err := e.workerEnv().Preflight(ctx, root, kind, slot, model)
	if err != nil {
		var we *exitcode.Error
		if errors.As(err, &we) && we.Exit == exitcode.ExitRefused {
			if bd, ok := we.Data.(worker.BlockData); ok {
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

	// Out: filled as the steps run.
	Account, Host, BriefText, Worktree string
	Dispatched, Changed                bool

	tmpName string
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
	solo := isSolo(root)
	var text string
	steps := []step{
		// The account is the pane's CLAUDE_CONFIG_DIR: keep the slot's own while
		// it has headroom, else pick; none usable is a refusal to start.
		// work.accounts is Anthropic's: a codex slot's CODEX_HOME is its account.
		// Under solo every subagent runs on the orchestrator's own account.
		{name: "account", skip: func() bool {
			return solo || !hz.WorkAccounts() || e.Accounts == nil || len(worker.Configured(root)) == 0
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
			tmp, err := os.CreateTemp("", "rota-round-brief-")
			if err != nil {
				return d.wrap(err)
			}
			d.tmpName = tmp.Name()
			tmp.WriteString(text)
			return d.wrap(tmp.Close())
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
		if _, err := e.workerEnv().Dispatch(ctx, root, worker.DispatchOpts{Slot: d.Slot, BodyFile: d.tmpName, Task: d.Task, Round: &rnd,
			Branch: d.Branch(), Model: d.Model, Kind: d.Kind}); err != nil {
			editSlot(root, d.Slot, func(s *worker.Slot) error { return s.MarkState("idle", "") })
			return err
		}
		d.Dispatched, d.Changed = true, true
		return nil
	}}), nil
}

// cleanup removes the brief's temp file.
func (d *delivery) cleanup() {
	if d.tmpName != "" {
		os.Remove(d.tmpName)
	}
}
