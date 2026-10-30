package flakeledger

import (
	"regexp"
	"time"

	"github.com/l4ci/rota/internal/testledger"
)

// Unknown output stays red. Even an assertion summary must not hide a
// process/infrastructure failure in another package of the same command.
var retryInfrastructure = regexp.MustCompile(`(?im)signal:|\bkilled\b|\[build failed\]|\[setup failed\]|^panic:|fatal error:|test timed out|context deadline exceeded|resource temporarily unavailable|cannot allocate memory|no space left on device|permission denied|command not found|executable file not found|fork/exec|connection refused|network is unreachable|i/o timeout`)

// RetryEligible reports whether a red check may be rerun once: exit 1, a
// named assertion failure, not excused by the test ledger (excused) and no
// infrastructure signature. It returns the failing tests the entry names. The
// clean-tree and cancellation checks need the environment and stay with the caller.
func RetryEligible(exitCode int, output string, excused bool) (tests []string, ok bool) {
	if exitCode != 1 || excused || retryInfrastructure.MatchString(output) {
		return nil, false
	}
	tests, assertion := testledger.FailingTests(output)
	if !assertion {
		return nil, false
	}
	return tests, true
}

// NewEntry builds the recovery record shared by the wrapper retry and the
// gate sub-check path.
func NewEntry(check string, tests []string, tree string, ts time.Time, first, retry Run) Entry {
	return Entry{Check: check, Tests: tests, Tree: tree, TS: ts.UTC(), First: first, Retry: retry}
}
