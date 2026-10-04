package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const glFixture = `# Knowledge

## Architecture

- **Alpha rule** — Keep modules small. <!-- 2026-01-01 -->

## Glossary

_(no terms yet — use /rota-learn --term)_
`

const glFixtureTerms = `# Knowledge

## Architecture

- **Alpha rule** — Keep modules small. <!-- 2026-01-01 -->

## Glossary

- **Worker** — an agent in a round
  - **Aliases:** agent, slot
  - **Not:** orchestrator
  <!-- 2026-02-02 -->

- **Round** — one cycle of work
  - **Aliases:** _none_
  <!-- 2026-02-03 -->
`

func glProject(t *testing.T, umbrella bool, knowledge string) string {
	dir := knProject(t, umbrella)
	knWrite(t, filepath.Join(dir, ".rota", "KNOWLEDGE.md"), knowledge)
	knWrite(t, filepath.Join(dir, "AGENTS.md"), "# Agents\n\nintro\n")
	if umbrella {
		knWrite(t, filepath.Join(dir, ".rota", "knowledge", "web", "KNOWLEDGE.md"), "# Web\n\n## Architecture\n\n- **Web rule** — UI. <!-- 2026-04-01 -->\n\n## Glossary\n\n_(no terms yet)_\n")
		knWrite(t, filepath.Join(dir, "web", "CLAUDE.md"), "# Web\n")
	}
	return dir
}

func TestGlossaryWriteMatchGolden(t *testing.T) {
	cases := []struct {
		name     string
		fixture  string
		umbrella bool
		newArgs  []string
		oldRC    int // the helper's exit code, frozen in the golden
		wantRC   int
	}{
		{"new term in empty glossary", glFixture, false,
			[]string{"glossary", "write", "Worker", "--def", "an agent", "--alias", "agent, slot", "--not", "orchestrator"}, 0, 0},
		{"second term sorts alphabetically", glFixtureTerms, false,
			[]string{"glossary", "write", "Batch", "--def", "a group"}, 0, 0},
		{"update keeps date and unions aliases", glFixtureTerms, false,
			[]string{"glossary", "write", "worker", "--def", "a new def", "--alias", "Agent,runner"}, 0, 0},
		{"touch restamps the date", glFixtureTerms, false,
			[]string{"glossary", "write", "Round", "--def", "one cycle", "--touch"}, 0, 0},
		{"empty --not clears the list", glFixtureTerms, false,
			[]string{"glossary", "write", "Worker", "--def", "d", "--not", ""}, 0, 0},
		{"omitting --not keeps the list", glFixtureTerms, false,
			[]string{"glossary", "write", "Worker", "--def", "d"}, 0, 0},
		{"alias collision refuses", glFixtureTerms, false,
			[]string{"glossary", "write", "Newbie", "--def", "d", "--alias", "agent"}, 3, 4},
		{"sub-repo scope", glFixture, true,
			[]string{"glossary", "write", "Page", "--def", "a view", "--repo", "web"}, 0, 0},
		{"missing Glossary heading", "# K\n\n## Architecture\n\n- **a** — b <!-- 2026-01-01 -->\n", false,
			[]string{"glossary", "write", "T", "--def", "d"}, 2, 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			want, got := knFrozen(t, glProject(t, c.umbrella, c.fixture), "", c.newArgs...)
			if want.RC != c.oldRC || got.RC != c.wantRC {
				t.Fatalf("rc frozen=%d (want %d) new=%d (want %d)\nfrozen: %s\nnew: %s", want.RC, c.oldRC, got.RC, c.wantRC, want.Stderr, got.Stderr)
			}
			knSameDelta(t, want, got)
		})
	}
}

func TestGlossaryImportMatchGolden(t *testing.T) {
	good := "# comment\nWorker\tan agent\tagent,slot\torchestrator\n\nRound\tone cycle\t\t\nBatch\ta group\tbunch\t\n"
	cases := []struct {
		name, manifest string
		args           []string
		oldRC, wantRC  int // the helper's exit code (frozen) and rota's
	}{
		{"imports a batch", good, nil, 0, 0},
		{"touch", good, []string{"--touch"}, 0, 0},
		{"empty manifest is a no-op", "# only a comment\n", nil, 0, 0},
		{"alias collision inside the batch", "A\td\tx\t\nB\td\tX\t\n", nil, 3, 4},
		{"repeated term", "A\td\t\t\nA\te\t\t\n", nil, 3, 4},
		{"missing definition", "A\t\t\t\n", nil, 2, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			want, got := knFrozen(t, glProject(t, false, glFixtureTerms), c.manifest, append([]string{"glossary", "import", "--body-file", "-"}, c.args...)...)
			if want.RC != c.oldRC || got.RC != c.wantRC {
				t.Fatalf("rc frozen=%d (want %d) new=%d (want %d)\nfrozen: %s\nnew: %s", want.RC, c.oldRC, got.RC, c.wantRC, want.Stderr, got.Stderr)
			}
			knSameDelta(t, want, got)
		})
	}
}

