package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/golden"
)

// The parity tests run a rota verb on a fixture and compare its output and the
// resulting .rota/ tree delta with a golden: what the retired bin/ helper
// produced for the same case (testdata/golden, via knFrozen).

const knFixtureKnowledge = `# Knowledge

## Architecture

- **Alpha rule** — Keep modules small. <!-- 2026-01-01 -->
- **Beta rule** — Prefer files. <!-- 2026-01-02 -->
- **Old rule** — Retired. <!-- 2026-01-03 -->

## Build

- **Gamma tip** — Run the gate. <!-- 2026-02-01 -->

## Glossary

- **Term** — a definition
`

const knFixtureTier = `{
  "version": 1,
  "entries": {
    "Architecture::Alpha rule": {
      "tier": "confirmed",
      "hits": 4,
      "lastSeen": "2026-03-01"
    },
    "Architecture::Beta rule": {
      "tier": "provisional",
      "hits": 1,
      "lastSeen": "2026-03-02"
    },
    "Architecture::Old rule": {
      "tier": "deprecated",
      "hits": 0,
      "lastSeen": "2026-03-03"
    }
  }
}
`

func knWrite(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o666); err != nil {
		t.Fatal(err)
	}
}

// knProject writes a fixture project; umbrella adds two registered sub-repos.
func knProject(t *testing.T, umbrella bool) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	knWrite(t, filepath.Join(dir, ".rota", "KNOWLEDGE.md"), knFixtureKnowledge)
	knWrite(t, filepath.Join(dir, ".rota", "knowledge-tier.json"), knFixtureTier)
	if umbrella {
		for _, n := range []string{"web", "api"} {
			if err := os.MkdirAll(filepath.Join(dir, n), 0o777); err != nil {
				t.Fatal(err)
			}
		}
		knWrite(t, filepath.Join(dir, ".rota", "repos.json"), `{
  "repos": [
    {
      "name": "web",
      "path": "web"
    },
    {
      "name": "api",
      "path": "api"
    }
  ]
}
`)
		knWrite(t, filepath.Join(dir, ".rota", "knowledge", "web", "KNOWLEDGE.md"), "# Web\n\n## Architecture\n\n- **Web rule** — Own the UI. <!-- 2026-04-01 -->\n\n## Glossary\n\n- **Page** — a view\n")
	}
	return dir
}

type knOut struct {
	stdout, stderr string
	rc             int
}

// knNew runs rota in dir.
func knNew(t *testing.T, dir, stdin string, args ...string) knOut {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)
	var so, se bytes.Buffer
	rc := Main(args, strings.NewReader(stdin), &so, &se)
	return knOut{so.String(), se.String(), rc}
}

// knTree reads every regular file under dir/.rota, plus the instructions files
// of the project root and of the fixture sub-repos, into a path → content map.
func knTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	root := filepath.Join(dir, ".rota")
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		out[rel] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, sub := range []string{"", "web", "api"} {
		for _, f := range []string{"AGENTS.md", "CLAUDE.md"} {
			if b, err := os.ReadFile(filepath.Join(dir, sub, f)); err == nil {
				out[filepath.Join("..", sub, f)] = string(b)
			}
		}
	}
	return out
}

// knFrozenOut is what a retired bin/ helper produced for one call, frozen in
// testdata/golden: its streams, exit code and the .rota/ tree delta it left.
type knFrozenOut struct {
	Stdout  string            `json:"stdout"`
	Stderr  string            `json:"stderr"`
	RC      int               `json:"rc"`
	Changed map[string]string `json:"changed"` // path -> content, files added or rewritten
	Removed []string          `json:"removed"`
}

var knLoggedAt = regexp.MustCompile(`"loggedAt": "[^"]*"`)
var migTS = regexp.MustCompile(`migrate-backup/\d{8}T\d{6}`)

