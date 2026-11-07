package cli

import (
	"strings"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/ledger"
	"github.com/l4ci/rota/internal/stamp"
)

// trainStatus adds the trains to round status (#2751): each running train with
// the request id that cancels it, and the newest recorded train outcome, so a
// cancelled train reads as cancelled rather than as a failure. It returns the
// text lines; the data goes into d.
func trainStatus(c *Ctx, root string, d *jsonx.Object) []string {
	var lines []string
	runs, err := workerEnvCtx(c, c.Context()).TrainRuns(c.Context(), root)
	if err == nil && len(runs) > 0 {
		rows := []any{}
		for _, r := range runs {
			o := jsonx.NewObject()
			o.Set("requestId", r.RequestID)
			o.Set("targets", strList(r.Targets))
			o.Set("base", r.Base)
			o.Set("pid", r.PID)
			o.Set("since", r.Since)
			rows = append(rows, o)
			lines = append(lines, "train\trunning\t"+r.RequestID+"\t"+strings.Join(r.Targets, ",")+"\tcancel: rota worker train cancel --request-id "+r.RequestID)
		}
		d.Set("trains", rows)
	}
	entries, err := ledger.Load(root)
	if err != nil {
		return lines
	}
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if e.Kind != ledger.KindTrain {
			continue
		}
		o := jsonx.NewObject()
		o.Set("verdict", e.DetailStr("verdict"))
		o.Set("round", e.Round)
		o.Set("at", stamp.Format(e.TS))
		line := "last train\t" + e.DetailStr("verdict") + "\t" + stamp.Format(e.TS)
		if id := e.DetailStr("requestId"); id != "" {
			o.Set("requestId", id)
			line += "\t" + id
		}
		d.Set("lastTrain", o)
		return append(lines, line)
	}
	return lines
}
