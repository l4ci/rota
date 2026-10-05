package cli

import (
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/worker"
)

// roundWait is `rota round wait`; the loop is worker.Env.Wait. It wakes a slot
// the way `rota worker poll` would classify it, and records the slot it returns.
func roundWait(fs *flag.FlagSet) RunFunc {
	timeout := fs.Float64("timeout", 0, "seconds to wait before giving up; 0 waits indefinitely")
	settle := fs.Float64("settle", 5, "seconds between the two pane captures of one classification")
	lines := fs.Int("lines", 60, "pane lines to classify")
	return func(c *Ctx, args []string) (Result, error) {
		if *timeout < 0 {
			return Result{}, Usage("--timeout must not be negative")
		}
		if *settle < 0 {
			return Result{}, Usage("--settle must not be negative")
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		ctx, stop := workerContext()
		defer stop()
		res, err := workerEnvCtx(c, ctx).Wait(ctx, root, worker.WaitOpts{
			Slots:   args,
			Timeout: time.Duration(*timeout * float64(time.Second)),
			Settle:  time.Duration(*settle * float64(time.Second)),
			Lines:   *lines,
		})
		if err != nil {
			return Result{}, err
		}
		d := jsonx.NewObject()
		if res.TimedOut {
			rows := make([]any, 0, len(res.Slots))
			var text []string
			for _, r := range res.Slots {
				o := jsonx.NewObject()
				o.Set("name", r.Name)
				o.Set("state", strings.ToLower(r.State))
				rows = append(rows, o)
				text = append(text, fmt.Sprintf("%s\t%s", r.Name, strings.ToLower(r.State)))
			}
			d.Set("timedOut", true)
			d.Set("waited", res.Waited.Seconds())
			d.Set("slots", rows)
			msg := fmt.Sprintf("timed out after %.0fs, no slot needs attention", res.Waited.Seconds())
			return Result{Data: d, Text: strings.Join(append(text, msg), "\n")}, Failed("%s", msg)
		}
		state := strings.ToLower(res.State)
		d.Set("slot", res.Slot)
		d.Set("state", state)
		d.Set("evidence", res.Evidence)
		d.Set("source", res.Source)
		d.Set("waited", res.Waited.Seconds())
		return Result{Data: d, Text: fmt.Sprintf("%s\t%s\t%s", res.Slot, state, res.Evidence)}, nil
	}
}
