package doctor

import (
	"context"
	"testing"
)

// The table is the one place the report order lives: every row is named once
// and only the legacy-state row leads.
func TestCheckTableNamesUniqueAndOnlyLegacyLeads(t *testing.T) {
	d := &runner{ctx: context.Background()}
	seen := map[string]bool{}
	for _, e := range d.checkTable() {
		if e.name == "" || seen[e.name] {
			t.Fatalf("check table row name %q is empty or repeated", e.name)
		}
		seen[e.name] = true
		if e.leads && e.name != "legacy state" {
			t.Fatalf("row %q leads the report; only legacy state does", e.name)
		}
	}
}