// knDelta is the change between two knTree snapshots. Backup directory
// timestamps and loggedAt stamps are normalised, they differ per run.
func knDelta(before, after map[string]string) (changed map[string]string, removed []string) {
	changed, removed = map[string]string{}, []string{}
	for k, v := range after {
		if old, ok := before[k]; !ok || old != v {
			changed[migTS.ReplaceAllString(k, "migrate-backup/TS")] = knLoggedAt.ReplaceAllString(v, `"loggedAt": "TS"`)
		}
	}
	for k := range before {
		if _, ok := after[k]; !ok {
			removed = append(removed, migTS.ReplaceAllString(k, "migrate-backup/TS"))
		}
	}
	sort.Strings(removed)
	return changed, removed
}

// knFrozenInputs names one case: the rota argv, its stdin and a digest of the
// fixture tree it starts from, so a changed case fails against the golden.
func knFrozenInputs(before map[string]string, stdin string, args []string) map[string]any {
	norm := map[string]string{}
	for k, v := range before {
		norm[k] = knLoggedAt.ReplaceAllString(v, `"loggedAt": "TS"`)
	}
	raw, _ := json.Marshal(norm)
	sum := sha256.Sum256(raw)
	return map[string]any{"argv": args, "stdin": stdin, "fixture": hex.EncodeToString(sum[:])}
}

// knFrozen runs rota in dir like knNew and returns what the retired helper
// produced for the same case (want, read from the golden) next to what rota did
// (got, with its tree delta). Call it once per recorded case, in order.
func knFrozen(t *testing.T, dir, stdin string, args ...string) (want, got knFrozenOut) {
	t.Helper()
	before := knTree(t, dir)
	golden.Golden(t, knFrozenInputs(before, stdin, args), &want)
	n := knNew(t, dir, stdin, args...)
	got = knFrozenOut{Stdout: n.stdout, Stderr: n.stderr, RC: n.rc}
	got.Changed, got.Removed = knDelta(before, knTree(t, dir))
	return want, got
}

// knSameDelta reports where rota's tree delta differs from the frozen one.
func knSameDelta(t *testing.T, want, got knFrozenOut) {
	t.Helper()
	var names []string
	seen := map[string]bool{}
	for _, m := range []map[string]string{want.Changed, got.Changed} {
		for k := range m {
			if !seen[k] {
				seen[k] = true
				names = append(names, k)
			}
		}
	}
	sort.Strings(names)
	for _, k := range names {
		w, wok := want.Changed[k]
		g, gok := got.Changed[k]
		switch {
		case wok != gok:
			t.Errorf(".rota/%s written: frozen=%v rota=%v", k, wok, gok)
		case w != g:
			t.Errorf(".rota/%s differs\n--- frozen ---\n%s\n--- rota ---\n%s", k, w, g)
		}
	}
	if strings.Join(want.Removed, "\n") != strings.Join(got.Removed, "\n") {
		t.Errorf("removed files: frozen=%v rota=%v", want.Removed, got.Removed)
	}
}

// knStep is one parity case: the rota call whose result is compared with the
// frozen helper call on the same fixture.
type knStep struct {
	name     string
	newArgs  []string
	stdin    string
	umbrella bool
	wantRC   int // rota exit code
	oldRC    int // the helper's exit code, frozen in the golden
}

