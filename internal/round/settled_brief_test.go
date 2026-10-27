package round

import (
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/worker"
)

func TestSettledAnswersRebrief(t *testing.T) {
	for _, key := range []string{"briefDecisions", "briefSettledAnswers"} {
		t.Run(key, func(t *testing.T) {
			f := newAssignFixture(t)
			if _, err := f.assign("12", "ben", nil); err != nil {
				t.Fatal(err)
			}
			if _, err := worker.UpdateSlot(f.root, "ben", func(s *worker.Slot) {
				s.Raw().Set(key, "m: Preserve the original answer.")
			}); err != nil {
				t.Fatal(err)
			}
			brief, _, err := f.env.RebuildTaskBrief(bg, f.root, f.be, worker.DispatchOpts{Slot: "ben", Task: "12"}, f.env.Round)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(brief, "Settled answers (verbatim):\n\nm: Preserve the original answer.") {
				t.Fatalf("saved answers lost on re-brief:\n%s", brief)
			}
		})
	}
}
