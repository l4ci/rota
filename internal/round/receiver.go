package round

import (
	"context"
	"fmt"

	"github.com/l4ci/rota/internal/blockedby"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/worker"
)

// receiverCheck is Transfer's rule for the slot that receives an issue: it is
// provisioned, not adopted, one of the round's active slots (or of its reserve,
// when reserve is set) and, if it holds work, parkable. queue says that held
// work must be queued first. resuming is a slot taking back what it holds.
func (e Env) receiverCheck(ctx context.Context, root string, be Board, reg worker.Registry, set roundcfg.Settings, name string, resuming, reserve bool) (queue bool, err error) {
	s := reg.Slot(name)
	if s == nil {
		return false, &exitcode.Error{Exit: exitcode.ExitResolution, Message: fmt.Sprintf("slot %s is not provisioned: run rota round start", name)}
	}
	if err := fenceExternal(s, "transfer", "check a branch out in a worktree"); err != nil {
		return false, err
	}
	if err := checkActive(reg, set, name, resuming, reserve); err != nil {
		return false, err
	}
	if h := s.HeldID(); h != "" && !resuming {
		if ok, why := e.parkable(ctx, root, be, s); !ok {
			return false, blocked(blockedby.SlotBusy, "%s", busyMsg(name, h, why))
		}
		return true, nil
	}
	return false, nil
}

// Receives says why Transfer would refuse name as the receiver of an issue it
// does not hold, nil when it would not. A caller picking a receiver asks it and
// tries the next slot on an error. It reads only.
func (e Env) Receives(ctx context.Context, root string, be Board, reg worker.Registry, name string, reserve bool) error {
	_, err := e.receiverCheck(ctx, root, be, reg, e.Round, name, false, reserve)
	return err
}