func TestKnowledgeWritesMatchGolden(t *testing.T) {
	steps := []knStep{
		{name: "add new bullet",
			newArgs: []string{"knowledge", "add", "--topic", "Build", "--title", "Delta tip", "--date", "2026-05-05", "--body-file", "-"},
			stdin:   "Use the shim.\n"},
		{name: "add duplicate title is a no-op",
			newArgs: []string{"knowledge", "add", "--topic", "Architecture", "--title", "alpha RULE", "--body-file", "-"},
			stdin:   "x"},
		{name: "add to sub-repo scope", umbrella: true,
			newArgs: []string{"knowledge", "add", "--repo", "web", "--topic", "Architecture", "--title", "Web two", "--date", "2026-05-06", "--body-file", "-"},
			stdin:   "More UI."},
		{name: "add under missing topic",
			newArgs: []string{"knowledge", "add", "--topic", "Nope", "--title", "t", "--body-file", "-"},
			stdin:   "b", oldRC: 1, wantRC: 3},
		{name: "amend",
			newArgs: []string{"knowledge", "amend", "--topic", "Architecture", "--fragment", "Beta", "--mode", "append", "--body-file", "-"},
			stdin:   "(see #12)\n"},
		{name: "amend in sub-repo", umbrella: true,
			newArgs: []string{"knowledge", "amend", "--repo", "web", "--topic", "Architecture", "--fragment", "Web rule", "--mode", "append", "--body-file", "-"},
			stdin:   "extra"},
		{name: "amend finds nothing",
			newArgs: []string{"knowledge", "amend", "--topic", "Architecture", "--fragment", "zzz", "--mode", "append", "--body-file", "-"},
			stdin:   "q", oldRC: 1, wantRC: 3},
		{name: "rename whole topic",
			newArgs: []string{"knowledge", "rename-topic", "--from", "Architecture", "--to", "Design"}},
		{name: "rename onto existing topic",
			newArgs: []string{"knowledge", "rename-topic", "--from", "Architecture", "--to", "Build"}, oldRC: 1, wantRC: 4},
		{name: "move one bullet",
			newArgs: []string{"knowledge", "rename-topic", "--from", "Architecture", "--to", "Build", "--title", "Beta rule"}},
		{name: "move one bullet backwards",
			newArgs: []string{"knowledge", "rename-topic", "--from", "Build", "--to", "Architecture", "--title", "Gamma tip"}},
		{name: "tier set",
			newArgs: []string{"knowledge", "tier", "set", "--topic", "Architecture", "--title", "Beta rule", "--tier", "confirmed"}},
		{name: "tier set untracked",
			newArgs: []string{"knowledge", "tier", "set", "--topic", "Build", "--title", "Gamma tip", "--tier", "deprecated"}},
		{name: "hit increments",
			newArgs: []string{"knowledge", "hit", "--topic", "Architecture", "--title", "Beta rule"}},
		{name: "hit creates untracked",
			newArgs: []string{"knowledge", "hit", "--topic", "Build", "--title", "Gamma tip"}},
		{name: "contradiction add",
			newArgs: []string{"knowledge", "contradiction", "add", "--topic", "Architecture", "--title", "Beta rule", "--text", "no, use dirs"}},
		{name: "contradiction clear",
			newArgs: []string{"knowledge", "contradiction", "clear"}},
	}
	for _, s := range steps {
		t.Run(s.name, func(t *testing.T) {
			want, got := knFrozen(t, knProject(t, s.umbrella), s.stdin, s.newArgs...)
			if want.RC != s.oldRC {
				t.Fatalf("frozen helper rc = %d, want %d; stderr: %s", want.RC, s.oldRC, want.Stderr)
			}
			if got.RC != s.wantRC {
				t.Fatalf("rota rc = %d, want %d; stderr: %s", got.RC, s.wantRC, got.Stderr)
			}
			knSameDelta(t, want, got)
		})
	}
}

// TestKnowledgeSequenceMatchGolden chains calls so state accumulates,
// including auto-promotion at the hit threshold.
func TestKnowledgeSequenceMatchGolden(t *testing.T) {
	dir := knProject(t, false)
	for i := 0; i < 3; i++ {
		want, got := knFrozen(t, dir, "", "knowledge", "hit", "--topic", "Architecture", "--title", "Beta rule")
		knSameDelta(t, want, got)
	}
	if tier := knTree(t, dir)["knowledge-tier.json"]; !strings.Contains(tier, `"tier": "confirmed"`) {
		t.Fatalf("Beta rule not promoted:\n%s", tier)
	}

	// A pending contradiction blocks promotion.
	want, got := knFrozen(t, dir, "", "knowledge", "contradiction", "add", "--topic", "Build", "--title", "Gamma tip", "--text", "c")
	knSameDelta(t, want, got)
	for i := 0; i < 4; i++ {
		want, got := knFrozen(t, dir, "", "knowledge", "hit", "--topic", "Build", "--title", "Gamma tip")
		knSameDelta(t, want, got)
	}
	if tier := knTree(t, dir)["knowledge-tier.json"]; !strings.Contains(tier, `"Build::Gamma tip": {
      "tier": "provisional",
      "hits": 4`) {
		t.Errorf("promotion was not blocked:\n%s", tier)
	}
}

