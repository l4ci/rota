package cli

import (
	"strings"
	"testing"
)

func TestWorkerTrainIntegrationFlag(t *testing.T) {
	dir := planProject(t, gateConfig(t, `{"test":{"full":["true"]}}`, "off", nil), false)
	for _, c := range []struct {
		name string
		args []string
		want string
	}{
		{"with --plan", []string{"--plan"}, "--integration cannot be combined"},
		{"with --land-green", []string{"--land-green"}, "--integration cannot be combined"},
		{"with --on-fail stop", []string{"--on-fail", "stop"}, "--integration cannot be combined"},
		{"slots without PRs", nil, "every member needs an open PR"},
		{"a name that is no ref component", []string{"--integration", "a/b"}, "not a valid branch name"},
	} {
		t.Run(c.name, func(t *testing.T) {
			args := append([]string{"worker", "train", "w1", "w2", "--base", "main", "--integration", "a", "--no-assign-check"}, c.args...)
			code, out, errOut := rotaIn(t, dir, args...)
			if code != 2 || !strings.Contains(out+errOut, c.want) {
				t.Fatalf("exit %d, want 2 with %q:\n%s%s", code, c.want, out, errOut)
			}
		})
	}
}
