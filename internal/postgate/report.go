package postgate

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/l4ci/rota/internal/bugreport"
	"github.com/l4ci/rota/internal/marker"
	"github.com/l4ci/rota/internal/worker"
)

// RenderReport describes a completed gate without changing its outcome.
func RenderReport(r worker.GateResult) string {
	var out strings.Builder
	fmt.Fprintf(&out, "%s\n### Gate report\n\nKind: `gate`\n\nOutcome: **%s**\n\nReviewed SHA: `%s`\n\n### Checks run\n\n", marker.Report("gate", r.SHA), r.Verdict, r.SHA)
	for i, check := range r.Verified {
		if i == 20 {
			out.WriteString("- Further checks are listed in the local gate output.\n")
			break
		}
		fmt.Fprintf(&out, "- `%s`\n", publicGateText(check))
	}
	if r.VerifySkipped {
		out.WriteString("Verification skipped.\n")
	} else if len(r.Verified) == 0 {
		out.WriteString("No completed checks recorded.\n")
	}
	if r.Err != "" {
		fmt.Fprintf(&out, "\n### Refusal\n\n%s\n", publicGateText(r.Err))
	}
	if r.Hint != "" {
		fmt.Fprintf(&out, "\n### Next step\n\n%s\n", publicGateText(r.Hint))
	}
	if r.Err != "" || r.Hint != "" {
		out.WriteString("\nLocal gate verdict: [ledger](.rota/ledger.jsonl). Full output stays on the gate host.\n")
	}
	return out.String()
}

// Keep the reason and next action, never the machine-captured output tail.
// Redact before clipping so a token straddling the limit cannot leak a prefix.
var localPathRE = regexp.MustCompile("(^|[\\s\"'`=(:])(?:[A-Za-z]:[\\\\/]|~?/)[^\\s\"'`<>),;]*")

func publicGateText(text string) string {
	text, _, _ = strings.Cut(text, "\n")
	text = localPathRE.ReplaceAllString(text, "${1}[local path]")
	text = bugreport.Redact(text, bugreport.Env{})
	if chars := []rune(text); len(chars) > 512 {
		const suffix = "… (truncated; see local gate output)"
		text = string(chars[:512-len([]rune(suffix))]) + suffix
	}
	return reportProse(text)
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
