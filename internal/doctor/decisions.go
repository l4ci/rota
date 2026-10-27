package doctor

import (
	"os"
	"strings"

	"github.com/l4ci/rota/internal/knowledge"
	"github.com/l4ci/rota/internal/rotatree"
)

// decisions uses the same validator as the standalone check and writers. A
// missing file adds no row; an undated entry is detail on a passing check.
func (d *runner) decisions() (Check, bool) {
	if d.in.ProjectRoot == "" {
		return Check{}, false
	}
	raw, err := os.ReadFile(rotatree.Decisions(d.in.ProjectRoot))
	if os.IsNotExist(err) {
		return Check{}, false
	}
	const hint = "run: rota decisions amend --topic <topic> --title <title> --part rule|why|forbids|permits --body-file <file>"
	if err != nil {
		return Check{Name: "decisions", Status: Warn, Detail: err.Error(), Hint: "restore read access to .rota/DECISIONS.md"}, true
	}
	report := knowledge.CheckDecisions(string(raw))
	row := Check{Name: "decisions", Status: Pass, Detail: "DECISIONS.md entries are well-formed"}
	if len(report.Errors) > 0 {
		row.Status = Warn
		row.Detail = strings.Join(report.Errors, "; ")
		row.Hint = hint
	}
	for _, warning := range report.Warnings {
		row.Detail += "; notice: " + warning
	}
	return row, true
}
