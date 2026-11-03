package harness

import (
	"reflect"
	"testing"
)

func TestCoreReportingCapabilities(t *testing.T) {
	if got := CoreWorkerKinds(); !reflect.DeepEqual(got, []string{Codex}) {
		t.Fatalf("verified Core workers=%v", got)
	}
	for _, tc := range []struct {
		app  string
		want bool
	}{{Codex, true}, {Claude, false}, {OpenCode, false}, {"", false}, {"unknown", false}, {"Codex", false}} {
		if got := CoreStatusSupported(tc.app); got != tc.want {
			t.Errorf("CoreStatusSupported(%q)=%v want %v", tc.app, got, tc.want)
		}
	}
	kinds := CoreWorkerKinds()
	kinds[0] = "mutated"
	if got := CoreWorkerKinds(); !reflect.DeepEqual(got, []string{Codex}) {
		t.Fatalf("caller mutated Core registry: %v", got)
	}
}
