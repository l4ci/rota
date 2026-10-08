package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

const topicsFixture = `# Knowledge

## Build & Tooling: Smoke testing

- **Smoke one** — a. <!-- 2026-01-01 -->

## Build & Tooling: Git

- **Git one** — b. <!-- 2026-01-02 -->
- **Git two** — c. <!-- 2026-01-03 -->

## Build

- **Build one** — d. <!-- 2026-01-04 -->
`

func topicsProject(t *testing.T) string {
	dir := knProject(t, false)
	knWrite(t, filepath.Join(dir, ".rota", "KNOWLEDGE.md"), topicsFixture)
	knWrite(t, filepath.Join(dir, ".rota", "DECISIONS.md"), topicsFixture)
	return dir
}

func TestQueryMatchesTopicsPartially(t *testing.T) {
	dir := topicsProject(t)
	for _, group := range []string{"knowledge", "decisions"} {
		t.Run(group, func(t *testing.T) {
			// Substring, case-insensitive: one heading.
			got := knNew(t, dir, "", group, "query", "smoke TESTING")
			if !strings.Contains(got.stdout, "## Build & Tooling: Smoke testing") || strings.Contains(got.stdout, "Git one") {
				t.Errorf("partial match: %q", got.stdout)
			}
			if strings.Contains(got.stderr, "no topic heading") {
				t.Errorf("unexpected warning: %q", got.stderr)
			}
			// Substring hitting two headings returns both.
			got = knNew(t, dir, "", group, "query", "tooling")
			if !strings.Contains(got.stdout, "Smoke one") || !strings.Contains(got.stdout, "Git two") || strings.Contains(got.stdout, "Build one") {
				t.Errorf("multi match: %q", got.stdout)
			}
			// An exact heading wins alone, though "build" is a substring of the others.
			got = knNew(t, dir, "", group, "query", "build")
			if !strings.Contains(got.stdout, "Build one") || strings.Contains(got.stdout, "Smoke one") {
				t.Errorf("exact match: %q", got.stdout)
			}
			// No hit is still a missing topic.
			got = knNew(t, dir, "", group, "query", "nope")
			if got.stdout != "" || got.rc != 0 {
				t.Errorf("no match: rc=%d %q", got.rc, got.stdout)
			}
		})
	}
}

func TestTopicsVerbs(t *testing.T) {
	dir := topicsProject(t)
	for _, group := range []string{"knowledge", "decisions"} {
		t.Run(group, func(t *testing.T) {
			got := knNew(t, dir, "", group, "topics")
			want := "Build & Tooling: Smoke testing: 1 bullets\nBuild & Tooling: Git: 2 bullets\nBuild: 1 bullets\n"
			if got.rc != 0 || got.stdout != want {
				t.Errorf("text: rc=%d %q", got.rc, got.stdout)
			}
			j := knNew(t, dir, "", group, "topics", "--json")
			if !strings.Contains(j.stdout, `"name": "Build & Tooling: Git", "bullets": 2`) {
				t.Errorf("json: %q", j.stdout)
			}
			if got := knNew(t, dir, "", group, "topics", "x"); got.rc != 2 {
				t.Errorf("extra arg rc = %d", got.rc)
			}
		})
	}
}

func TestQueryPartialHybrid(t *testing.T) {
	dir := knProject(t, true)
	got := knNew(t, dir, "", "knowledge", "query", "--repo", "web", "archi")
	if !strings.Contains(got.stdout, "Alpha rule") || !strings.Contains(got.stdout, "Web rule") || strings.Contains(got.stdout, "Gamma") {
		t.Errorf("hybrid partial: %q", got.stdout)
	}
}
