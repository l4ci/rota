package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestKnowledgeAddPathsRoundTrip(t *testing.T) {
	dir := knProject(t, false)
	code, _, errOut := rotaStdin(t, dir, "Use a private helper.", "knowledge", "add", "--topic", "Architecture", "--title", "Scoped", "--body-file", "-", "--path", "internal/**/*.go", "--path", "cmd/*.go")
	if code != 0 {
		t.Fatalf("path-scoped add failed: %d %s", code, errOut)
	}
	code, out := textRun(t, "-C", dir, "knowledge", "query", "Architecture")
	if code != 0 || !strings.Contains(out, `<!-- paths: ["internal/**/*.go","cmd/*.go"] -->`) || !strings.Contains(out, "(provisional)") {
		t.Fatalf("query lost paths or tier: %d %s", code, out)
	}
	code, _, errOut = rotaStdin(t, dir, "Bad", "knowledge", "add", "--topic", "Architecture", "--title", "Bad", "--body-file", "-", "--path", "[")
	if code != 2 || !strings.Contains(errOut, "glob") {
		t.Fatalf("invalid glob: %d %s", code, errOut)
	}
	// The decision write flow remains /rota-decide; its existing query retains
	// the same field and the complete boundary without interpreting the scope.
	knWrite(t, filepath.Join(dir, ".rota", "DECISIONS.md"), "# Decisions\n## Architecture\n### Scoped\nRule.\n<!-- paths: [\"internal/**/*.go\"] -->\n**Forbids.** Public state.\n")
	code, out = textRun(t, "-C", dir, "decisions", "query", "Architecture")
	if code != 0 || !strings.Contains(out, `<!-- paths: ["internal/**/*.go"] -->`) || !strings.Contains(out, "Public state") {
		t.Fatalf("decision query: %d %s", code, out)
	}
}
