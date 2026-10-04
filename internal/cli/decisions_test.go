package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const decFixture = `# Decisions

## Architecture

### Keep modules small

*Why.* Reviews stay short.

**Forbids.**
- god packages

**Permits.**
- many small ones

<!-- [Auto:Loop] plan-1 2026-09-30 — review and articulate Forbids/Permits -->

## Build

### Gate first

*Why.* Cheap.

**Forbids.**
- _(Unresolved — user must articulate)_

**Permits.**
- tests

<!-- [Auto:Loop] plan-2 2026-10-02 — review and articulate Forbids/Permits -->

### Never skip

*Why.* Safety.

**Forbids.**
- _(Unresolved — user must articulate)_

**Permits.**
- _(Unresolved — user must articulate)_

<!-- [Auto:Loop] plan-3 2026-10-03 — review and articulate Forbids/Permits -->
`

func decProject(t *testing.T, decisions, status string) string {
	dir := knProject(t, false)
	if decisions != "" {
		knWrite(t, filepath.Join(dir, ".rota", "DECISIONS.md"), decisions)
	}
	if status != "" {
		knWrite(t, filepath.Join(dir, ".rota", "status.json"), status)
	}
	return dir
}

func TestDecisionsQueryMatchGolden(t *testing.T) {
	dir := decProject(t, decFixture, "")
	for _, topics := range [][]string{{"build"}, {"Build", "architecture"}, {"nothing"}} {
		want, got := knFrozen(t, dir, "", append([]string{"decisions", "query"}, topics...)...)
		if want.Stdout != got.Stdout || got.RC != 0 {
			t.Errorf("%v\n--- frozen ---\n%s\n--- new ---\n%s", topics, want.Stdout, got.Stdout)
		}
	}
	j := knNew(t, dir, "", "decisions", "query", "Build", "ghost", "--json")
	if !strings.Contains(j.stdout, `"missing": ["ghost"]`) {
		t.Errorf("missing not reported: %s", j.stdout)
	}
	if got := knNew(t, dir, "", "decisions", "query"); got.rc != 2 {
		t.Errorf("no topic: rc=%d", got.rc)
	}
	if got := knNew(t, dir, "", "decisions", "query", "--repo", "web", "Build"); got.rc != 2 {
		t.Errorf("--repo accepted: rc=%d", got.rc)
	}
}

func TestDecisionsAutoLogMatchGolden(t *testing.T) {
	cases := []struct {
		name         string
		decisions    string
		newArgs      []string
		wantMarker   string
		wantUnchange bool
	}{
		{"new entry in existing topic", decFixture,
			[]string{"decisions", "auto-log", "--topic", "Build", "--title", "Fresh rule", "--why", "because", "--plan-key", "plan-9", "--date", "2026-10-05"}, "### Fresh rule", false},
		{"new topic", decFixture,
			[]string{"decisions", "auto-log", "--topic", "Release", "--title", "Tag first", "--why", "why not", "--date", "2026-10-05"}, "## Release", false},
		{"repeat is a no-op", decFixture,
			[]string{"decisions", "auto-log", "--topic", "Build", "--title", "Gate first", "--why", "again", "--date", "2026-10-05"}, "", true},
		{"file does not exist yet", "",
			[]string{"decisions", "auto-log", "--topic", "Build", "--title", "First", "--why", "w", "--date", "2026-10-05"}, "### First", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := decProject(t, c.decisions, "")
			want, got := knFrozen(t, dir, "", c.newArgs...)
			if want.RC != 0 || got.RC != 0 {
				t.Fatalf("rc frozen=%d new=%d %s %s", want.RC, got.RC, want.Stderr, got.Stderr)
			}
			knSameDelta(t, want, got)
			j := knNew(t, dir, "", append(c.newArgs, "--json")...)
			if !strings.Contains(j.stdout, `"changed": false`) {
				t.Errorf("second call not idempotent: %s", j.stdout)
			}
		})
	}
	dir := decProject(t, decFixture, "")
	if got := knNew(t, dir, "", "decisions", "auto-log", "--topic", "Build", "--title", "t"); got.rc != 2 {
		t.Errorf("missing --why: rc=%d", got.rc)
	}
}

func TestDecisionsAutoSince(t *testing.T) {
	status := `{"loopStartedAt": "2026-10-01T09:00:00Z"}`
	dir := decProject(t, decFixture, status)
	n := knNew(t, dir, "", "decisions", "auto-since", "--json")
	want := `"since": "2026-10-01T09:00:00Z", "decisions": [` +
		`{"topic": "Build", "title": "Gate first", "date": "2026-10-02", "status": "partial"}, ` +
		`{"topic": "Build", "title": "Never skip", "date": "2026-10-03", "status": "unresolved"}]`
	if !strings.Contains(n.stdout, want) {
		t.Errorf("got %s\nwant to contain %s", n.stdout, want)
	}
	// Text output and the old helper agree on the entries it can see.
	frozen, text := knFrozen(t, dir, "", "decisions", "auto-since")
	if frozen.Stdout != text.Stdout {
		t.Errorf("text differs\nfrozen: %q\nnew: %q", frozen.Stdout, text.Stdout)
	}
	// No loop, no file: empty.
	for _, d := range []string{decProject(t, decFixture, ""), decProject(t, "", status)} {
		j := knNew(t, d, "", "decisions", "auto-since", "--json")
		if !strings.Contains(j.stdout, `"decisions": []`) || j.rc != 0 {
			t.Errorf("expected none: rc=%d %s", j.rc, j.stdout)
		}
	}
}

