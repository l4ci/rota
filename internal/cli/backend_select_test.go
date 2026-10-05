package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// Per-verb backend refusals, decided in backend_select.go and fileRoot: a
// file-only verb under backlog.backend "issues" and an issue-only verb under
// "file" both stop with blockedBy "backend". A mutating verb is refused (4); a
// read-only one fails (1), since the conventions forbid 4 for a read.
func TestBackendRefusals(t *testing.T) {
	raw := filepath.Join(t.TempDir(), "raw.md")
	if err := os.WriteFile(raw, []byte("- **[B02] [P1] Raw.** x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	body := bodyFile(t, "x")
	cases := []struct {
		name   string
		config string
		want   int
		args   []string
	}{
		// file-only verbs under issues (openBacklogFile)
		{"backlog archive", issuesConfig, ExitRefused, []string{"backlog", "archive"}},
		{"backlog backfill", issuesConfig, ExitRefused, []string{"backlog", "backfill"}},
		{"item field set detail", issuesConfig, ExitRefused, []string{"item", "field", "set", "7", "--name", "detail", "--value", "x"}},
		{"item create --raw-file", issuesConfig, ExitRefused, []string{"item", "create", "--kind", "bugs", "--raw-file", raw}},
		{"id next milestones", issuesConfig, ExitRefused, []string{"id", "next", "--kind", "milestones"}},
		// file-only verbs under issues (fileRoot): mutating refuses, read-only fails
		{"design amend", issuesConfig, ExitRefused, []string{"design", "amend", "F7", "--section", "Goal", "--mode", "append", "--body-file", body}},
		{"design list", issuesConfig, 1, []string{"design", "list"}},
		// issue-only verbs under file (openIssueBackend)
		{"review queue", "", 1, []string{"review", "queue"}},
		{"release milestone-check", "", 1, []string{"release", "milestone-check", "M01"}},
		{"release close-milestone", "", ExitRefused, []string{"release", "close-milestone", "M01", "--release", "1.0.0"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := trackerProject(t, c.config)
			deps := withTracker(t, issueFixture())
			code, env, stderr := rotaRunWith(t, deps, append([]string{"--json", "-C", root}, c.args...)...)
			if code != c.want {
				t.Fatalf("exit %d, want %d (%v) %s", code, c.want, env, stderr)
			}
			if d := ddata(t, env); d["blockedBy"] != "backend" {
				t.Errorf("data %v, want blockedBy backend", d)
			}
		})
	}
}
