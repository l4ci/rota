package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/worker"
)

// harvestHintTimeout bounds the tracker reads behind the hint.
const harvestHintTimeout = 8 * time.Second

// harvestHintLine says which just-landed issues carry `learning` comments no
// `harvested` mark names yet, and how to read them. Advisory like the assign
// nudge: an unreadable tracker, a file backend or a forge that cannot name
// writers reads as no hint, and nothing is written. "" means no hint.
func harvestHintLine(c *Ctx, root string, issues []string) string {
	var ids []string
	seen := map[string]bool{}
	for _, i := range issues {
		if id := worker.NormID(i); id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return ""
	}
	if issue, err := issueBackend(c.deps(), root); err != nil || !issue {
		return ""
	}
	trust, err := c.deps().workerEnv().WriterTrust(root)
	if err != nil {
		return ""
	}
	// Advice never holds up a landing: a slow tracker means no hint.
	parent := c.Context()
	ctx, cancel := context.WithTimeout(parent, harvestHintTimeout)
	defer cancel()
	c.ctx = ctx
	defer func() { c.ctx = parent }()
	var parts []string
	var cmds []string
	_, _ = withBackend(c, root, false, "", func(be backlog.Backend) (Result, error) {
		for _, id := range ids {
			learnings, marks, err := learningsAndMarks(be, trust, id)
			if err != nil {
				continue
			}
			done := map[string]bool{}
			for _, m := range marks {
				done[strings.TrimSpace(m.Text)] = true
			}
			n := 0
			for _, l := range learnings {
				if !done[l.ID] {
					n++
				}
			}
			if n > 0 {
				parts = append(parts, fmt.Sprintf("#%s (%d)", id, n))
				cmds = append(cmds, "rota item comment list "+id+" --kind learning")
			}
		}
		return Result{}, nil
	})
	if ctx.Err() != nil || len(parts) == 0 {
		return ""
	}
	return "HARVEST unharvested learnings on landed " + strings.Join(parts, ", ") +
		"; read: " + strings.Join(cmds, "; ") + " (fold in at /rota-learn)"
}

func hintHarvest(c *Ctx, root string, issues []string) {
	if l := harvestHintLine(c, root, issues); l != "" {
		fmt.Fprintln(c.Stderr, l)
	}
}
