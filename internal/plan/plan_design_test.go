package plan

import (
	"errors"
	"testing"

	"github.com/l4ci/rota/internal/artifact"
)

func TestDesignRefByStore(t *testing.T) {
	notes := NewNotes(nil, nil, nil)
	for _, c := range []struct {
		design string
		issue  bool
		ok     bool
	}{
		{"F3", true, true}, {"3", true, true}, {"B07", true, true}, {"F123", true, true},
		{"S1", true, false}, {"FX", true, false},
		{"F3", false, false}, {"3", false, false},
	} {
		st := Store(Files(t.TempDir()))
		if c.issue {
			st = notes
		}
		_, err := st.DesignRef(false, c.design)
		// File mode with a valid ID fails later on the missing file; only the
		// ID check (usage, exit 2) is under test here.
		var ae *artifact.Error
		usage := errors.As(err, &ae) && ae.Exit == artifact.ExitUsage
		if c.ok == usage {
			t.Errorf("design %q issue=%v: err=%v", c.design, c.issue, err)
		}
	}
}
