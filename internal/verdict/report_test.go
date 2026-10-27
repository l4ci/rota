package verdict

import (
	"reflect"
	"strings"
	"testing"
)

func TestRenderReportReview(t *testing.T) {
	r := NewRecord(ReviewSpec, Concerns, "abc1234", Body{
		Summary: "Review found one blocker and one deferred improvement.",
		Findings: []Finding{
			{Severity: "minor", Title: "Name the timeout", File: "run.go", Line: 18, Detail: "Use a named duration.", Fix: "The timeout has a descriptive name."},
			{Severity: "blocker", Title: "Missing permission check", File: "run.go", Line: 7, Detail: "Untrusted callers can write."},
		},
		Declined: []Declined{{Title: "Production throughput", Detail: "Requires a production trace."}},
		Items:    []ItemVerdict{{ID: "2132", Verdict: Concerns}},
	}, recAt)
	got := RenderReport(r, nil)
	for _, want := range []string{
		"<!-- rota:report review-spec sha=abc1234 -->\n",
		"Kind: `review-spec`", "Verdict: **CONCERNS**", "Reviewed SHA: `abc1234`",
		"### Blocker\n", "**Missing permission check** (blocking)", "`run.go:7`",
		"### Minor\n", "**Name the timeout** (deferred)", "The timeout has a descriptive name.",
		"### Declined findings\n", "**Production throughput**", "Requires a production trace.",
		"### Item verdicts\n", "2132: **CONCERNS**",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("report lacks %q:\n%s", want, got)
		}
	}
	if strings.Index(got, "### Blocker") > strings.Index(got, "### Minor") {
		t.Error("findings are not grouped in severity order")
	}
}

func TestRenderReportQA(t *testing.T) {
	b, err := ParseBody(`{"verdict":"INFRA-FAIL","summary":"go test ./...: blocked because the fixture database was unavailable.","findings":[{"severity":"info","title":"Database unavailable","detail":"No product assertion ran."}],"declined":[{"title":"Checkout flow","detail":"Requires the database."}]}`)
	if err != nil {
		t.Fatal(err)
	}
	r := NewRecord(QA, InfraFail, "def5678", b, recAt)
	got := RenderReport(r, []string{".rota/qa-runs/2026-10-26/runner.log"})
	for _, want := range []string{
		"<!-- rota:report qa sha=def5678 -->", "Verdict: **INFRA-FAIL**",
		"### Checks run\n\ngo test ./...: blocked because the fixture database was unavailable.",
		"### Artifacts\n\n- `.rota/qa-runs/2026-10-26/runner.log`\n",
		"No product assertion ran.", "Requires the database.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("QA report lacks %q:\n%s", want, got)
		}
	}
	_, body, ok := strings.Cut(got, "```json\n")
	if !ok {
		t.Fatal("report has no fenced JSON body")
	}
	body, _, ok = strings.Cut(body, "\n```\n")
	if !ok {
		t.Fatal("report has no closing JSON fence")
	}
	decoded, err := ParseBody(body)
	if err != nil || !reflect.DeepEqual(decoded, b) {
		t.Fatalf("report body changed: %+v (%v), want %+v", decoded, err, b)
	}
	if got := RenderReport(r, nil); !strings.Contains(got, "### Artifacts\n\nNone recorded.") {
		t.Fatal("QA report invents an artifact when none exists")
	}
}

func TestRenderReportKinds(t *testing.T) {
	for _, kind := range BranchKinds {
		t.Run(kind, func(t *testing.T) {
			r := NewRecord(kind, Pass, "abc1234", Body{}, recAt)
			got := RenderReport(r, nil)
			if !strings.HasPrefix(got, "<!-- rota:report "+kind+" sha=abc1234 -->\n") {
				t.Fatalf("report marker: %s", got)
			}
			for _, line := range strings.Split(got, "\n") {
				if strings.HasPrefix(line, "## ") || strings.HasPrefix(line, "# ") {
					t.Fatalf("report escapes its file-backend ## Reports section: %s", line)
				}
			}
		})
	}
}

func TestRenderReportPreservesFileSection(t *testing.T) {
	b := Body{
		Summary:  "Checks completed.\n## Other section\n<!-- rota:report qa sha=fake -->",
		Findings: []Finding{{Severity: "info", Title: "Diagnostic\n## Another heading", File: "a.go\n## File heading", Detail: "Observed\n## Detail heading", Patch: "@@ -1 +1 @@\n## Patch heading\n```"}},
		Declined: []Declined{{Title: "Not checked\n## Declined heading"}},
	}
	r := NewRecord(QA, Pass, "abc1234", b, recAt)
	got := RenderReport(r, nil)
	if !strings.Contains(got, "(informational)") {
		t.Error("info finding was labelled as a deferred task")
	}
	for i, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(line, "## ") || strings.HasPrefix(line, "# ") {
			t.Errorf("line %d escapes the ## Reports section: %s", i+1, line)
		}
		if i > 0 && strings.HasPrefix(line, "<!-- rota:report ") {
			t.Errorf("line %d creates a second report identity: %s", i+1, line)
		}
	}
	_, block, ok := strings.Cut(got, "### Verdict body\n\n")
	if !ok {
		t.Fatal("missing verdict body")
	}
	fenceLine, block, _ := strings.Cut(block, "\n")
	fence := strings.TrimSuffix(fenceLine, "json")
	body := strings.TrimSuffix(block, fence+"\n")
	decoded, err := ParseBody(body)
	if err != nil || decoded.Summary != b.Summary || decoded.Findings[0].Patch != b.Findings[0].Patch {
		t.Fatalf("JSON lost original markdown: %+v (%v)", decoded, err)
	}
}
