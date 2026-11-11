package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/worker"
)

// An integration branch's package holds its fix commits, not the members
// rota merged before them.
func TestReviewPackageIntegrationDefaultsSince(t *testing.T) {
	plain, _ := reviewProject(t)
	start := strings.TrimSpace(gittest.Run(t, plain, "rev-parse", "main"))
	gittest.Run(t, plain, "checkout", "-q", "-b", "integration/r55-a", "main")
	gittest.Run(t, plain, "merge", "-q", "--no-ff", "-m", "rota train merge", "feat/x")
	merged := strings.TrimSpace(gittest.Run(t, plain, "rev-parse", "HEAD"))
	write(t, filepath.Join(plain, "fix.txt"), "the fix")
	gittest.Run(t, plain, "add", "fix.txt")
	gittest.Run(t, plain, "commit", "-q", "-m", "Fix the interaction")
	gittest.Run(t, plain, "checkout", "-q", "main")
	if err := worker.RecordIntegration(plain, worker.Integration{Name: "a", Branch: "integration/r55-a", Round: 55, Base: "main",
		Start: start, Assembled: merged, RotaMerges: []string{merged}}); err != nil {
		t.Fatal(err)
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
}
