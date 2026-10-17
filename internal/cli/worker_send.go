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
	type sendFlag struct {
		spec worker.EventSpec
		text *string
		set  *bool
	}
	var flags []sendFlag
	var names []string
	for _, spec := range worker.Events {
		if !spec.Send {
			continue
		}
		f := sendFlag{spec: spec, text: new(string), set: new(bool)}
		if spec.SendBody {
			f.text = fs.String(spec.Kind, "", spec.Help)
		} else {
			f.set = fs.Bool(spec.Kind, false, spec.Help)
		}
		flags = append(flags, f)
		names = append(names, "--"+spec.Kind)
	}
	oneOf := "send exactly one of " + strings.Join(names, ", ")
	return func(c *Ctx, args []string) (Result, error) {
		slot, err := oneArg(args, "slot")
		if err != nil {
			return Result{}, err
		}
		kind, body := "", ""
		for _, f := range flags {
			if !*f.set && *f.text == "" {
				continue
			}
			if kind != "" {
				return Result{}, Usage("%s", oneOf)
			}
			kind, body = f.spec.Kind, strings.TrimSpace(*f.text)
		}
		if kind == "" {
			return Result{}, Usage("%s", oneOf)
		}
		if spec, _ := worker.EventSpecFor(kind); spec.SendBody && body == "" {
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
