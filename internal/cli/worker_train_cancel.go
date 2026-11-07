package cli

import (
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/receipt"
	"github.com/l4ci/rota/internal/worker"
)

// workerTrainCancel is `rota worker train cancel --request-id <id>` (#2751):
// it signals the train holding the id, which stops its tests, removes its
// scratch tree, frees the land lock and settles its receipt as cancelled, then
// reports that receipt. An id no running train holds cancels nothing: exit 4
// when its call already settled, exit 3 when rota knows no call under it.
func workerTrainCancel(fs *flag.FlagSet) RunFunc {
	rid := fs.String("request-id", "", "the request id of the train to cancel (printed by `rota worker train`, listed by `rota round status`)")
	wait := fs.Duration("wait", 2*time.Minute, "how long to wait for the train to stop and settle")
	return func(c *Ctx, args []string) (Result, error) {
		if err := noArgs(args); err != nil {
			return Result{}, err
		}
		id := strings.TrimSpace(*rid)
		if id == "" {
			return Result{}, Usage("--request-id is required")
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		ctx := c.Context()
		d := jsonx.NewObject()
		d.Set("requestId", id)
		d.Set("changed", false)
		run, err := workerEnvCtx(c, ctx).CancelTrain(ctx, root, id, *wait)
		if errors.Is(err, worker.ErrTrainNotRunning) {
			return notRunning(root, id, d)
		}
		if err != nil {
			if run.PID != 0 {
				d.Set("pid", run.PID)
			}
			return Result{Data: d}, Unavailable("%v", err)
		}
		d.Set("pid", run.PID)
		d.Set("targets", strList(run.Targets))
		e, found, err := receipt.New(root).Show(id)
		if err != nil {
			return Result{Data: d}, err
		}
		if !found {
			d.Set("changed", true)
			d.Set("state", receiptAbsent)
			c.Warn("train pid %d stopped but left no receipt for %s; inspect the base and `git worktree list` before you re-run", run.PID, id)
			return Result{Data: d, Text: fmt.Sprintf("stopped: train %s (pid %d); no receipt recorded", strings.Join(run.Targets, ", "), run.PID)}, nil
		}
		if len(e.Data) > 0 {
			if v, err := jsonx.Decode(e.Data); err == nil {
				d.Set("result", v)
			}
		}
		if e.Status != receipt.Cancelled {
			// The train settled some other way before the signal took effect.
			d.Set("state", e.Status)
			return Result{Data: d}, Refused("train %s settled as %s before the cancel took effect: %s", id, e.Status, e.Message)
		}
		d.Set("changed", true)
		d.Set("state", receiptCancelled)
		text := fmt.Sprintf("cancelled: train %s (pid %d) — %s", strings.Join(run.Targets, ", "), run.PID, e.Message)
		if e.Text != "" {
			text += "\n" + e.Text
		}
		return Result{Data: d, Text: text}, nil
	}
}

// notRunning answers a cancel for an id no live train holds, and cancels
// nothing: a settled call is a refusal naming how it settled, an id rota has
// no receipt for is unknown.
func notRunning(root, id string, d *jsonx.Object) (Result, error) {
	e, found, err := receipt.New(root).Show(id)
	if err != nil {
		return Result{Data: d}, err
	}
	if !found {
		return Result{Data: d}, Resolution("no running train holds request id %s, and no call under it is recorded; `rota round status` lists the running trains", id)
	}
	d.Set("verb", e.Verb)
	d.Set("state", e.Status)
	if e.Verb != "worker train" {
		return Result{Data: d}, Refused("request id %s belongs to %s, not a train; only trains can be cancelled", id, e.Verb)
	}
	if e.Status == receipt.Failed {
		return Result{Data: d}, Refused("the train under request id %s is not running: its call stopped part way (`rota receipt show %s`); nothing was cancelled", id, id)
	}
	return Result{Data: d}, Refused("the train under request id %s is not running: it already settled as %s; nothing was cancelled", id, e.Status)
}
