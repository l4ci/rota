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
		if got := knFrozen(t, dir, "", append([]string{"decisions", "query"}, topics...)...); got.RC != 0 {
			t.Errorf("%v: rc %d", topics, got.RC)
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
		if got := knFrozen(t, dir, "", append([]string{group, "query"}, names...)...); got.RC != 0 {
			t.Errorf("%s: rc %d", group, got.RC)
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
			if got := knFrozen(t, dir, "", c.group, "index"); got.RC != 0 {
				t.Fatalf("%s rc %d %s", c.group, got.RC, got.Stderr)
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
	got := knFrozenView(t, dir, "", func(o *knFrozenOut) { o.Stdout, o.Stderr = "", o.Stdout }, "map", "stats", "--cap")
	if !strings.HasPrefix(got.Stdout, "note: project map has 2 subsystems") {
		t.Errorf("text mode: %q", got.Stdout)
	}
}

func TestCRLFMatchGoldenForDecisionsMapAndQA(t *testing.T) {
	crlf := func(s string) string { return strings.ReplaceAll(s, "\n", "\r\n") }
	t.Run("decisions query", func(t *testing.T) {
		dir := decProject(t, crlf(decFixture), "")
		knFrozen(t, dir, "", "decisions", "query", "build")
	})
	t.Run("map query, stats and index", func(t *testing.T) {
		dir := mapProject(t)
		knWrite(t, filepath.Join(dir, ".rota", "map", "cli.md"), crlf(mapFileA))
		knWrite(t, filepath.Join(dir, "cmd", "main.go"), crlf("package main\n\nfunc main() {}\n"))
		if got := knFrozen(t, dir, "", "map", "query", "cli"); strings.Contains(got.Stdout, "\r") {
			t.Errorf("query kept CR: %q", got.Stdout)
		}
		n := knNew(t, dir, "", "map", "stats", "--json")
		if !strings.Contains(n.stdout, `"entryPoints": 3, "brokenRefs": 2`) {
			t.Errorf("new: %s", n.stdout)
		}
	})
}
