package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrateMilestoneFiles(t *testing.T) {
	stale := func(t *testing.T, config string) (root, file string) {
		root = trackerProject(t, config)
		dir := filepath.Join(root, ".rota", "milestones")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		file = filepath.Join(dir, "M07.md")
		if err := os.WriteFile(file, []byte("---\nid: M07\n---\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return root, file
	}
	t.Run("tracker previews then archives", func(t *testing.T) {
		root, file := stale(t, issuesConfig)
		deps := withTracker(t, issueFixture())
		code, env, stderr := rotaRunWith(t, deps, "--json", "-C", root, "migrate", "milestone-files")
		if code != 0 || ddata(t, env)["changed"] != false {
			t.Fatalf("preview exit %d %v %s", code, env, stderr)
		}
		if strings.Contains(stderr, "commit") {
			t.Fatalf("preview printed a commit hint: %s", stderr)
		}
		if _, err := os.Stat(file); err != nil {
			t.Fatal("preview moved the file")
		}
		code, env, stderr = rotaRunWith(t, deps, "--json", "-C", root, "migrate", "milestone-files", "--apply")
		if code != 0 || ddata(t, env)["changed"] != true {
			t.Fatalf("apply exit %d %v %s", code, env, stderr)
		}
		if !strings.Contains(stderr, "commit .rota/milestones and .rota/archive/milestones") {
			t.Fatalf("apply gave no commit hint: %s", stderr)
		}
		if _, err := os.Stat(filepath.Join(root, ".rota", "archive", "milestones", "M07.md")); err != nil {
			t.Fatalf("not archived: %v", err)
		}
	})
	t.Run("file mode refuses", func(t *testing.T) {
		root, file := stale(t, "")
		code, env, stderr := rotaRunWith(t, withTracker(t, issueFixture()), "--json", "-C", root, "migrate", "milestone-files", "--apply")
		if code != ExitRefused || ddata(t, env)["blockedBy"] != "backend" {
			t.Fatalf("exit %d %v %s", code, env, stderr)
		}
		if _, err := os.Stat(file); err != nil {
			t.Fatal("file mode lost its milestone file")
		}
	})
}
