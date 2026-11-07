package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/worker"
)

// The train result carries verify and diagnose time apart, the original
// failure apart from diagnostic-only ones, and whether anything landed (#2753).
func TestTrainDataTimingAndFailureOrigin(t *testing.T) {
	r := worker.TrainResult{
		Members:      []worker.TrainMember{{Target: "a"}, {Target: "b"}},
		VerifyTime:   3 * time.Second,
		DiagnoseTime: 7 * time.Second,
		Original:     &worker.TrainFailure{Tier: "test.full", Command: "go test", Tests: []string{"TestA"}, Members: 2, Tip: "abc1234"},
		Diagnostic:   []worker.TrainFailure{{Tier: "test.full", Members: 1, Tip: "def5678", Tests: []string{"TestA", "TestB"}, OnlyInDiagnostic: []string{"TestB"}}},
	}
	raw, err := jsonx.Marshal(trainData(r))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		AnyLanded bool `json:"anyLanded"`
		Timing    struct{ VerifyMs, DiagnoseMs int64 }
		Original  struct{ Tests []string }
		Diag      []struct {
			Only []string `json:"onlyInDiagnostic"`
		} `json:"diagnosticFailures"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.AnyLanded || got.Timing.VerifyMs != 3000 || got.Timing.DiagnoseMs != 7000 {
		t.Errorf("timing/landed: %s", raw)
	}
	if !strings.Contains(string(raw), `"originalFailure"`) || len(got.Diag) != 1 || strings.Join(got.Diag[0].Only, ",") != "TestB" {
		t.Errorf("failure origin: %s", raw)
	}
	if s := trainSummary(r); !strings.Contains(s, "verify 3s") || !strings.Contains(s, "diagnose 7s") || !strings.Contains(s, "nothing landed") {
		t.Errorf("summary: %q", s)
	}
}
