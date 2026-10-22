package cli

import (
	"errors"
	"reflect"
	"testing"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/roundcfg"
)

// One Config seam covers strict and tolerant callers and the exit exceptions
// promised by the verb contracts, without repeating bad-config tests per verb.
func TestRoundSettings(t *testing.T) {
	const badStall = `round.stallMinutes must be an integer (got soon)`
	const badBounces = `round.maxBounces must be an integer (got many)`
	for _, tc := range []struct {
		name, cfg       string
		policy          roundConfigPolicy
		tolerant, valid bool
		exit            int
		message         string
	}{
		{name: "good", cfg: `{"round":{"scope":"open","stallMinutes":7}}`, valid: true},
		{name: "bad", cfg: `{"round":{"stallMinutes":"soon"}}`, exit: ExitResolution, message: badStall},
		{name: "usage contract", cfg: `{"round":{"stallMinutes":"soon"}}`, policy: roundConfigUsage, exit: ExitUsage, message: badStall},
		{name: "bounce maxBounces contract", cfg: `{"round":{"maxBounces":"many"}}`, policy: roundConfigBounce, exit: ExitInternal, message: badBounces},
		{name: "bounce other key", cfg: `{"round":{"stallMinutes":"soon"}}`, policy: roundConfigBounce, exit: ExitResolution, message: badStall},
		{name: "tolerant good", cfg: `{"round":{"scope":"open","stallMinutes":7}}`, tolerant: true, valid: true},
		// A later parse error must also clear fields that parsed successfully.
		{name: "tolerant bad", cfg: `{"round":{"stallMinutes":7,"maxBounces":"many"}}`, tolerant: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := jsonx.Decode([]byte(tc.cfg))
			if err != nil {
				t.Fatal(err)
			}
			d := &Deps{Config: func(root string) any {
				if root != "memory-project" {
					t.Fatalf("config root = %q", root)
				}
				return cfg
			}}
			var set roundcfg.Settings
			if tc.tolerant {
				var ok bool
				set, ok = d.tolerantRoundSettings("memory-project")
				if ok != tc.valid {
					t.Fatalf("valid = %v, want %v", ok, tc.valid)
				}
			} else {
				set, err = d.roundSettings("memory-project", tc.policy)
				if tc.valid {
					if err != nil {
						t.Fatal(err)
					}
				} else {
					var ce *Error
					if !errors.As(err, &ce) || ce.Exit != tc.exit || ce.Message != tc.message {
						t.Fatalf("error = %#v, want exit %d and message %q", err, tc.exit, tc.message)
					}
				}
			}
			if tc.valid {
				if set.Scope != "open" || set.StallMinutes != 7 {
					t.Fatalf("loaded settings = %+v", set)
				}
			} else if !reflect.DeepEqual(set, roundcfg.Settings{}) {
				t.Fatalf("bad config returned partial settings: %+v", set)
			}
		})
	}
}
