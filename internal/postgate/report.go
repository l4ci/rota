package postgate

import (
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/marker"
	"github.com/l4ci/rota/internal/worker"
)

// RenderReport describes a completed gate without changing its outcome.
func RenderReport(r worker.GateResult) string {
	var out strings.Builder
	fmt.Fprintf(&out, "%s\n### Gate report\n\nKind: `gate`\n\nOutcome: **%s**\n\nReviewed SHA: `%s`\n\n### Checks run\n\n", marker.Report("gate", r.SHA), r.Verdict, r.SHA)
	for _, check := range r.Verified {
		fmt.Fprintf(&out, "- `%s`\n", reportProse(check))
	}
	if r.VerifySkipped {
		out.WriteString("Verification skipped.\n")
	} else if len(r.Verified) == 0 {
		out.WriteString("No completed checks recorded.\n")
	}
	if r.Err != "" {
		fmt.Fprintf(&out, "\n### Refusal\n\n%s\n", reportProse(r.Err))
	}
	if r.Hint != "" {
		fmt.Fprintf(&out, "\n### Next step\n\n%s\n", reportProse(r.Hint))
	}
	return out.String()
}

// Preserve arbitrary command output without introducing a sibling file
// section or a second report marker in a file-backend detail.
func reportProse(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "## ") || strings.HasPrefix(line, "# ") {
			lines[i] = "\\" + line
		} else if strings.HasPrefix(line, "<!-- rota:report ") {
			lines[i] = "&lt;" + line[1:]
		}
	}
	return strings.Join(lines, "\n")
}
