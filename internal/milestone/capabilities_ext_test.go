package milestone_test

import (
	"reflect"
	"testing"

	"github.com/l4ci/rota/internal/milestone"
	"github.com/l4ci/rota/internal/round"
)

const extMilestone = "## Capabilities\n\n- `Rota Round Assign` (modified)\n- round.maxBounces (new)\n"

// A child that copies a milestone capability into its Touches yields exactly
// the milestone's capability names as scopes, so siblings clash with no extra
// declaration.
func TestCapabilitiesMatchScopes(t *testing.T) {
	caps := milestone.CapabilityNames(milestone.Capabilities(extMilestone))

	child := "## Touches\n\n- rota round assign\n- `ROUND.MAXBOUNCES`\n"
	if got := round.Scopes(child); !reflect.DeepEqual(caps, got) {
		t.Fatalf("capability names %v != Touches scopes %v", caps, got)
	}

	other := "## Touches\n\n- rota round assign2\n- `ROUND.MAXBOUNCES`\n"
	want := []string{"round.maxbounces"}
	if got := round.ScopeOverlaps(round.Scopes(child), round.Scopes(other)); !reflect.DeepEqual(got, want) {
		t.Fatalf("overlap = %v, want %v", got, want)
	}
}
