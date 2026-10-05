package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// contractPaths reads the verb paths out of the contract:
// "### rota <path>" headings, argument and flag words
// dropped.
func contractPaths(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("..", "..", "docs", "design", "contract", "*.md"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no contract files: %v", err)
	}
	var b []byte
	for _, f := range files {
		part, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		b = append(b, part...)
	}
	re := regexp.MustCompile(`(?m)^### rota (.*)$`)
	seen := map[string]bool{}
	var out []string
	for _, m := range re.FindAllStringSubmatch(string(b), -1) {
		var w []string
		for _, tok := range strings.Fields(m[1]) {
			if strings.ContainsAny(tok[:1], "<[-") {
				break
			}
			w = append(w, tok)
		}
		if p := strings.Join(w, " "); p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// Every verb the contract documents resolves to a real verb in the tree. A
// path is implemented when the walk reaches a verb before running out of words
// (`block skills` is `block` with a key), or ends on a node that is a verb.
func TestTreeImplementsEveryContractVerb(t *testing.T) {
	root := Tree()
	for _, path := range contractPaths(t) {
		cmd := root
		for i, w := range strings.Fields(path) {
			next := cmd.sub(w)
			if next == nil {
				if cmd.Verb == nil || i == 0 {
					t.Errorf("contract verb %q has no verb in the tree (add it, or fix the contract heading)", path)
				}
				cmd = nil
				break
			}
			cmd = next
			if cmd.Verb != nil && i < len(strings.Fields(path))-1 && cmd.sub(strings.Fields(path)[i+1]) == nil {
				break // extra words are the verb's positional arguments
			}
		}
		if cmd != nil && cmd.Verb == nil {
			t.Errorf("contract verb %q is a group in the tree, not a verb", path)
		}
	}
}

func runMain(args ...string) (int, string, string) {
	return runMainWith(testDeps(), args...)
}

func runMainWith(d *Deps, args ...string) (int, string, string) {
	var so, se bytes.Buffer
	code := mainWith(d, args, strings.NewReader(""), &so, &se)
	return code, so.String(), se.String()
}
