package milestone

import (
	"strings"

	"github.com/l4ci/rota/internal/acceptance"
	"github.com/l4ci/rota/internal/section"
)

// ShippedSummary is the closing record, using the milestone's existing
// checkbox audit rather than item acceptance marks. closed holds item bullets.
func ShippedSummary(body string, closed []string) string {
	var b strings.Builder
	b.WriteString("## Shipped\n\n### Closed items\n\n")
	if len(closed) == 0 {
		b.WriteString("_(none)_\n")
	} else {
		b.WriteString(strings.Join(closed, "\n") + "\n")
	}
	b.WriteString("\n### Acceptance\n\n")
	crits := acceptance.IDs(body)
	if len(crits) == 0 {
		b.WriteString("_(none)_\n")
	}
	unmet := Audit(body, nil).Unticked
	for _, c := range crits {
		status := "met"
		for _, id := range unmet {
			if id == c.ID {
				status = "unmet"
				break
			}
		}
		b.WriteString("- " + c.ID + ": " + status + " — " + c.Text + "\n")
	}
	b.WriteString("\n### Decisions\n\n")
	decisions := strings.TrimSpace(section.Body(body, DecisionsSection))
	if decisions == "" {
		decisions = "_(none)_"
	}
	b.WriteString(decisions + "\n")
	return b.String()
}

// closedItemBullets reads the generated file-mode Items list when no backlog
// reader was supplied (the standalone milestone API).
func closedItemBullets(body string) []string {
	var out []string
	for _, line := range strings.Split(section.Body(body, ItemsHeading), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "- ") {
			continue
		}
		for _, reason := range []string{"done", "dropped", "handed-off", "blocked"} {
			if strings.HasSuffix(line, " ("+reason+")") {
				out = append(out, line)
				break
			}
		}
	}
	return out
}
