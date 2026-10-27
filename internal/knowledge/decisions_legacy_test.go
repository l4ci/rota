package knowledge

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDecisionLegacyMetadataOrders(t *testing.T) {
	core := "Keep boundaries.\n\n*Why.* Prevent drift.\n\n**Forbids.** Hand edits.\n\n**Permits.** Verbs.\n"
	stamp := "<!-- 2026-10-01 -->"
	paths := "<!-- paths: [\"internal/**/*.go\"] -->"
	status := "*Status.* Retired 2026-10-02. Boundary lifted."
	for order, metadata := range [][]string{
		{paths, status, stamp}, {stamp, paths, status}, {status, stamp, paths},
		{paths, stamp, status}, {stamp, status, paths}, {status, paths, stamp},
	} {
		t.Run(fmt.Sprintf("order_%d", order), func(t *testing.T) {
			s := Store{Root: t.TempDir()}
			path := filepath.Join(s.Root, ".rota", "DECISIONS.md")
			active := "# Decisions\n\n## Architecture\n\n### Active legacy\n\n" + core + "\n" + stamp + "\n\n" + paths + "\n"
			before := active + "\n### Retired legacy\n\n" + core + "\n" + strings.Join(metadata, "\n\n") + "\n"
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(before), 0600); err != nil {
				t.Fatal(err)
			}
			report, err := s.CheckDecisions()
			if err != nil || len(report.Errors) > 0 {
				t.Fatalf("legacy metadata rejected: %+v (%v)", report, err)
			}
			if _, err := s.AmendDecision("Architecture", "Active legacy", "why", "Keep the history."); err != nil {
				t.Fatal(err)
			}
			raw, _ := os.ReadFile(path)
			if string(raw) != strings.Replace(before, "Prevent drift.", "Keep the history.", 1) {
				t.Fatalf("amend reordered old metadata: %s", raw)
			}
			body := core + "\n" + stamp + "\n\n" + paths + "\n"
			if _, err := s.AddDecision("Architecture", "New canonical", body, "2026-10-27"); err != nil {
				t.Fatal(err)
			}
			if _, err := s.RetireDecision("Architecture", "New canonical", "Lifted.", "2026-10-28"); err != nil {
				t.Fatal(err)
			}
			raw, _ = os.ReadFile(path)
			start := strings.Index(string(raw), "### New canonical")
			end := strings.Index(string(raw), "### Active legacy")
			entry := string(raw)[start:end]
			if !strings.Contains(entry, "*Status.* Retired 2026-10-28. Lifted.\n\n"+paths+"\n\n<!-- 2026-10-27 -->") {
				t.Fatalf("new writer did not use canonical metadata order: %s", entry)
			}
			text, _, err := s.DecisionsQuery([]string{"Architecture"})
			if err != nil || !strings.Contains(text, "### Active legacy") || strings.Contains(text, "### Retired legacy") {
				t.Fatalf("legacy status did not control active readers: %s (%v)", text, err)
			}
		})
	}
}
