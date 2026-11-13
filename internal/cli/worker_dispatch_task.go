package cli

import (
	"context"
	"errors"
	"os"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/worker"
)

// assignedTaskDispatch leaves standalone and relay briefs alone. A round
// recovery rebuilds the assignment before appending the operator's notes.
func assignedTaskDispatch(c *Ctx, ctx context.Context, root string, o worker.DispatchOpts) (worker.DispatchOpts, func(), error) {
	cleanup := func() {}
	assigned, err := round.RecoveryAssigned(root, o)
	if err != nil || !assigned {
		return o, cleanup, err
	}
	be, err := openBacklog(c, root, false, "")
	if err != nil {
		return o, cleanup, err
	}
	board, ok := round.BoardOf(be)
	if !ok {
		return o, cleanup, &Error{Exit: ExitInternal, Message: "the backlog backend has no workflow"}
	}
	notes, err := os.ReadFile(o.BodyFile)
	if err != nil {
		return o, cleanup, Resolution("body file not found: %s", o.BodyFile)
	}
	text, rebuilt, err := roundEnv(c, ctx, root).RecoveryBrief(ctx, root, board, c.deps().Config(root), o, string(notes))
	if err != nil {
		var b *round.BlockedError
		var ce *round.ConfigError
		switch {
		case errors.As(err, &b):
			return o, cleanup, Refused("%s", b.Msg)
		case errors.As(err, &ce):
			return o, cleanup, Usage("%s", ce)
		}
		return o, cleanup, err
	}
	path, cleanup, err := fsio.WriteTemp(c.deps().Getenv("TMPDIR"), "rota-rebuilt-brief-", []byte(text))
	if err != nil {
		return o, cleanup, err
	}
	rebuilt.BodyFile = path
	return rebuilt, cleanup, nil
}

// postSupportRelay carries a relay to the slot's issue as a comment when this
// is a support machine (#1827): the main machine's gate cannot read the
// support machine's local relay log, so the issue thread is where it looks.
// Main keeps its local log only. The brief is already sent; a failed post is
// returned for the caller to warn about.
func postSupportRelay(c *Ctx, root, slot, bodyFile string, n int, pr string) error {
	text, err := os.ReadFile(bodyFile)
	if err != nil {
		return err
	}
	issue, body, post, err := round.SupportRelay(root, c.deps().Config(root), slot, n, string(text), pr)
	if err != nil || !post {
		return err
	}
	be, err := openBacklog(c, root, false, "")
	if err != nil {
		return err
	}
	_, err = be.AddComment(issue, "relay", body)
	return err
}