func TestKnowledgeQueryMatchGolden(t *testing.T) {
	cases := []struct {
		name     string
		umbrella bool
		newArgs  []string
	}{
		{"topics in document order", false, []string{"knowledge", "query", "Build", "Architecture"}},
		{"case-insensitive", false, []string{"knowledge", "query", "architecture"}},
		{"include deprecated", false, []string{"knowledge", "query", "--include-deprecated", "Architecture"}},
		{"tier filter", false, []string{"knowledge", "query", "--tier", "confirmed", "Architecture"}},
		{"unmatched topic", false, []string{"knowledge", "query", "Architecture", "Nope"}},
		{"sub-repo hybrid", true, []string{"knowledge", "query", "--repo", "web", "Architecture", "Build"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			want, got := knFrozen(t, knProject(t, c.umbrella), "", c.newArgs...)
			if want.Stdout != got.Stdout {
				t.Errorf("stdout differs\n--- frozen ---\n%s\n--- new ---\n%s", want.Stdout, got.Stdout)
			}
			if want.RC != 0 || got.RC != 0 {
				t.Errorf("rc frozen=%d new=%d", want.RC, got.RC)
			}
			warnOld := strings.Count(want.Stderr, "no topic heading matches")
			warnNew := strings.Count(got.Stderr, "no topic heading matches")
			if warnOld != warnNew {
				t.Errorf("warnings frozen=%d new=%d\n%s", warnOld, warnNew, got.Stderr)
			}
		})
	}
}

func TestKnowledgeStats(t *testing.T) {
	dir := knProject(t, false)
	n := knNew(t, dir, "", "knowledge", "stats", "--json")
	for _, want := range []string{`"name": "Architecture", "bullets": 3`, `"name": "Glossary"`} {
		if !strings.Contains(n.stdout, want) {
			t.Errorf("stats missing %s: %s", want, n.stdout)
		}
	}
}

func TestKnowledgeTierReads(t *testing.T) {
	dir := knProject(t, false)
	n := knNew(t, dir, "", "knowledge", "tier", "list", "--tier", "confirmed", "--json")
	if !strings.Contains(n.stdout, `"title": "Alpha rule"`) || strings.Contains(n.stdout, "Beta") {
		t.Errorf("list: %s", n.stdout)
	}
	g := knNew(t, dir, "", "knowledge", "tier", "get", "--topic", "Architecture", "--title", "Nope", "--json")
	if g.rc != 0 || !strings.Contains(g.stdout, `"found": false`) {
		t.Errorf("get untracked: rc=%d %s", g.rc, g.stdout)
	}
	g = knNew(t, dir, "", "knowledge", "tier", "get", "--topic", "Architecture", "--title", "Alpha rule", "--json")
	if !strings.Contains(g.stdout, `"tier": "confirmed", "hits": 4, "lastSeen": "2026-03-01"`) {
		t.Errorf("get: %s", g.stdout)
	}
}

func TestKnowledgeContractErrors(t *testing.T) {
	dir := knProject(t, false)
	cases := []struct {
		name string
		args []string
		rc   int
	}{
		{"query with no topic", []string{"knowledge", "query"}, 2},
		{"query bad tier", []string{"knowledge", "query", "--tier", "x", "Build"}, 2},
		{"add missing flag", []string{"knowledge", "add", "--topic", "Build", "--body-file", "-"}, 2},
		{"amend bad mode", []string{"knowledge", "amend", "--topic", "Build", "--fragment", "G", "--mode", "replace", "--body-file", "-"}, 2},
		{"tier set bad tier", []string{"knowledge", "tier", "set", "--topic", "Build", "--title", "t", "--tier", "bogus"}, 2},
		{"repo outside umbrella", []string{"knowledge", "query", "--repo", "web", "Build"}, 3},
		{"contradiction has miss", []string{"knowledge", "contradiction", "has", "--topic", "Build", "--title", "t"}, 1},
		{"stats rejects --repo", []string{"knowledge", "stats", "--repo", "web"}, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := knNew(t, dir, "x", c.args...); got.rc != c.rc {
				t.Errorf("rc = %d, want %d; stderr: %s", got.rc, c.rc, got.stderr)
			}
		})
	}
}

