package round

import (
	"errors"
	"fmt"
	"testing"

	"github.com/l4ci/rota/internal/tracker"
)

func TestReadStageNamesTrackerFailuresOnly(t *testing.T) {
	var se *StageError
	err := readStage("plan", fmt.Errorf("x: %w", &tracker.Error{Kind: tracker.KindUnavailable, Message: "gh timed out"}))
	if !errors.As(err, &se) || se.Stage != "plan" {
		t.Fatalf("got %v, want a plan StageError", err)
	}
	if plain := errors.New("boom"); readStage("plan", plain) != plain {
		t.Fatal("a non-tracker error must pass through")
	}
	if readStage("plan", nil) != nil {
		t.Fatal("nil must stay nil")
	}
}
