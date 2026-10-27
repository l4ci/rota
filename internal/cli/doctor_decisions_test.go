package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctorDecisionsCheck(t *testing.T) {
	for _, tc := range []struct{ name, body, status, detail string }{
		{"malformed", "Rule only.\n", "warn", "Architecture :: Boundary: missing why"},
		{"missing date", decisionBody, "pass", "Architecture :: Boundary: missing date"},
		{"valid", decisionBody + "\n<!-- 2026-10-27 -->\n", "pass", "well-formed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := knProject(t, false)
			knWrite(t, filepath.Join(dir, ".rota", "DECISIONS.md"), "# Decisions\n\n## Architecture\n\n### Boundary\n\n"+tc.body)
			d := doctorDeps(t, map[string]string{"git": `case "$1" in remote) exit 2;; esac; exit 0`})
			_, out, _ := rotaInWith(t, d, dir, "doctor", "--json")
			_, checks := doctorData(t, out)
			row := checks["decisions"]
			if row == nil || row["status"] != tc.status || !strings.Contains(row["detail"].(string), tc.detail) {
				t.Fatalf("decisions finding = %v, want %s %q", row, tc.status, tc.detail)
			}
			if tc.status == "warn" && !strings.Contains(row["hint"].(string), "rota decisions amend") {
				t.Fatalf("missing repair hint: %v", row)
			}
			if tc.status == "pass" && row["hint"] != nil {
				t.Fatalf("date notice should be detail only: %v", row)
			}
		})
	}
}
