package worker

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/flakeledger"
	"github.com/l4ci/rota/internal/testledger"
)

const subcheckPrefix = "ROTA-FLAKY "

var subcheckName = regexp.MustCompile(`^(smoke-shard[1-9][0-9]*|go:[A-Za-z0-9._/-]+)$`)

// gateSubchecks reads only unindented protocol lines emitted by test/gate.sh.
// Check-log excerpts are indented by the wrapper and cannot become records.
func gateSubchecks(output, tree string, now time.Time) ([]flakeledger.Entry, error) {
	var entries []flakeledger.Entry
	for _, line := range strings.Split(output, "\n") {
		if !strings.HasPrefix(line, subcheckPrefix) {
			continue
		}
		var wire struct {
			Check        string  `json:"check"`
			FirstExit    *int    `json:"firstExit"`
			RetryExit    *int    `json:"retryExit"`
			FirstOutput  *string `json:"firstOutput"`
			RetryOutput  *string `json:"retryOutput"`
			RetrySeconds float64 `json:"retrySeconds"`
		}
		if len(line) > 64*1024 || json.Unmarshal([]byte(strings.TrimPrefix(line, subcheckPrefix)), &wire) != nil ||
			!subcheckName.MatchString(wire.Check) || wire.FirstExit == nil || *wire.FirstExit != 1 ||
			wire.RetryExit == nil || *wire.RetryExit != 0 || wire.FirstOutput == nil || wire.RetryOutput == nil || wire.RetrySeconds < 0 {
			return nil, fmt.Errorf("invalid gate sub-check recovery record")
		}
		tests, _ := testledger.FailingTests(*wire.FirstOutput)
		if len(tests) == 0 {
			tests = []string{wire.Check}
		}
		entries = append(entries, flakeledger.NewEntry(wire.Check, tests, tree, now,
			flakeledger.Run{ExitCode: *wire.FirstExit, Output: *wire.FirstOutput},
			flakeledger.Run{ExitCode: *wire.RetryExit, Output: *wire.RetryOutput, WallSeconds: wire.RetrySeconds}))
	}
	return entries, nil
}
