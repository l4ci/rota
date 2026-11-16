package cli

import (
	"strings"
	"testing"
)

// `round transfer` takes --settled-file like `round assign`, with --body-file
// kept as its deprecated alias (#3446).
func TestRoundTransferSettledFileHelp(t *testing.T) {
	code, out, errOut := rotaIn(t, t.TempDir(), "round", "transfer", "--help")
	if code != 0 || !strings.Contains(out+errOut, "--settled-file") || !strings.Contains(out+errOut, "deprecated alias for --settled-file") {
		t.Fatalf("help: %d %s %s", code, out, errOut)
	}
}