func TestGlossaryReadMatchGolden(t *testing.T) {
	for _, umbrella := range []bool{false, true} {
		dir := glProject(t, umbrella, glFixtureTerms)
		if umbrella {
			knWrite(t, filepath.Join(dir, ".rota", "knowledge", "web", "KNOWLEDGE.md"), "## Glossary\n\n- **Page** — a view\n  - **Aliases:** _none_\n  <!-- 2026-04-01 -->\n\n- **Round** — web round\n  - **Aliases:** _none_\n  <!-- 2026-04-02 -->\n")
		}
		args := []string{"glossary", "read", "worker", "ROUND", "ghost"}
		if umbrella {
			args = append(args, "--repo", "web", "Page")
		}
		want, got := knFrozen(t, dir, "", args...)
		if want.Stdout != got.Stdout || got.RC != 0 {
			t.Errorf("umbrella=%v\n--- frozen ---\n%s\n--- new ---\n%s\nrc=%d %s", umbrella, want.Stdout, got.Stdout, got.RC, got.Stderr)
		}
		if !strings.Contains(got.Stdout, "> from: .rota/KNOWLEDGE.md (## Glossary)") {
			t.Errorf("no provenance line: %s", got.Stdout)
		}
	}
	dir := glProject(t, false, glFixtureTerms)
	j := knNew(t, dir, "", "glossary", "read", "ghost", "--json")
	if !strings.Contains(j.stdout, `"missing": ["ghost"]`) || j.rc != 0 {
		t.Errorf("missing not reported: rc=%d %s", j.rc, j.stdout)
	}
	if got := knNew(t, dir, "", "glossary", "read"); got.rc != 2 {
		t.Errorf("no terms: rc=%d", got.rc)
	}
}

func TestBlockMatchGolden(t *testing.T) {
	cases := []struct {
		name     string
		agents   string
		umbrella bool
		newArgs  []string
		stdin    string
		oldRC    int // the helper's exit code, frozen in the golden
		wantRC   int
	}{
		{"knowledge block appended", "# Agents\n", false, []string{"block", "knowledge"}, "", 0, 0},
		{"decisions block", "# Agents\n", false, []string{"block", "decisions"}, "", 0, 0},
		{"sub-repo knowledge block", "# Agents\n", true, []string{"block", "knowledge", "--repo", "web"}, "", 0, 0},
		{"custom body", "# Agents\n", false, []string{"block", "vision", "--body-file", "-"}, "## Vision\n\nbody\n", 0, 0},
		{"decisions with --repo", "# Agents\n", true, []string{"block", "decisions", "--repo", "web"}, "", 1, 2},
		{"unknown generated key", "# Agents\n", false, []string{"block", "nope"}, "", 1, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := glProject(t, c.umbrella, glFixtureTerms)
			knWrite(t, filepath.Join(dir, "AGENTS.md"), c.agents)
			knWrite(t, filepath.Join(dir, ".rota", "DECISIONS.md"), "# Decisions\n\n## Architecture\n\n### Rule\n")
			want, got := knFrozen(t, dir, c.stdin, c.newArgs...)
			if want.RC != c.oldRC || got.RC != c.wantRC {
				t.Fatalf("rc frozen=%d (want %d) new=%d (want %d)\nfrozen: %s\nnew: %s", want.RC, c.oldRC, got.RC, c.wantRC, want.Stderr, got.Stderr)
			}
			if c.oldRC == 0 && strings.TrimSpace(want.Stdout) != strings.TrimSpace(got.Stdout) {
				t.Errorf("status frozen=%q new=%q", want.Stdout, got.Stdout)
			}
			knSameDelta(t, want, got)
		})
	}
}

func TestBlockIsIdempotent(t *testing.T) {
	dir := glProject(t, false, glFixtureTerms)
	first := knNew(t, dir, "", "block", "knowledge", "--json")
	second := knNew(t, dir, "", "block", "knowledge", "--json")
	if !strings.Contains(first.stdout, `"status": "appended", "changed": true`) || !strings.Contains(second.stdout, `"status": "unchanged", "changed": false`) {
		t.Errorf("first=%s second=%s", first.stdout, second.stdout)
	}
	// A glossary write regenerates the block, and a repeat leaves the tree alone.
	knNew(t, dir, "", "glossary", "write", "Zed", "--def", "last")
	before := knTree(t, dir)
	knNew(t, dir, "", "glossary", "write", "Zed", "--def", "last")
	after := knTree(t, dir)
	for k, v := range before {
		if after[k] != v {
			t.Errorf("%s changed on an identical rewrite", k)
		}
	}
}

