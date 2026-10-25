package cli

import (
	"flag"
	"testing"
)

func TestRoundAssignReclaimStaleFlag(t *testing.T) {
	fs := flag.NewFlagSet("assign", flag.ContinueOnError)
	roundAssign(fs)
	if err := fs.Parse([]string{"--reclaim-stale", "mac-ben@3"}); err != nil {
		t.Fatalf("reclaim flag: %v", err)
	}
	if got := fs.Lookup("reclaim-stale").Value.String(); got != "mac-ben@3" {
		t.Fatalf("holder %q", got)
	}
}
