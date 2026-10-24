package cli

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/l4ci/rota/internal/marker"
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

// postSupportRelay carries a relay to the slot's issue as a comment when this
// is a support machine (#1827): the main machine's gate cannot read the
// support machine's local relay log, so the issue thread is where it looks.
// Main keeps its local log only. The brief is already sent; a failed post is
// returned for the caller to warn about.
func postSupportRelay(c *Ctx, root, slot, bodyFile string, round int) error {
	cfg := c.deps().Config(root)
	if roundcfg.SupportName(cfg) == "" {
		return nil
	}
	if role, err := roundcfg.MachineRole(cfg); err != nil || role != roundcfg.RoleSupport {
		return nil
	}
	reg, err := worker.LoadRegistry(root)
	if err != nil {
		return err
	}
	s := reg.Slot(slot)
	if s == nil || s.HeldID() == "" {
		return nil
	}
	text, err := os.ReadFile(bodyFile)
	if err != nil {
		return err
	}
	be, err := openBacklog(c, root, false, "")
	if err != nil {
		return err
	}
	_, err = be.AddComment(s.HeldID(), "relay", marker.RelayLine(round)+"\n"+strings.TrimSpace(string(text)))
	return err
}