func TestKnowledgeHitJSON(t *testing.T) {
	dir := knProject(t, false)
	var last knOut
	for i := 0; i < 2; i++ {
		last = knNew(t, dir, "", "knowledge", "hit", "--topic", "Architecture", "--title", "Beta rule", "--json")
	}
	want := `{"ok": true, "data": {"topic": "Architecture", "title": "Beta rule", "hits": 3, "tier": "confirmed", "promoted": true, "promotionBlocked": false, "changed": true}}`
	if strings.TrimSpace(last.stdout) != want {
		t.Errorf("got  %s\nwant %s", last.stdout, want)
	}
	g := knNew(t, dir, "", "knowledge", "hit", "--topic", "Glossary", "--title", "Term", "--json")
	if !strings.Contains(g.stdout, `"hits": 0, "tier": "provisional", "promoted": false, "promotionBlocked": false, "changed": false`) {
		t.Errorf("glossary hit: %s", g.stdout)
	}
}

// A CRLF KNOWLEDGE.md is read as LF and rewritten as pure LF, like the old
// helpers (Python's read_text normalizes line endings).
func TestKnowledgeCRLFMatchGolden(t *testing.T) {
	crlf := strings.ReplaceAll(knFixtureKnowledge, "\n", "\r\n")
	steps := []struct {
		name    string
		newArgs []string
		stdin   string
	}{
		{"add", []string{"knowledge", "add", "--topic", "Build", "--title", "Delta", "--date", "2026-05-05", "--body-file", "-"}, "b"},
		{"amend", []string{"knowledge", "amend", "--topic", "Architecture", "--fragment", "Beta", "--mode", "append", "--body-file", "-"}, "more"},
		{"rename-topic", []string{"knowledge", "rename-topic", "--from", "Architecture", "--to", "Build", "--title", "Beta rule"}, ""},
	}
	for _, s := range steps {
		t.Run(s.name, func(t *testing.T) {
			dir := knProject(t, false)
			knWrite(t, filepath.Join(dir, ".rota", "KNOWLEDGE.md"), crlf)
			want, got := knFrozen(t, dir, s.stdin, s.newArgs...)
			if want.RC != 0 || got.RC != 0 {
				t.Fatalf("rc frozen=%d new=%d %s %s", want.RC, got.RC, want.Stderr, got.Stderr)
			}
			knSameDelta(t, want, got)
			if strings.Contains(knTree(t, dir)["KNOWLEDGE.md"], "\r") {
				t.Error("CR survived the rewrite")
			}
		})
	}
}

func TestKnowledgeAmendRejectsEmptyBody(t *testing.T) {
	dir := knProject(t, false)
	before := knTree(t, dir)["KNOWLEDGE.md"]
	for _, body := range []string{"", "\n", "  \n\n"} {
		n := knNew(t, dir, body, "knowledge", "amend", "--topic", "Architecture", "--fragment", "Beta", "--mode", "append", "--body-file", "-")
		if n.rc != 2 {
			t.Errorf("body %q: rc=%d", body, n.rc)
		}
	}
	if knTree(t, dir)["KNOWLEDGE.md"] != before {
		t.Error("file changed")
	}
	ok := knNew(t, dir, "x", "knowledge", "amend", "--topic", "Architecture", "--fragment", "Beta", "--mode", "append", "--body-file", "-", "--json")
	if !strings.Contains(ok.stdout, `"changed": true`) {
		t.Errorf("%s", ok.stdout)
	}
}
