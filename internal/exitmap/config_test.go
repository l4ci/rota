package exitmap

import (
	"errors"
	"fmt"
	"testing"

	"github.com/l4ci/rota/internal/blockedby"
	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/fsio"
)

func TestTranslateConfigTable(t *testing.T) {
	wrap := func(e error) error { return fmt.Errorf("ctx: %w", e) }
	for _, tc := range []struct {
		name    string
		err     error
		classes Class
		exit    int // 0: passes through unchanged
	}{
		{"malformed key", config.ErrMalformedKey, ConfigUsage, exitcode.ExitUsage},
		{"not schema key", wrap(config.ErrNotSchemaKey), ConfigUsage, exitcode.ExitUsage},
		{"bad value", config.ErrBadValue, ConfigUsage, exitcode.ExitUsage},
		{"project owned", config.ErrProjectOwned, ConfigUsage, exitcode.ExitUsage},
		{"not object", config.ErrNotObject, ConfigInternal, exitcode.ExitInternal},
		{"local not object", config.ErrLocalNotObject, ConfigInternal, exitcode.ExitInternal},
		{"global not object", wrap(config.ErrGlobalNotObject), ConfigInternal, exitcode.ExitInternal},
		{"unreadable", fsio.ErrUnreadable, ConfigInternal, exitcode.ExitInternal},
		{"corrupt", config.ErrCorrupt, ConfigCorrupt, exitcode.ExitInternal},
		{"config conflict", &config.ConflictError{}, ConfigRefusal, exitcode.ExitRefused},
		{"account conflict", &config.AccountConflictError{}, ConfigRefusal, exitcode.ExitRefused},
		{"merge conflict", &config.MergeConflictError{}, ConfigRefusal, exitcode.ExitRefused},
		// A class left out of the options passes the error through.
		{"corrupt outside internal", config.ErrCorrupt, ConfigInternal, 0},
		{"unreadable outside corrupt", fsio.ErrUnreadable, ConfigCorrupt | ConfigRefusal, 0},
		{"not object outside usage", config.ErrNotObject, ConfigUsage, 0},
		{"unrelated", errors.New("other"), Config, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, out := Translate(tc.err, Options{Classes: tc.classes})
			if tc.exit == 0 {
				if out != tc.err {
					t.Fatalf("want pass-through, got %v", out)
				}
				return
			}
			if got := exitOf(t, out); got != tc.exit {
				t.Fatalf("exit %d, want %d", got, tc.exit)
			}
		})
	}
}

func TestTranslateConfigRefusalCarriesConflicts(t *testing.T) {
	err := &config.MergeConflictError{Conflicts: []config.MergeConflict{{File: "f", Old: "a", New: "b"}}}
	data, out := Translate(err, Options{Classes: ConfigRefusal})
	if exitOf(t, out) != exitcode.ExitRefused {
		t.Fatalf("not refused: %v", out)
	}
	got := dataJSON(t, data)
	want := `{"blockedBy": "` + blockedby.MergeConflict + `", "conflicts": [{"file": "f", "old": "a", "new": "b"}], "changed": false}`
	if got != want {
		t.Fatalf("data %s, want %s", got, want)
	}
}