// An auto-logged entry with no plan key has a footer the old helper's
// auto-since pattern (and so this port) does not match.
func TestDecisionsAutoSinceIgnoresFootersWithoutPlanKey(t *testing.T) {
	dir := decProject(t, "", `{"loopStartedAt": "2026-10-01T00:00:00Z"}`)
	knNew(t, dir, "", "decisions", "auto-log", "--topic", "T", "--title", "No key", "--why", "w", "--date", "2026-10-02")
	knNew(t, dir, "", "decisions", "auto-log", "--topic", "T", "--title", "With key", "--why", "w", "--plan-key", "p", "--date", "2026-10-02")
	n := knNew(t, dir, "", "decisions", "auto-since")
	if !strings.Contains(n.stdout, "With key") || strings.Contains(n.stdout, "No key") {
		t.Errorf("got %q", n.stdout)
	}
	// The frozen helper run on the same tree printed the same.
	if want, _ := knFrozen(t, dir, "", "decisions", "auto-since"); want.Stdout != n.stdout {
		t.Errorf("frozen=%q new=%q", want.Stdout, n.stdout)
	}
}

const mapFileA = "---\nsubsystem: cli\nsummary: The command line\ntouched: 2026-09-01\n---\n\n# CLI\n\ntext\n\n## Entry points\n\n- cmd/main.go:2\n- cmd/main.go:99\n- gone/file.go:1\n"
const mapFileB = "---\nsubsystem: alpha\n---\n\n# Alpha\n"

func mapProject(t *testing.T) string {
	dir := knProject(t, false)
	knWrite(t, filepath.Join(dir, ".rota", "map", "cli.md"), mapFileA)
	knWrite(t, filepath.Join(dir, ".rota", "map", "alpha.md"), mapFileB)
	knWrite(t, filepath.Join(dir, ".rota", "map", "nofm.md"), "no frontmatter\n")
	knWrite(t, filepath.Join(dir, "cmd", "main.go"), "package main\n\nfunc main() {}\n")
	knWrite(t, filepath.Join(dir, "AGENTS.md"), "# Agents\n")
	return dir
}

func TestMapQueryAndQAQueryMatchGolden(t *testing.T) {
	dir := mapProject(t)
	knWrite(t, filepath.Join(dir, ".rota", "qa", "web.md"), "---\nsurface: web\nsummary: browser\n---\n\n# Web QA\n")
	for _, group := range []string{"map", "qa"} {
		names := []string{"cli", "ghost", "alpha", "web"}
		want, got := knFrozen(t, dir, "", append([]string{group, "query"}, names...)...)
		if want.Stdout != got.Stdout || got.RC != 0 {
			t.Errorf("%s\n--- frozen ---\n%s\n--- new ---\n%s", group, want.Stdout, got.Stdout)
		}
		if got := knNew(t, dir, "", group, "query"); got.rc != 2 {
			t.Errorf("%s with no name: rc=%d", group, got.rc)
		}
	}
	j := knNew(t, dir, "", "map", "query", "ghost", "../x", "--json")
	if !strings.Contains(j.stdout, `"missing": ["ghost", "../x"]`) {
		t.Errorf("missing: %s", j.stdout)
	}
}

// The goldens carry the old helpers' blocks with the A9 G4 text: the pointer
// names the rota verb. Everything else is the frozen output, byte for byte.
func TestMapAndQAIndexMatchGolden(t *testing.T) {
	for _, c := range []struct{ group string }{{"map"}, {"qa"}} {
		for _, withEntries := range []bool{true, false} {
			dir := mapProject(t)
			if !withEntries {
				os.RemoveAll(filepath.Join(dir, ".rota", "map"))
			} else {
				knWrite(t, filepath.Join(dir, ".rota", "qa", "web.md"), "---\nsurface: web\nsummary: browser\n---\n")
				knWrite(t, filepath.Join(dir, ".rota", "qa", "bare.md"), "---\n---\n")
			}
			want, got := knFrozen(t, dir, "", c.group, "index")
			if want.RC != 0 || got.RC != 0 {
				t.Fatalf("%s rc frozen=%d new=%d %s %s", c.group, want.RC, got.RC, want.Stderr, got.Stderr)
			}
			if want.Changed["../AGENTS.md"] != got.Changed["../AGENTS.md"] {
				t.Errorf("%s (entries=%v) AGENTS.md differs\n--- frozen ---\n%s\n--- new ---\n%s", c.group, withEntries, want.Changed["../AGENTS.md"], got.Changed["../AGENTS.md"])
			}
			again := knNew(t, dir, "", c.group, "index", "--json")
			if !strings.Contains(again.stdout, `"status": "unchanged", "changed": false`) {
				t.Errorf("%s second run: %s", c.group, again.stdout)
			}
		}
	}
}

