package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// denyRead makes path unreadable, creating it first when it is missing.
func denyRead(t *testing.T, path, content string) {
	t.Helper()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		knWrite(t, path, content)
	}
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })
	if _, err := os.ReadFile(path); err == nil {
		t.Skip("runner bypasses file permissions")
	}
}

func wantUnreadable(t *testing.T, dir, source string, args []string) {
	t.Helper()
	stdin := "body"
	if args[0] == "decisions" {
		stdin = decisionBody
	}
	got := knNew(t, dir, stdin, args...)
	if got.rc != ExitUnavailable || !strings.Contains(got.stderr, source) {
		t.Errorf("%v: want exit %d naming %s, got %+v", args, ExitUnavailable, source, got)
	}
}

// One table per shared reader: every verb that goes through it exits 5.
func TestUnreadableProjectFilesExit5(t *testing.T) {
	for _, tc := range []struct {
		source string
		verbs  []string
	}{
		{".rota/KNOWLEDGE.md", []string{
			"knowledge query Architecture", "knowledge stats", "knowledge topics",
			"knowledge add --topic Architecture --title New --body-file -",
			"knowledge amend --topic Architecture --fragment x --mode append --body-file -",
			"knowledge tier list", "glossary read Page",
			"glossary write Page --def view", "backlog stale --kind knowledge", "summary",
		}},
		{".rota/DECISIONS.md", []string{
			"decisions query Architecture", "decisions topics", "decisions stats", "decisions check",
			"decisions add --topic Architecture --title T --body-file -",
			"decisions retire --topic Architecture --title T --reason r", "summary",
		}},
		{".rota/map/alpha.md", []string{"map query alpha", "map index", "map stats", "backlog stale --kind map"}},
		{".rota/qa/alpha.md", []string{"qa query alpha", "qa index"}},
	} {
		for _, verb := range tc.verbs {
			t.Run(tc.source+"/"+verb, func(t *testing.T) {
				dir := knProject(t, false)
				knWrite(t, filepath.Join(dir, ".rota", "BACKLOG.md"), "# Backlog\n")
				denyRead(t, filepath.Join(dir, tc.source), "## Architecture\n")
				wantUnreadable(t, dir, tc.source, strings.Fields(verb))
			})
		}
	}
}

func TestUnreadableExternalFilesExit5(t *testing.T) {
	for _, tc := range []struct {
		source string
		verbs  []string
	}{
		{"README.md", []string{"knowledge topics --external", "knowledge query --external go"}},
		{"topics/go.md", []string{
			// git reports an unreadable tracked file as an uncommitted change.
			"knowledge query --external go", "knowledge query --external --match vet",
			"knowledge add --external --topic Go --title New --body-file -",
		}},
		{"topics/git.md", []string{"knowledge add --external --topic Go --title New --body-file -"}},
		{"rota-knowledge.json", []string{"knowledge add --external --topic Go --title New --body-file -"}},
	} {
		for _, verb := range tc.verbs {
			t.Run(tc.source+"/"+verb, func(t *testing.T) {
				project, clone := extWalkFixture(t)
				denyRead(t, filepath.Join(clone, tc.source), `{"write":"push"}`)
				wantUnreadable(t, project, tc.source, strings.Fields(verb))
			})
		}
	}
}
