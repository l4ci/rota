package cli

import (
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/worker"
)

// A conflict path is exact bytes; the TAB-separated nudge must render it
// quoted so one conflict stays one line, while the JSON keeps it exact.
func TestTrainNudgeQuotesUnsafeConflictPath(t *testing.T) {
	ns := trainNudges([]waitingPR{
		{PR: "#9", Base: "main", Reviewed: true},
		{PR: "#10", Base: "main", Reviewed: true},
	})
	odd := "a\tb\nc.go"
	n := ns[0].withPlan(&worker.TrainPlan{
		Conflicts: []worker.TrainConflict{{A: "9", B: "10", Paths: []string{odd, "plain.go"}}},
		Landable:  []string{"9"},
	})
	lines := strings.Split(n.Text(), "\n")
	if len(lines) != 2 {
		t.Fatalf("want one conflict line and one train line, got %q", lines)
	}
	if !strings.Contains(lines[0], `on "a\tb\nc.go", plain.go;`) || strings.Count(lines[0], "\t") != 3 {
		t.Errorf("conflict line = %q", lines[0])
	}
	o, _ := jsonx.MarshalCompact(trainNudgeData([]trainNudge{n}))
	if !strings.Contains(string(o), `a\tb\nc.go`) {
		t.Errorf("json lost the exact path: %s", o)
	}
}
