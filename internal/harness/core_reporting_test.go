package harness

import (
	"context"
	"reflect"
	"testing"
)

func TestCoreReportingCapabilities(t *testing.T) {
	if got := CoreWorkerKinds(); !reflect.DeepEqual(got, []string{Claude, Codex}) {
		t.Fatalf("verified Core workers=%v", got)
	}
	for _, tc := range []struct {
		app  string
		want bool
	}{{Codex, true}, {Claude, false}, {"claude-code", true}, {OpenCode, false}, {"", false}, {"unknown", false}, {"Codex", false}} {
		if got := CoreStatusSupported(tc.app); got != tc.want {
			t.Errorf("CoreStatusSupported(%q)=%v want %v", tc.app, got, tc.want)
		}
	}
	kinds := CoreWorkerKinds()
	kinds[0] = "mutated"
	if got := CoreWorkerKinds(); !reflect.DeepEqual(got, []string{Claude, Codex}) {
		t.Fatalf("caller mutated Core registry: %v", got)
	}
}

func TestCoreComposerChecked(t *testing.T) {
	for _, tc := range []struct {
		app  string
		want bool
	}{{"claude-code", false}, {Codex, true}, {"", true}, {"unknown", true}} {
		if got := CoreComposerChecked(tc.app); got != tc.want {
			t.Errorf("CoreComposerChecked(%q)=%v want %v", tc.app, got, tc.want)
		}
	}
}

func TestClaudeCorePreflightSucceeds(t *testing.T) {
	_, err := ResolveLauncher(Claude).Preflight(context.Background(), Probe{Look: func(string) (string, bool) {
		t.Fatal("claude preflight must not probe")
		return "", false
	}}, coreOpts(t))
	if err != nil {
		t.Fatalf("claude on Core refused: %v", err)
	}
}
