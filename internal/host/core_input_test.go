package host

import (
	"testing"

	"github.com/l4ci/rota/internal/harness"
)

func TestQueueFooterWithoutGapDoesNotCrash(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("queue footer without a gap pattern panicked: %v", r)
		}
	}()
	const screen = "❯ pending input\n  send pending\n\n────────────\n❯ \n"
	syntax := harness.InputSyntax{Composer: "❯", QueueFooter: "  send pending"}
	output, queue := coreEvidenceRegionsFor(screen, []harness.InputSyntax{syntax})
	if queue != -1 {
		t.Errorf("unbounded queue footer counted as evidence: %d", queue)
	}
	if want := "❯ pending input\n  send pending\n\n────────────\n"; output != want {
		t.Errorf("output = %q, want %q", output, want)
	}
}
