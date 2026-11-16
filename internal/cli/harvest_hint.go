package cli

import (
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/worker"
)

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
	var parts []string
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
			}
		}
		return Result{}, nil
	})
	if len(parts) == 0 {
		return ""
	}
	return "HARVEST unharvested learnings on landed " + strings.Join(parts, ", ") +
		"; read them: rota knowledge harvest (or rota item comment list <id> --kind learning), fold in at /rota-learn"
}

func hintHarvest(c *Ctx, root string, issues []string) {
	if l := harvestHintLine(c, root, issues); l != "" {
		fmt.Fprintln(c.Stderr, l)
	}
}
