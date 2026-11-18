package host

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// Both adapters consume the same pane fixtures. Core's receipt baseline and
// queue classification are intentionally stronger than herdr's boolean echo.
func TestDeliveryEvidenceFixtures(t *testing.T) {
	b, err := os.ReadFile("testdata/delivery-evidence.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Name, Text, Screen, Before string
		Herdr                      bool
		Core                       string
	}
	if err := json.Unmarshal(b, &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, f := range fixtures {
		t.Run(f.Name, func(t *testing.T) {
			t.Run("herdr", func(t *testing.T) {
				output := f.Screen
				if i := strings.LastIndexAny(output, promptMarkers); i >= 0 {
					output = output[:i]
				}
				if got := relayQueued(output, f.Text); got != f.Herdr {
					t.Errorf("echo = %v, want %v", got, f.Herdr)
				}
			})
			t.Run("herdr-core", func(t *testing.T) {
				if got := coreDeliveryEvidence(f.Screen, f.Text, coreTerminal{}, f.Before); got != f.Core {
					t.Errorf("evidence = %q, want %q", got, f.Core)
				}
			})
		})
	}
}