func TestBlockSkillsMatchGolden(t *testing.T) {
	dir := glProject(t, false, glFixtureTerms)
	want, got := knFrozen(t, dir, "", "block", "skills")
	if want.RC != 0 || got.RC != 0 {
		t.Fatalf("rc frozen=%d new=%d %s %s", want.RC, got.RC, want.Stderr, got.Stderr)
	}
	// The golden carries the old helper's output with the A9 G4 body, which
	// names rota verbs, and B2's `.rota/verdicts.json` in the gitignored list
	// (#55); everything around the body is the frozen output.
	knSameDelta(t, want, got)
	if got := knNew(t, dir, "x", "block", "skills", "--body-file", "-"); got.rc != 2 {
		t.Errorf("skills with a body: rc=%d", got.rc)
	}
}

func TestInstructionsInitMatchGolden(t *testing.T) {
	blocks := "<!-- rota-knowledge-start -->\nK\n<!-- rota-knowledge-end -->\n\n<!-- rota-decisions-start -->\nD\n<!-- rota-decisions-end -->\n"
	cases := []struct {
		name          string
		claude, agent string // "" means the file does not exist
	}{
		{"nothing exists", "", ""},
		{"only CLAUDE.md with prose", "# Mine\n\nhello\n", ""},
		{"CLAUDE.md with blocks and prose", "# Mine\n\n" + blocks + "\nafter\n", ""},
		{"CLAUDE.md with only blocks", blocks, ""},
		{"AGENTS.md exists, CLAUDE.md does not", "", "# Agents\n"},
		{"AGENTS.md exists, CLAUDE.md lacks the import", "# C\n", "# Agents\n"},
		{"already set up", "# C\n\n@AGENTS.md\n", "# Agents\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := knProject(t, false)
			if c.claude != "" {
				knWrite(t, filepath.Join(dir, "CLAUDE.md"), c.claude)
			}
			if c.agent != "" {
				knWrite(t, filepath.Join(dir, "AGENTS.md"), c.agent)
			}
			want, got := knFrozen(t, dir, "", "instructions", "init")
			if want.RC != 0 || got.RC != 0 {
				t.Fatalf("rc frozen=%d new=%d %s %s", want.RC, got.RC, want.Stderr, got.Stderr)
			}
			if want.Stdout != got.Stdout {
				t.Errorf("actions differ\nfrozen: %q\nnew: %q", want.Stdout, got.Stdout)
			}
			knSameDelta(t, want, got)
			// A second run has nothing left to do.
			again := knNew(t, dir, "", "instructions", "init", "--json")
			if !strings.Contains(again.stdout, `"actions": [], "changed": false`) {
				t.Errorf("second run not a no-op: %s", again.stdout)
			}
		})
	}
}

func TestInstructionsInitSkipsSymlinks(t *testing.T) {
	dir := knProject(t, false)
	knWrite(t, filepath.Join(dir, "AGENTS.md"), "# A\n")
	if err := os.Symlink("AGENTS.md", filepath.Join(dir, "CLAUDE.md")); err != nil {
		t.Skip(err)
	}
	n := knNew(t, dir, "", "instructions", "init", "--json")
	if !strings.Contains(n.stdout, `"action": "skippedSymlink"`) {
		t.Errorf("got %s", n.stdout)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "AGENTS.md")); string(b) != "# A\n" {
		t.Errorf("AGENTS.md was rewritten: %q", b)
	}
}

// CRLF files are read as LF and rewritten as pure LF, like the old helpers.
func TestCRLFMatchGoldenForGlossaryBlocksAndInstructions(t *testing.T) {
	crlf := func(s string) string { return strings.ReplaceAll(s, "\n", "\r\n") }
	t.Run("glossary write and block", func(t *testing.T) {
		dir := glProject(t, false, crlf(glFixtureTerms))
		knWrite(t, filepath.Join(dir, "AGENTS.md"), crlf("# Agents\n\ntext\n"))
		want, got := knFrozen(t, dir, "", "glossary", "write", "Batch", "--def", "a group")
		if want.RC != 0 || got.RC != 0 {
			t.Fatalf("rc frozen=%d new=%d %s %s", want.RC, got.RC, want.Stderr, got.Stderr)
		}
		knSameDelta(t, want, got)
		for k, v := range knTree(t, dir) {
			if strings.Contains(v, "\r") && !strings.HasSuffix(k, ".lock") {
				t.Errorf("%s kept CR", k)
			}
		}
	})
	t.Run("glossary read", func(t *testing.T) {
		want, got := knFrozen(t, glProject(t, false, crlf(glFixtureTerms)), "", "glossary", "read", "worker")
		if want.Stdout != got.Stdout {
			t.Errorf("frozen %q new %q", want.Stdout, got.Stdout)
		}
	})
	t.Run("instructions init", func(t *testing.T) {
		dir := knProject(t, false)
		knWrite(t, filepath.Join(dir, "CLAUDE.md"), crlf("# Mine\n\n<!-- rota-knowledge-start -->\nK\n<!-- rota-knowledge-end -->\n\nafter\n"))
		want, got := knFrozen(t, dir, "", "instructions", "init")
		knSameDelta(t, want, got)
	})
}
