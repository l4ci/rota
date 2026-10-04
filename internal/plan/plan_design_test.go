package plan

import (
	"errors"
	"github.com/l4ci/rota/internal/exitcode"
	"testing"
)

func TestExtrasDesignIDByMode(t *testing.T) {
	for _, c := range []struct {
		design string
		issue  bool
		ok     bool
	}{
		{"F3", true, true}, {"3", true, true}, {"B07", true, true}, {"F123", true, true},
		{"S1", true, false}, {"FX", true, false}, {"", true, true},
		{"F3", false, false}, {"3", false, false},
	} {
		_, _, err := extras(t.TempDir(), AddOpts{Title: "t", Design: c.design}, c.issue)
		// File mode with a valid ID fails later on the missing file; only the
		// ID check (usage, exit 2) is under test here.
		var ae *exitcode.Error
		usage := errors.As(err, &ae) && ae.Exit == exitcode.ExitUsage
		if c.ok == usage {
			t.Errorf("design %q issue=%v: err=%v", c.design, c.issue, err)
		}
	}
}
