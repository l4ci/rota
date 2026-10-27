package round

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/worker"
)

const briefDecisionFixture = `# Decisions

## Architecture

### Keep the boundary

Use the shared store.

**Forbids.**
- Splitting the store.

**Permits.**
- Adding store methods.

### Old boundary

Never use a store.

*Status.* Retired on 2026-10-01.

## Rounds

### Keep claims

Claims travel with transfers.

## Empty

## Lifted

### Gone

Removed rule.

*Status.* Superseded by Keep claims.
`

func writeBriefDecisions(t *testing.T, root, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, ".rota", "DECISIONS.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAssignDecisionTopicReminder(t *testing.T) {
	f := newAssignFixture(t)
	writeBriefDecisions(t, f.root, briefDecisionFixture)
	if _, err := f.assign("12", "ben", nil); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"DECISIONS.md topics: Architecture, Rounds", "rota decisions query", "before implementing"} {
		if !strings.Contains(f.host.sent, want) {
			t.Errorf("brief lacks %q:\n%s", want, f.host.sent)
		}
	}
	for _, absent := range []string{"Empty", "Lifted", "Use the shared store."} {
		if strings.Contains(f.host.sent, absent) {
			t.Errorf("brief unexpectedly contains %q", absent)
		}
	}
}

func TestAssignNamedDecisionTopics(t *testing.T) {
	f := newAssignFixture(t)
	writeBriefDecisions(t, f.root, briefDecisionFixture)
	if _, err := f.assign("12", "ben", func(o *AssignOpts) {
		o.DecisionTopics = []string{"Architecture", "Rounds", "Architecture"}
		o.SettledAnswers = "m: Keep the CLI spelling."
	}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"### Keep the boundary\n\nUse the shared store.", "**Forbids.**\n- Splitting the store.", "**Permits.**\n- Adding store methods.", "### Keep claims", "m: Keep the CLI spelling."} {
		if !strings.Contains(f.host.sent, want) {
			t.Errorf("brief lacks %q:\n%s", want, f.host.sent)
		}
	}
	for _, absent := range []string{"Old boundary", "Never use a store", "Removed rule", "### Gone", "DECISIONS.md topics:"} {
		if strings.Contains(f.host.sent, absent) {
			t.Errorf("brief unexpectedly contains %q", absent)
		}
	}
	if n := strings.Count(f.host.sent, "### Keep the boundary"); n != 1 {
		t.Errorf("entry appears %d times, want once", n)
	}
}

func TestDecisionContextSurvivesRecovery(t *testing.T) {
	for _, queued := range []bool{false, true} {
		name := "slot"
		if queued {
			name = "queued"
		}
		t.Run(name, func(t *testing.T) {
			f := newMoveFx(t)
			writeBriefDecisions(t, f.root, briefDecisionFixture)
			if _, err := f.env.Assign(bg, f.root, f.be, AssignOpts{ID: "12", Agent: "ben", DecisionTopics: []string{"Architecture"}}); err != nil {
				t.Fatal(err)
			}
			writeBriefDecisions(t, f.root, "# Decisions\n\n## Architecture\n\n### Replacement\n\nA different rule.\n")
			assertContext := func(brief string) {
				t.Helper()
				if !strings.Contains(brief, "Use the shared store.") || strings.Contains(brief, "A different rule.") {
					t.Fatalf("original decision context lost:\n%s", brief)
				}
			}
			brief, _, err := f.env.RebuildTaskBrief(bg, f.root, f.be, worker.DispatchOpts{Slot: "ben", Task: "12"}, f.env.Round)
			if err != nil {
				t.Fatal(err)
			}
			assertContext(brief)
			if _, err := f.assign("12", "ben"); err != nil {
				t.Fatal(err)
			}
			assertContext(f.host.sent)
			if queued {
				f.finish(t, "ben", pr7)
				if _, err := f.assign("13", "ben"); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := f.transfer("12", "dana", nil); err != nil {
				t.Fatal(err)
			}
			assertContext(f.host.sent)
			brief, _, err = f.env.RebuildTaskBrief(bg, f.root, f.be, worker.DispatchOpts{Slot: "dana", Task: "12"}, f.env.Round)
			if err != nil {
				t.Fatal(err)
			}
			assertContext(brief)
		})
	}
}

func TestAssignWithoutDecisionEntries(t *testing.T) {
	for _, content := range []string{"missing", "", "# Decisions\n\n## Architecture\n", "# Decisions\n\n## Architecture\n\n### Retired\n\nOld rule.\n\n*Status.* Retired on 2026-10-01.\n"} {
		t.Run(content, func(t *testing.T) {
			f := newAssignFixture(t)
			if content != "missing" {
				writeBriefDecisions(t, f.root, content)
			}
			if _, err := f.assign("12", "ben", nil); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(f.host.sent, "DECISIONS.md") {
				t.Fatalf("empty decisions changed brief:\n%s", f.host.sent)
			}
		})
	}
}