func TestMapStats(t *testing.T) {
	dir := mapProject(t)
	n := knNew(t, dir, "", "map", "stats", "--json")
	for _, want := range []string{`"name": "alpha"`, `"name": "cli"`, `"entryPoints": 3`, `"brokenRefs": 2`, `"touched": "2026-09-01"`, `"count": 2`} {
		if !strings.Contains(n.stdout, want) {
			t.Errorf("missing %s: %s", want, n.stdout)
		}
	}
	if strings.Index(n.stdout, `"alpha"`) > strings.Index(n.stdout, `"cli"`) {
		t.Errorf("not sorted by name: %s", n.stdout)
	}
	// No map directory: empty, exit 0.
	empty := knProject(t, false)
	if e := knNew(t, empty, "", "map", "stats", "--json"); !strings.Contains(e.stdout, `"subsystems": [], "count": 0`) || e.rc != 0 {
		t.Errorf("empty: rc=%d %s", e.rc, e.stdout)
	}
}

func TestMapStatsCap(t *testing.T) {
	dir := mapProject(t)
	below := knNew(t, dir, "", "map", "stats", "--cap", "--json")
	if !strings.Contains(below.stdout, `"cap": 20, "overCap": false`) || strings.Contains(below.stderr, "note") {
		t.Errorf("below cap: %s / %s", below.stdout, below.stderr)
	}
	knWrite(t, filepath.Join(dir, ".rota", "config.json"), `{"map": {"softcap_subsystems": 2}}`)
	over := knNew(t, dir, "", "map", "stats", "--cap", "--json")
	if !strings.Contains(over.stdout, `"cap": 2, "overCap": true`) || !strings.Contains(over.stdout, `"warnings": ["project map has 2 subsystems (cap 2);`) {
		t.Errorf("over cap: %s", over.stdout)
	}
	// The old nudge text matches: the helper printed it on stderr, rota prints it on stdout.
	want, got := knFrozen(t, dir, "", "map", "stats", "--cap")
	if want.Stderr != got.Stdout || !strings.HasPrefix(got.Stdout, "note: project map has 2 subsystems") {
		t.Errorf("text mode: frozen %q new %q", want.Stderr, got.Stdout)
	}
}

func TestCRLFMatchGoldenForDecisionsMapAndQA(t *testing.T) {
	crlf := func(s string) string { return strings.ReplaceAll(s, "\n", "\r\n") }
	t.Run("auto-log rewrites as LF", func(t *testing.T) {
		dir := decProject(t, crlf(decFixture), "")
		want, got := knFrozen(t, dir, "", "decisions", "auto-log", "--topic", "Build", "--title", "New rule", "--why", "why", "--date", "2026-10-05")
		knSameDelta(t, want, got)
		if strings.Contains(knTree(t, dir)["DECISIONS.md"], "\r") {
			t.Error("CR survived")
		}
	})
	t.Run("decisions query and auto-since", func(t *testing.T) {
		dir := decProject(t, crlf(decFixture), `{"loopStartedAt": "2026-10-01T09:00:00Z"}`)
		if want, got := knFrozen(t, dir, "", "decisions", "query", "build"); want.Stdout != got.Stdout {
			t.Errorf("query frozen %q new %q", want.Stdout, got.Stdout)
		}
		if want, got := knFrozen(t, dir, "", "decisions", "auto-since"); want.Stdout != got.Stdout {
			t.Errorf("since frozen %q new %q", want.Stdout, got.Stdout)
		}
	})
	t.Run("map query, stats and index", func(t *testing.T) {
		dir := mapProject(t)
		knWrite(t, filepath.Join(dir, ".rota", "map", "cli.md"), crlf(mapFileA))
		knWrite(t, filepath.Join(dir, "cmd", "main.go"), crlf("package main\n\nfunc main() {}\n"))
		if want, got := knFrozen(t, dir, "", "map", "query", "cli"); want.Stdout != got.Stdout || strings.Contains(got.Stdout, "\r") {
			t.Errorf("query frozen %q new %q", want.Stdout, got.Stdout)
		}
		n := knNew(t, dir, "", "map", "stats", "--json")
		if !strings.Contains(n.stdout, `"entryPoints": 3, "brokenRefs": 2`) {
			t.Errorf("new: %s", n.stdout)
		}
	})
}
