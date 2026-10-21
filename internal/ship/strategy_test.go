package ship

import (
	"errors"
	"testing"

	"github.com/l4ci/rota/internal/tracker"
)

// Strategy is the landing rule: a tracker backlog needs a PR, a local forge has
// none to open, anything else follows work.mergeStrategy.
func TestStrategy(t *testing.T) {
	for _, c := range []struct {
		name, backend, forge, configured string
		want                             string
		err                              error
	}{
		{"file, pr, github", "file", "github", "pr", "pr", nil},
		{"file, direct, github", "file", "github", "direct", "direct", nil},
		{"file, unset, github", "file", "github", "", "unset", nil},
		{"file, unknown forge keeps configured", "file", tracker.ProviderUnknown, "pr", "pr", nil},
		{"file, pr, local forge", "file", tracker.ProviderLocal, "pr", "direct", nil},
		{"file, unset, local forge", "file", tracker.ProviderLocal, "", "direct", nil},
		{"tracker forces pr", "issues", "gitlab", "direct", "pr", nil},
		{"tracker, unset config", "issues", "github", "", "pr", nil},
		{"tracker on a local forge", "issues", tracker.ProviderLocal, "pr", "", ErrTrackerNeedsLocalForge},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := Strategy(c.backend, c.forge, c.configured)
			if !errors.Is(err, c.err) {
				t.Fatalf("err = %v, want %v", err, c.err)
			}
			if got.Strategy != c.want {
				t.Errorf("strategy = %q, want %q", got.Strategy, c.want)
			}
			if c.err == nil && got.Reason == "" {
				t.Error("reason is empty")
			}
		})
	}
}
