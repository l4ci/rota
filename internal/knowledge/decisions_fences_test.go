package knowledge

import (
	"path/filepath"
	"strings"
	"testing"
)

const fencedDecisionBody = "Keep boundaries active.\n\n*Why.* Examples follow.\n\n%s\n## Example topic\n### Example entry\n*Status.* Retired 2026-10-01. Example only.\n%s\n\n**Forbids.** Losing boundaries.\n\n**Permits.** Literal examples.\n\n<!-- paths: [\"internal/**/*.go\"] -->\n"

func TestDecisionReadersTreatFencesAsBody(t *testing.T) {
	for _, fence := range []string{"```", "~~~~", "````"} {
		t.Run(fence, func(t *testing.T) {
			s := Store{Root: t.TempDir()}
			body := strings.ReplaceAll(fencedDecisionBody, "%s", fence)
			if _, err := s.AddDecision("Architecture", "Live", body, "2026-10-27"); err != nil {
				t.Fatal(err)
			}
			plain := "Keep history.\n\n*Why.* Audit.\n\n**Forbids.** Deletion.\n\n**Permits.** Retirement.\n"
			if _, err := s.AddDecision("History", "Old", plain, "2026-10-01"); err != nil {
				t.Fatal(err)
			}
			if _, err := s.RetireDecision("History", "Old", "Lifted.", "2026-10-27"); err != nil {
				t.Fatal(err)
			}
			check := func() {
				t.Helper()
				text, _, err := s.DecisionsQuery([]string{"Architecture"})
				if err != nil || !strings.Contains(text, "### Live") || !strings.Contains(text, "### Example entry") || !strings.Contains(text, "*Status.* Retired 2026-10-01. Example only.") {
					t.Fatalf("literal example hid/truncated active entry: %q (%v)", text, err)
				}
				stats, err := s.DecisionsStats()
				if err != nil || len(stats) != 1 || stats[0].Name != "Architecture" {
					t.Fatalf("fenced or retired topic leaked into stats: %+v (%v)", stats, err)
				}
				topics, _, err := s.BlockInputs("decisions", Umbrella)
				if err != nil || len(topics) != 1 || topics[0] != "Architecture" {
					t.Fatalf("wrong managed topics: %v (%v)", topics, err)
				}
				entries, err := s.PathEntries(filepath.Join(s.Root, "internal/example.go"))
				if err != nil || len(entries) != 1 || !strings.HasSuffix(entries[0].ID, "::Architecture::Live") || !strings.Contains(entries[0].Text, "### Example entry") {
					t.Fatalf("wrong boundary recall: %+v (%v)", entries, err)
				}
			}
			check()
			why := "Amended example.\n\n" + fence + "\n## Example topic\n### Example entry\n*Status.* Retired 2026-10-01. Example only.\n" + fence
			if _, err := s.AmendDecision("Architecture", "Live", "why", why); err != nil {
				t.Fatal(err)
			}
			check()
		})
	}
}
