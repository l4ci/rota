package cli

import (
	"bytes"
	"slices"
	"strings"
	"testing"
)

func TestAutopilotTickDeduplicatesMigrationWarnings(t *testing.T) {
	f := newScopeFx(t, "warn", `,"support":"mac"`)
	autopilotOn(t, f.root)
	f.deps.Getwd = func() (string, error) { return f.root, nil }
	f.issues["2"] = strings.Replace(f.issues["2"], `"labels":[]`, `"labels":[{"name":"site:support"}]`, 1)
	set, err := f.deps.roundSettings(f.root, roundConfigRequired)
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	c := &Ctx{Path: "rota round watch", Deps: f.deps, Stderr: &stderr}
	const first = "2 still carries retired label site:support: run rota migrate labels --apply before assigning it"
	const second = "3 still carries retired label site:support: run rota migrate labels --apply before assigning it"
	tick := func(want []string) {
		t.Helper()
		if _, err := autopilotTick(c, f.root, set, "", scopePID); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(c.warnings, want) {
			t.Errorf("warnings = %q, want %q", c.warnings, want)
		}
		var lines []string
		for _, warning := range want {
			lines = append(lines, "rota round watch: warning: "+warning+"\n")
		}
		if got := stderr.String(); got != strings.Join(lines, "") {
			t.Errorf("stderr = %q, want %q", got, strings.Join(lines, ""))
		}
	}
	tick([]string{first})
	tick([]string{first})
	// A newly arriving legacy issue still gets its own warning on the next wake.
	f.issues["3"] = strings.Replace(f.issues["2"], `"number":2`, `"number":3`, 1)
	f.issues["2"] = f.issues["3"]
	tick([]string{first, second})
	tick([]string{first, second})
	// A separate invocation reports the warning again.
	stderr.Reset()
	c = &Ctx{Path: "rota round watch", Deps: f.deps, Stderr: &stderr}
	tick([]string{second})
}
