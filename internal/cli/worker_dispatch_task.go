package cli

import (
	"context"
	"errors"
	"os"

	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/worker"
)

// assignedTaskDispatch leaves standalone and relay briefs alone. A round
// recovery rebuilds the assignment before appending the operator's notes.
func assignedTaskDispatch(c *Ctx, ctx context.Context, root string, o worker.DispatchOpts) (worker.DispatchOpts, func(), error) {
	cleanup := func() {}
	if o.Relay || o.Task == "" {
		return o, cleanup, nil
	}
	reg, err := worker.LoadRegistry(root)
	if err != nil {
		return o, cleanup, err
	}
	s := reg.Slot(o.Slot)
	if s == nil || s.Task() != worker.NormID(o.Task) || s.ClaimID() == "" {
		return o, cleanup, nil
	}
	be, err := openBacklog(c, root, false, "")
	if err != nil {
		return o, cleanup, err
	}
	board, ok := round.BoardOf(be)
	if !ok {
		return o, cleanup, &Error{Exit: ExitInternal, Message: "the backlog backend has no workflow"}
	}
	set, err := roundcfg.FromConfig(c.deps().Config(root))
	if err != nil {
		return o, cleanup, Usage("%s", err)
	}
	env := c.deps().RoundEnv(ctx, root)
	env.Worker = workerEnvCtx(c, ctx)
	text, rebuilt, err := env.RebuildTaskBrief(ctx, root, board, o, set)
	if err != nil {
		var b *round.BlockedError
		if errors.As(err, &b) {
			return o, cleanup, Refused("%s", b.Msg)
		}
		return o, cleanup, err
	}
	notes, err := os.ReadFile(o.BodyFile)
	if err != nil {
		return o, cleanup, Resolution("body file not found: %s", o.BodyFile)
	}
	tmp, err := os.CreateTemp("", "rota-rebuilt-brief-")
	if err != nil {
		return o, cleanup, err
	}
	cleanup = func() { os.Remove(tmp.Name()) }
	if _, err = tmp.WriteString(text + "\nNotes appended for recovery:\n\n" + string(notes)); err == nil {
		err = tmp.Close()
	} else {
		tmp.Close()
	}
	if err != nil {
		cleanup()
		return o, func() {}, err
	}
	rebuilt.BodyFile = tmp.Name()
	return rebuilt, cleanup, nil
}
