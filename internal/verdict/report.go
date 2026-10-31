package verdict

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/marker"
)

// RenderReport renders the durable, human-readable report for a recorded verdict.
// artifacts names paths the caller has checked exist.
// The final JSON block has the existing ParseBody shape, not the store's shape.
func RenderReport(r Record, artifacts []string) string {
	var out strings.Builder
	fmt.Fprintf(&out, "%s\n### Verdict report\n\nKind: `%s`\n\nVerdict: **%s**\n\nReviewed SHA: `%s`\n", marker.Report(r.Kind, r.Sha), r.Kind, r.Verdict, r.Sha)
	section := "Summary"
	if r.Kind == QA {
		section = "Checks run"
	}
	fmt.Fprintf(&out, "\n### %s\n\n%s\n", section, marker.ReportProse(reportOrNone(r.Summary)))
	out.WriteString("\n### Findings by severity\n")
	for _, severity := range severities {
		fmt.Fprintf(&out, "\n### %s\n\n", strings.ToUpper(severity[:1])+severity[1:])
		found := false
		for _, f := range r.Findings {
			if f.Severity != severity {
				continue
			}
			found = true
			disposition := "deferred"
			if f.IsBlocking() {
				disposition = "blocking"
			} else if f.Severity == "info" {
				disposition = "informational"
			}
			fmt.Fprintf(&out, "- **%s** (%s)%s\n", marker.ReportProse(f.Title), disposition, reportLocation(f.File, f.Line))
			reportDetail(&out, f.Detail)
			if f.Fix != "" {
				reportDetail(&out, "Expected: "+f.Fix)
			}
			if f.Patch != "" {
				// A file detail's section scanner does not parse code fences.
				// Indent both fence and body so raw patch headings stay inside
				// Reports, while Markdown still displays the original patch.
				fmt.Fprintf(&out, "\n  %sdiff\n  %s\n  %s\n", reportFence(f.Patch), strings.ReplaceAll(strings.TrimRight(f.Patch, "\n"), "\n", "\n  "), reportFence(f.Patch))
			}
		}
		if !found {
			out.WriteString("None.\n")
		}
	}
	out.WriteString("\n### Declined findings\n\n")
	if len(r.Declined) == 0 {
		out.WriteString("None.\n")
	}
	for _, d := range r.Declined {
		fmt.Fprintf(&out, "- **%s**%s\n", marker.ReportProse(d.Title), reportLocation(d.File, d.Line))
		reportDetail(&out, d.Detail)
	}
	out.WriteString("\n### Item verdicts\n\n")
	if len(r.Items) == 0 {
		out.WriteString("None.\n")
	}
	for _, it := range r.Items {
		fmt.Fprintf(&out, "- %s: **%s**\n", marker.ReportProse(it.ID), it.Verdict)
	}
	if r.Kind == QA || len(artifacts) > 0 {
		out.WriteString("\n### Artifacts\n\n")
		if len(artifacts) == 0 {
			out.WriteString("None recorded.\n")
		}
		for _, path := range artifacts {
			fmt.Fprintf(&out, "- `%s`\n", marker.ReportProse(path))
		}
	}
	findings := r.Findings
	if findings == nil {
		findings = []Finding{}
	}
	// Body.Verdict is excluded from its JSON, so include the recorded verdict
	// explicitly while preserving the producer's unmodified body fields.
	payload := struct {
		Verdict string `json:"verdict"`
		Body
	}{r.Verdict, Body{Summary: r.Summary, Findings: findings, Items: r.Items, Declined: r.Declined}}
	// A markdown snippet for a report, not a state file: jsonx byte-compat does not apply.
	raw, _ := json.MarshalIndent(payload, "", "  ") // these field types cannot fail
	fence := reportFence(string(raw))
	fmt.Fprintf(&out, "\n### Verdict body\n\n%sjson\n%s\n%s\n", fence, raw, fence)
	return out.String()
}

func reportOrNone(text string) string {
	if text == "" {
		return "None recorded."
	}
	return text
}

func reportLocation(file string, line int) string {
	if file == "" {
		return ""
	}
	if line > 0 {
		file += fmt.Sprintf(":%d", line)
	}
	return " — `" + marker.ReportProse(file) + "`"
}

func reportDetail(out *strings.Builder, detail string) {
	if detail != "" {
		fmt.Fprintf(out, "  %s\n", strings.ReplaceAll(detail, "\n", "\n  "))
	}
}

func reportFence(text string) string {
	fence := "```"
	for strings.Contains(text, fence) {
		fence += "`"
	}
	return fence
}
