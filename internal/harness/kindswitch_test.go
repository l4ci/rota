package harness

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Nothing outside this package names a worker kind (#584): a kind's behaviour
// is a Harness method, so a new kind is one file here. The guard reads every
// Go file of the repo outside internal/harness and reports:
//
//   - production code: the literal "codex" or "claude" anywhere (a comparison,
//     a struct field, a map key, a const alias such as `KindCodex = "codex"`),
//     any use of harness.Codex or harness.Claude, and harness.Default in a
//     comparison (harness.IsDefault is the question to ask);
//   - test code: a kind compared against the literal or listed in a case,
//     one-line or spread over several lines. A test may use harness.Codex and
//     harness.Claude as fixture values.
//
// A constant that holds a kind name is caught where it is declared, so its
// uses need no rule of their own.
var (
	kindLit       = regexp.MustCompile(`"(codex|claude)"`)
	litCompare    = regexp.MustCompile(`(==|!=)\s*"(codex|claude)"|"(codex|claude)"\s*(==|!=)`)
	kindIdent     = regexp.MustCompile(`\bharness\.(Codex|Claude)\b`)
	defaultCompar = regexp.MustCompile(`(==|!=)\s*harness\.Default\b|\bharness\.Default\s*(==|!=)`)
)

// notKinds are the lines whose "codex" or "claude" is not a worker kind: the
// file, a pattern the line must match, and why.
var notKinds = []struct {
	file string
	line *regexp.Regexp
	why  string
}{
	{"codex_plugin_test.go", regexp.MustCompile(`Source\.Ref|"codex"\)`), "the codex git ref of the plugin marketplace"},
	{"internal/cli/skills.go", regexp.MustCompile(`a\.agent`), "the skill install target of --agent"},
	{"internal/cli/skills_test.go", regexp.MustCompile(`agent"\]|\.Agent\b`), "the skill install target of --agent"},
	{"internal/skills/install.go", regexp.MustCompile(`^\s*(Claude|Codex)\s+= "`), "the skill install targets"},
	{"internal/verdict/route.go", regexp.MustCompile(`s\.Runner == "codex"`), "the retired ship.secondOpinionRunner value"},
	{"internal/verdict/verdict.go", regexp.MustCompile(`s\.Runner != "codex"`), "the retired ship.secondOpinionRunner value"},
	{"internal/cli/init.go", regexp.MustCompile(`fs\.Bool\("codex"`), "the removed --codex flag"},
	{"internal/rotastate/rotastate.go", regexp.MustCompile(`func CodexDir`), "the on-disk directory name of the codex slot state"},
	{"internal/config/keys.go", regexp.MustCompile(`k\("(round\.workerKind|orchestrator\.harness|plan\.criticHarness)"`), "enum rows config cannot build from harness (import cycle); TestWorkerKindEnumIsKinds pins them"},
	{"internal/config/schema.go", regexp.MustCompile(`^\s*"(claude|codex)":\s+"`), "the setup prompt's help lines; TestWorkerKindEnumIsKinds checks each kind has one"},
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// kindNames returns "file:line: text" for each line of a Go file under root
// that names a kind outside internal/harness.
func kindNames(t *testing.T, root string) []string {
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
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		prod := !strings.HasSuffix(rel, "_test.go")
		inCase := false // inside a case list that continues on the next line
		for i, line := range strings.Split(string(b), "\n") {
			code := stripComment(line)
			if strings.HasPrefix(strings.TrimSpace(code), "case ") {
				inCase = true
			}
			hit := false
			switch {
			case prod:
				hit = kindLit.MatchString(code) || kindIdent.MatchString(code) || defaultCompar.MatchString(code)
			default:
				hit = litCompare.MatchString(code) || inCase && kindLit.MatchString(code)
			}
			if strings.HasSuffix(strings.TrimSpace(code), ":") {
				inCase = false // the clause ends; a list continues on lines ending in a comma
			}
			if hit && !excused(rel, code) {
				hits = append(hits, fmt.Sprintf("%s:%d: %s", rel, i+1, strings.TrimSpace(line)))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return hits
}

func excused(rel, code string) bool {
	for _, n := range notKinds {
		if n.file == rel && n.line.MatchString(code) {
			return true
		}
	}
	return false
}

func TestNoKindNamedOutsideHarness(t *testing.T) {
	if hits := kindNames(t, repoRoot(t)); len(hits) > 0 {
		t.Fatalf("a worker kind is named outside internal/harness; move it behind a Harness method:\n%s", strings.Join(hits, "\n"))
	}
}

// Every notKinds entry must still excuse a line: one that no longer does is
// dead weight that would hide a future hit.
func TestKindExclusionsStillApply(t *testing.T) {
	root := repoRoot(t)
	for _, n := range notKinds {
		b, err := os.ReadFile(filepath.Join(root, n.file))
		if err != nil {
			t.Errorf("%s: %v", n.file, err)
			continue
		}
		found := false
		for _, line := range strings.Split(string(b), "\n") {
			if n.line.MatchString(line) && (kindLit.MatchString(line) || kindIdent.MatchString(line)) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: no line matches %s (%s); drop the exclusion", n.file, n.line, n.why)
		}
	}
}

// Each shape the guard promises to catch is planted alone, in production code
// or test code, so deleting one branch of the matcher fails exactly here.
func TestKindGuardFindsEachPlantedShape(t *testing.T) {
	prod := map[string]string{
		"comparison":     `func f(k string) bool { return k == "codex" }`,
		"switch case":    "func f(k string) {\n\tswitch k {\n\tcase \"claude\":\n\t}\n}",
		"struct literal": `var v = T{Kind: "codex"}`,
		"map key":        `var m = map[string]int{"codex": 1}`,
		"const alias":    `const KindX = "codex"`,
		"bare constant":  `var v = harness.Codex`,
		"claude const":   `var v = harness.Claude`,
		"default":        `func f(k string) bool { return k != harness.Default }`,
		"multiline case": "func f(k string) {\n\tswitch k {\n\tcase \"a\",\n\t\t\"codex\":\n\t}\n}",
	}
	test := map[string]string{
		"test comparison":     `func f(k string) bool { return k != "claude" }`,
		"test multiline case": "func f(k string) {\n\tswitch k {\n\tcase \"a\",\n\t\t\"codex\":\n\t}\n}",
		"test one-line case":  "func f(k string) {\n\tswitch k {\n\tcase \"a\", \"codex\":\n\t}\n}",
	}
	plant := func(name, file, body string) {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, "internal", "x"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "internal", "x", file), []byte("package x\n\n"+body+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if hits := kindNames(t, root); len(hits) == 0 {
			t.Errorf("%s: the guard missed %q", name, body)
		}
	}
	for name, body := range prod {
		plant(name, "x.go", body)
	}
	for name, body := range test {
		plant(name, "x_test.go", body)
	}
	// What a test may do, and an excused line, stays quiet.
	plant2 := func(file, body string) {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, "internal", "x"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "internal", "x", file), []byte("package x\n\n"+body+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if hits := kindNames(t, root); len(hits) != 0 {
			t.Errorf("false positive on %q: %v", body, hits)
		}
	}
	plant2("x_test.go", `var v = T{Kind: harness.Codex, Account: "codex-home"}`)
	plant2("x.go", `// the codex binary: "codex" is named in a comment only`)
}

// stripComment drops a trailing // comment, a // inside a string kept.
func stripComment(line string) string {
	for i := 0; i+1 < len(line); i++ {
		if line[i] == '/' && line[i+1] == '/' && strings.Count(line[:i], `"`)%2 == 0 && strings.Count(line[:i], "`")%2 == 0 {
			return line[:i]
		}
	}
	return line
}
