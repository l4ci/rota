package harness

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Nothing outside this package may switch on a worker kind (#584): a kind's
// behaviour is a Harness method, so a new kind is one file here. The guard
// greps every Go file of the repo, tests included, for a kind compared against
// the literal "codex" or "claude"; production code may not compare against the
// Codex or Claude constants either.
var (
	litCompare   = regexp.MustCompile(`(==|!=)\s*"(codex|claude)"|"(codex|claude)"\s*(==|!=)|\bcase\b[^:]*"(codex|claude)"`)
	constCompare = regexp.MustCompile(`(==|!=)\s*harness\.(Codex|Claude)\b|\bharness\.(Codex|Claude)\s*(==|!=)|\bcase\b[^:]*\bharness\.(Codex|Claude)\b`)
)

// notKinds are files whose "codex" or "claude" is not a worker kind.
var notKinds = map[string]string{
	"codex_plugin_test.go":             "the codex git ref of the plugin marketplace",
	"internal/cli/skills.go":           "the skill install target of --agent, not a worker kind",
	"internal/cli/skills_test.go":      "the skill install target of --agent, not a worker kind",
	"internal/verdict/route.go":        "the retired ship.secondOpinionRunner value",
	"internal/verdict/verdict.go":      "the retired ship.secondOpinionRunner value",
	"internal/cli/orchestrate_test.go": "the binary the orchestrator launcher runs, not a worker kind",
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// kindSwitches returns "file:line: text" for each line of a Go file under root
// that compares a kind by name, outside internal/harness.
func kindSwitches(t *testing.T, root string) []string {
	t.Helper()
	var hits []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			switch rel {
			case ".git", ".worktrees", "internal/harness", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(rel, ".go") {
			return nil
		}
		if _, skip := notKinds[rel]; skip {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		prod := !strings.HasSuffix(rel, "_test.go")
		for i, line := range strings.Split(string(b), "\n") {
			code := line
			if j := strings.Index(code, "//"); j >= 0 && !strings.Contains(code[:j], `"`) {
				code = code[:j]
			}
			if litCompare.MatchString(code) || (prod && constCompare.MatchString(code)) {
				hits = append(hits, rel+":"+itoa(i+1)+": "+strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return hits
}

func itoa(n int) string { return fmtInt(n) }

func fmtInt(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for ; n > 0; n /= 10 {
		d = append([]byte{byte('0' + n%10)}, d...)
	}
	return string(d)
}

func TestNoKindSwitchOutsideHarness(t *testing.T) {
	if hits := kindSwitches(t, repoRoot(t)); len(hits) > 0 {
		t.Fatalf("a worker kind is compared by name outside internal/harness; move it behind a Harness method:\n%s", strings.Join(hits, "\n"))
	}
}

// The guard must fail on a hit: copy a clean tree shape, plant one comparison
// of each form, and expect each to be found.
func TestKindSwitchGuardFindsAPlantedHit(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	src := "package x\n\nfunc f(kind string) bool { return kind == \"codex\" }\nfunc g(k string) bool { switch k { case \"claude\": return true }; return k == harness.Codex }\n"
	if err := os.WriteFile(filepath.Join(root, "internal", "x", "x.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if hits := kindSwitches(t, root); len(hits) != 2 {
		t.Fatalf("planted literal and constant comparison: want 2 hits, got %v", hits)
	}
}
