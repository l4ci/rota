package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/landing"
	"github.com/l4ci/rota/internal/worker"
)

// An integration branch's package holds its fix commits, not the members
// rota merged before them.
func TestReviewVerbsShareIntegrationFixSpan(t *testing.T) {
	plain, _ := reviewProject(t)
	start := strings.TrimSpace(gittest.Run(t, plain, "rev-parse", "main"))
	gittest.Run(t, plain, "checkout", "-q", "-b", "integration/r55-a", "main")
	gittest.Run(t, plain, "merge", "-q", "--no-ff", "-m", "rota train merge", "feat/x")
	merged := strings.TrimSpace(gittest.Run(t, plain, "rev-parse", "HEAD"))
	write(t, filepath.Join(plain, "fix.txt"), "the fix")
	gittest.Run(t, plain, "add", "fix.txt")
	gittest.Run(t, plain, "commit", "-q", "-m", "Fix the interaction")
	gittest.Run(t, plain, "checkout", "-q", "main")
	if err := worker.RecordIntegration(plain, landing.Integration{Name: "a", Branch: "integration/r55-a", Round: 55, Base: "main",
		Start: start, Assembled: merged, RotaMerges: []string{merged}}); err != nil {
		t.Fatal(err)
	}

	write(t, filepath.Join(plain, ".rota", "config.json"), `{"forge":{"provider":"local"},"ship":{"review":{"default":"full","lightBelow":2}}}`)
	for _, verb := range [][]string{{"review", "depth"}, {"review", "scope"}, {"review", "brief"}, {"review", "scaffolding"}, {"change", "classify"}} {
		o := trRun(t, plain, "", append(verb, "integration/r55-a", "--json")...)
		if o.code != 0 {
			t.Fatalf("%v: exit %d %s", verb, o.code, o.stderr)
		}
		d := envelope(t, o.stdout)["data"].(map[string]any)
		switch verb[1] {
		case "depth":
			if d["changedLines"] != float64(1) || d["depth"] != "light" {
				t.Errorf("depth must measure only the fix, like the gate: %v", d)
			}
		case "scope":
			if d["commitCount"] != float64(1) || !reflect.DeepEqual(d["touchedFiles"], []any{"fix.txt"}) {
				t.Errorf("scope must contain only the fix: %v", d)
			}
		case "brief":
			if d["commitCount"] != float64(1) || strings.Contains(d["brief"].(string), "a.txt") {
				t.Errorf("brief must contain only the fix: %v", d)
			}
		case "scaffolding":
			if len(d["findings"].([]any)) != 0 {
				t.Errorf("scaffolding must exclude member commits: %v", d)
			}
		case "classify":
			if d["changedLines"] != float64(1) || !reflect.DeepEqual(d["files"], []any{"fix.txt"}) {
				t.Errorf("classify must contain only the fix: %v", d)
			}
		}
	}
	o := trRun(t, plain, "", "review", "package", "integration/r55-a", "--json")
	if o.code != 0 {
		t.Fatalf("package: exit %d %s", o.code, o.stderr)
	}
	body, err := os.ReadFile(envelope(t, o.stdout)["data"].(map[string]any)["path"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "fix.txt") || strings.Contains(string(body), "a.txt") {
		t.Fatalf("want only the fix commit packaged:\n%s", body)
	}
	// Orchestrator previews also accept the remote-tracking branch ref.
	gittest.Run(t, plain, "remote", "add", "origin", filepath.Join(t.TempDir(), "origin.git"))
	gittest.Run(t, plain, "update-ref", "refs/remotes/origin/integration/r55-a", "integration/r55-a")
	for _, verb := range [][]string{{"review", "depth"}, {"review", "package"}} {
		o := trRun(t, plain, "", append(verb, "origin/integration/r55-a", "--json")...)
		if o.code != 0 {
			t.Fatalf("remote %v: %s", verb, o.stderr)
		}
		d := envelope(t, o.stdout)["data"].(map[string]any)
		if verb[1] == "depth" {
			if d["changedLines"] != float64(1) {
				t.Errorf("remote depth lost the integration fix span: %v", d)
			}
		} else if d["files"] != float64(1) {
			t.Errorf("remote package lost the integration fix span: %v", d)
		}
	}

	// An explicit re-review boundary still overrides the integration default.
	o = trRun(t, plain, "", "review", "package", "integration/r55-a", "--since", start, "--json")
	if o.code != 0 {
		t.Fatal(o.stderr)
	}
	body, err = os.ReadFile(envelope(t, o.stdout)["data"].(map[string]any)["path"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "a.txt") {
		t.Errorf("explicit --since lost the member diff: %s", body)
	}

	// With no fix, the assembled tip is the boundary: there is no diff.
	gittest.Run(t, plain, "branch", "integration/r55-clean", merged)
	if err := worker.RecordIntegration(plain, landing.Integration{Name: "clean", Branch: "integration/r55-clean", Round: 55, Base: "main", Start: start, Assembled: merged, RotaMerges: []string{merged}}); err != nil {
		t.Fatal(err)
	}
	o = trRun(t, plain, "", "review", "depth", "integration/r55-clean", "--json")
	if o.code != 0 {
		t.Fatal(o.stderr)
	}
	if d := envelope(t, o.stdout)["data"].(map[string]any); d["changedLines"] != float64(0) {
		t.Errorf("no fixes must mean an empty span: %v", d)
	}

}
