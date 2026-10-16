package cli

import (
	"flag"
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/worker"
)

// workerSend is `rota worker send <slot> --blocked <q> | --question <q> |
// --heartbeat`: a worker's typed event on its own slot. The writer is
// round.ReportSlot, the same one `worker done` and `worker reply` record
// through, so `round wait` reads every push path the same way and believes it
// before the pane. The ROTA-BLOCKED sentinel is still printed after it.
func workerSend(fs *flag.FlagSet) RunFunc {
	blocked := fs.String("blocked", "", "the one question the worker is blocked on")
	question := fs.String("question", "", "a question for the orchestrator; the worker waits for the answer")
	heartbeat := fs.Bool("heartbeat", false, "alive, not progress: restarts the stall clock, writes no ledger row")
	return func(c *Ctx, args []string) (Result, error) {
		slot, err := oneArg(args, "slot")
		if err != nil {
			return Result{}, err
		}
		kind, body := "", ""
		for _, o := range []struct {
			kind, text string
			set        bool
		}{{worker.EventBlocked, *blocked, *blocked != ""}, {worker.EventQuestion, *question, *question != ""}, {worker.EventHeartbeat, "", *heartbeat}} {
			if !o.set {
				continue
			}
			if kind != "" {
				return Result{}, Usage("send exactly one of --blocked, --question, --heartbeat")
			}
			kind, body = o.kind, strings.TrimSpace(o.text)
		}
		if kind == "" {
			return Result{}, Usage("send exactly one of --blocked, --question, --heartbeat")
		}
		if kind != worker.EventHeartbeat && body == "" {
			return Result{}, Usage("--%s needs the text of the question", kind)
		}
		// The registry lives in the main checkout; a worker runs this from its worktree.
		poolRoot, err := returnRoot(c, slot)
		if err != nil {
			return Result{}, err
		}
		res, err := c.deps().baseRoundEnv().ReportSlot(c.Context(), poolRoot, round.ReportOpts{Slot: slot, Event: kind, Body: body})
		if err != nil {
			return Result{}, err
		}
		d := jsonx.NewObject()
		d.Set("slot", res.Slot)
		d.Set("event", kind)
		d.Set("state", res.State)
		d.Set("previous", res.Previous)
		d.Set("changed", res.Changed)
		if ev := worker.LoadRegistryTolerant(poolRoot).Slot(slot).Event(); ev != nil {
			d.Set("seq", ev.Seq)
		}
		return Result{Data: d, Text: fmt.Sprintf("%s\t%s", res.Slot, kind)}, nil
	}
}
