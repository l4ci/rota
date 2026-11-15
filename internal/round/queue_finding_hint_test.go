package round

import (
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/worker"
)

// The slot hint points at `round accept` only for an item accept takes
// (research, minted review). An ordinary item with a finding gets a step that
// works, and accept refuses it (#3332).
func TestParkableFindingHintAgreesWithAccept(t *testing.T) {
	cases := map[string]struct {
		setup      func(f *moveFx, t *testing.T)
		acceptable bool
	}{
		"research": {func(f *moveFx, t *testing.T) { f.be.items["12"].Labels = []string{"decision:research"} }, true},
		"review":   {func(f *moveFx, t *testing.T) { f.mintReview(t, "12") }, true},
		"ordinary": {func(f *moveFx, t *testing.T) {}, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newMoveFx(t)
			tc.setup(f, t)
			if err := editSlot(f.root, "ben", func(s *worker.Slot) error { s.SetFinding("99"); return nil }); err != nil {
				t.Fatal(err)
			}
			ok, why := f.env.parkable(bg, f.be, f.slot("ben"))
			if ok {
				t.Fatalf("a slot with a finding and no PR is not parkable")
			}
			if got := strings.Contains(why, "rota round accept"); got != tc.acceptable {
				t.Errorf("hint %q mentions accept = %v, want %v", why, got, tc.acceptable)
			}
			if !tc.acceptable && !strings.Contains(why, "round transfer 12 --to human") {
				t.Errorf("hint %q names no working step", why)
			}
		})
	}
}
