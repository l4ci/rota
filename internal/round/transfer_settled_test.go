package round

import (
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/worker"
)

// A transfer's brief carries the settled answers and decision topics the
// original assign recorded, without the orchestrator passing them again (#3446).
func TestTransferCarriesForwardSettledAnswersAndDecisionContext(t *testing.T) {
	f := newMoveFx(t)
	if err := editSlot(f.root, "ben", func(s *worker.Slot) error {
		s.SetBriefContext("Orchestrator: use the shared helper", nil)
		s.SetBriefDecisionContext("## Decisions\nno harness pins")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f.host.sent = ""
	if _, err := f.transfer("12", "dana", nil); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Settled answers", "use the shared helper", "no harness pins"} {
		if !strings.Contains(f.host.sent, want) {
			t.Errorf("brief lacks %q:\n%s", want, f.host.sent)
		}
	}
	if got := f.slot("dana").BriefSettledAnswers(); got != "Orchestrator: use the shared helper" {
		t.Errorf("receiver keeps the settled answers: %q", got)
	}
}

// Answers passed to the transfer replace the recorded ones.
func TestTransferExplicitSettledAnswersWin(t *testing.T) {
	f := newMoveFx(t)
	if err := editSlot(f.root, "ben", func(s *worker.Slot) error {
		s.SetBriefContext("old answer", nil)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f.host.sent = ""
	if _, err := f.transfer("12", "dana", func(o *TransferOpts) { o.Decisions = "new answer" }); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.host.sent, "new answer") || strings.Contains(f.host.sent, "old answer") {
		t.Errorf("brief:\n%s", f.host.sent)
	}
}

// A queued PR keeps the settled answers of its slot's assignment too.
func TestTransferQueuedPRCarriesForwardSettledAnswers(t *testing.T) {
	f := newMoveFx(t)
	if err := editSlot(f.root, "ben", func(s *worker.Slot) error {
		s.SetBriefContext("queued answer", nil)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f.finish(t, "ben", pr7)
	if _, err := f.assign("13", "ben"); err != nil {
		t.Fatal(err)
	}
	f.host.sent = ""
	if _, err := f.transfer("12", "dana", nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.host.sent, "queued answer") {
		t.Errorf("brief lacks the queued PR's settled answers:\n%s", f.host.sent)
	}
}
