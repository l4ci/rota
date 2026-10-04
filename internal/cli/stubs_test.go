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

// contractPaths reads the verb paths out of the contract, the same way the
// generated list was made: "### rota <path>" headings, argument and flag words
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

func TestContractVerbsMatchTheContract(t *testing.T) {
	want := contractPaths(t)
	if strings.Join(want, "\n") != strings.Join(contractVerbs, "\n") {
		var b strings.Builder
		for _, p := range want {
			b.WriteString("\t\"" + p + "\",\n")
		}
		t.Fatalf("contractVerbs (internal/cli/contract_verbs.go) drifted from the contract; replace its entries with:\n%s", b.String())
	}
}

func stubRun(args ...string) (int, string, string) {
	var so, se bytes.Buffer
	code := Main(args, strings.NewReader(""), &so, &se)
	return code, so.String(), se.String()
}

// withFakeStub adds `zz` and `zz stub` to the contract verbs for one test. Every
// real contract verb is ported, so the stub machinery would otherwise go
// untested. `zz` is a stub with a stub sub-verb, the shape `init` and `init
// check` had.
func withFakeStub(t *testing.T) {
	t.Helper()
	saved := contractVerbs
	contractVerbs = append(append([]string{}, saved...), "zz", "zz stub")
	t.Cleanup(func() { contractVerbs = saved })
}

// A contract verb the Go binary lacks answers exit 71, whatever follows it,
// not "unknown command" (exit 2).
func TestUnimplementedContractVerbExits71(t *testing.T) {
	withFakeStub(t)
	for _, args := range [][]string{
		{"zz"},
		{"zz", "stub"},
		{"zz", "--no-such-flag", "x"},
		{"zz", "stub", "--repo", "web"},
	} {
		code, out, errOut := stubRun(args...)
		if code != 71 || !strings.Contains(errOut, "is not ported yet") {
			t.Errorf("%v: exit %d stderr %q", args, code, errOut)
		}
		if hasJSON := containsJSON(args); hasJSON != strings.Contains(out, `"code": "not_implemented"`) || (hasJSON && !strings.Contains(out, `"exit": 71`)) {
			t.Errorf("%v: envelope %q", args, out)
		}
	}
	_, _, errOut := stubRun("zz", "stub")
	if !strings.Contains(errOut, "rota zz stub is not ported yet") {
		t.Errorf("the message names the full verb path: %q", errOut)
	}
	// -h still reaches help
	if code, out, _ := stubRun("zz", "stub", "--help"); code != 0 || !strings.Contains(out, "rota zz stub") {
		t.Errorf("help: %d %q", code, out)
	}
	// a typo under an implemented group stays an unknown command
	if code, _, errOut := stubRun("knowledge", "nosuch"); code != 2 || !strings.Contains(errOut, "unknown command") {
		t.Errorf("unknown: %d %q", code, errOut)
	}
}

// `rota __verbs` is what test/hv-hybrid routes by: stubs must stay out of it.
func TestVerbsExcludesStubs(t *testing.T) {
	withFakeStub(t)
	_, out, _ := stubRun("__verbs")
	listed := map[string]bool{}
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		listed[l] = true
	}
	for _, stub := range []string{"zz", "zz stub"} {
		if listed[stub] {
			t.Errorf("stub %q listed by __verbs", stub)
		}
	}
	if !listed["version"] || !listed["knowledge query"] {
		t.Errorf("implemented verbs missing: %v", listed)
	}
	// every listed verb really runs; every other contract verb is a stub. The
	// probe runs in an empty directory: from the package directory a verb such
	// as `block skills` walks up to this repo's .rota/ and rewrites AGENTS.md.
	empty := t.TempDir()
	for _, p := range contractVerbs {
		if listed[p] {
			continue
		}
		if code, _, _ := stubRun(append([]string{"-C", empty}, append(strings.Fields(p), "--json")...)...); code == 2 && !strings.HasPrefix(p, "block ") {
			t.Errorf("contract verb %q is neither implemented nor a stub (exit 2)", p)
		}
	}
}
