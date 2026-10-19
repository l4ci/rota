package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestKnowledgeAddStoresShaBesideDate(t *testing.T) {
	dir := knProject(t, false)
	got := knNew(t, dir, "Use the shim.\n", "knowledge", "add", "--topic", "Build", "--title", "Sha tip",
		"--date", "2026-05-05", "--sha", "ABC1234", "--body-file", "-")
	if got.rc != 0 {
		t.Fatalf("rc = %d: %s", got.rc, got.stderr)
	}
	want := "- **Sha tip** — Use the shim. <!-- 2026-05-05 sha:abc1234 -->"
	if k := knTree(t, dir)["KNOWLEDGE.md"]; !strings.Contains(k, want) {
		t.Errorf("KNOWLEDGE.md lacks %q:\n%s", want, k)
	}
}

func TestKnowledgeAddRejectsMalformedSha(t *testing.T) {
	for _, sha := range []string{"xyz", "abc12", "HEAD", "abc1234 -->"} {
		dir := knProject(t, false)
		got := knNew(t, dir, "b\n", "knowledge", "add", "--topic", "Build", "--title", "Bad",
			"--sha", sha, "--body-file", "-")
		if got.rc != 2 {
			t.Errorf("sha %q: rc = %d, want 2 (%s)", sha, got.rc, got.stderr)
		}
	}
}

func TestKnowledgeQueryPrintsShaAndKeepsTierRules(t *testing.T) {
	dir := knProject(t, false)
	knWrite(t, filepath.Join(dir, ".rota", "KNOWLEDGE.md"), `# Knowledge

## Build

- **Pinned** — claims a flag. <!-- 2026-05-05 sha:abc1234 -->
- **Old rule** — retired. <!-- 2026-05-06 sha:def5678 -->
- **Legacy** — no sha. <!-- 2026-01-01 -->
`)
	knWrite(t, filepath.Join(dir, ".rota", "knowledge-tier.json"), `{
  "version": 1,
  "entries": {
    "Build::Pinned": {"tier": "provisional", "hits": 0, "lastSeen": "2026-05-05"},
    "Build::Old rule": {"tier": "deprecated", "hits": 0, "lastSeen": "2026-05-06"}
  }
}
`)
	got := knNew(t, dir, "", "knowledge", "query", "Build")
	if got.rc != 0 {
		t.Fatalf("rc = %d: %s", got.rc, got.stderr)
	}
	for _, want := range []string{
		"- **Pinned** — claims a flag. (provisional) <!-- 2026-05-05 sha:abc1234 -->",
		"- **Legacy** — no sha. <!-- 2026-01-01 -->",
	} {
		if !strings.Contains(got.stdout, want) {
			t.Errorf("output lacks %q:\n%s", want, got.stdout)
		}
	}
	if strings.Contains(got.stdout, "Old rule") {
		t.Errorf("deprecated sha entry not hidden:\n%s", got.stdout)
	}
}
