package postgate

import (
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/worker"
)

func TestRenderReportMerged(t *testing.T) {
	got := RenderReport(worker.GateResult{Verdict: worker.GatePass, SHA: "abc1234", Verified: []string{"go test ./internal/verdict", "bash test/fast.sh"}})
	want := "<!-- rota:report gate sha=abc1234 -->\n### Gate report\n\nKind: `gate`\n\nOutcome: **pass**\n\nReviewed SHA: `abc1234`\n\n### Checks run\n\n- `go test ./internal/verdict`\n- `bash test/fast.sh`\n"
	if got != want {
		t.Fatalf("gate report:\n%s\nwant:\n%s", got, want)
	}
	if strings.Contains(got, "Refusal") {
		t.Fatal("successful report claims a refusal")
	}
}

func TestRenderReportRefused(t *testing.T) {
	got := RenderReport(worker.GateResult{
		Verdict: worker.GateReviewMissing, SHA: "def5678", VerifySkipped: true,
		Err: "review-quality is missing at head", Hint: "Run rota-review on the current head.",
	})
	want := "<!-- rota:report gate sha=def5678 -->\n### Gate report\n\nKind: `gate`\n\nOutcome: **review-missing**\n\nReviewed SHA: `def5678`\n\n### Checks run\n\nVerification skipped.\n\n### Refusal\n\nreview-quality is missing at head\n\n### Next step\n\nRun rota-review on the current head.\n"
	if got != want {
		t.Fatalf("refusal report:\n%s\nwant:\n%s", got, want)
	}
	if got := RenderReport(worker.GateResult{Verdict: worker.GateReviewMissing, SHA: "def5678"}); !strings.Contains(got, "No completed checks recorded.") {
		t.Fatal("gate report claims checks ran without recorded verification")
	}
}

func TestRenderReportProtectsFileSections(t *testing.T) {
	got := RenderReport(worker.GateResult{
		Verdict: worker.GateVerifyFailed, SHA: "abc1234", Verified: []string{"printf 'check\n## Check heading'"},
		Err: "command failed:\n## Error heading\n<!-- rota:report gate sha=fake -->", Hint: "Retry:\n## Hint heading",
	})
	for i, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(line, "## ") || (i > 0 && strings.HasPrefix(line, "<!-- rota:report ")) {
			t.Errorf("line %d escaped its report: %s", i+1, line)
		}
	}
}
