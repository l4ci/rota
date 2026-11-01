package worker

import (
	"errors"
	"testing"
)

func TestLandingStages(t *testing.T) {
	for _, tc := range []struct {
		name string
		done bool
		err  error
	}{
		{name: "handoff"}, {name: "verdict", done: true}, {name: "error", err: errors.New("cannot run")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start := landingStageResult[int, string]{Value: 7}
			first := runLandingStage(start, func(n int) (string, string, bool, error) { return "seven", "refused", tc.done, tc.err })
			second := runLandingStage(first, func(s string) (bool, string, bool, error) { return s == "seven", "accepted", true, nil })
			want := "accepted"
			if tc.done || tc.err != nil {
				want = "refused"
			}
			if second.Result != want || second.Err != tc.err || (want == "accepted" && !second.Value) {
				t.Fatalf("typed handoff or terminal answer lost: %+v", second)
			}
		})
	}
}
