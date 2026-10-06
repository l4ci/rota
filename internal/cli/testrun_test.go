package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// tierProject is a repo on main whose config holds cfg.
func tierProject(t *testing.T, cfg string) string {
	t.Helper()
	root := newRepo(t, t.TempDir(), "proj", "main")
	write(t, filepath.Join(root, ".rota", "config.json"), cfg)
	return root
}

func TestTestRunPassesAndStopsAtFirstFailure(t *testing.T) {
	root := tierProject(t, `{"test":{"fast":["echo a","false","echo never"],"e2e":["true"]}}`)
	o := trRun(t, root, "", "test", "run", "e2e", "--json")
	if o.code != 0 {
		t.Fatalf("passing tier: exit %d\n%s%s", o.code, o.stdout, o.stderr)
	}
	o = trRun(t, root, "", "test", "run", "fast", "--json")
	if o.code != 1 {
		t.Fatalf("failing tier: exit %d, want 1\n%s%s", o.code, o.stdout, o.stderr)
	}
	data := envelope(t, o.stdout)["data"].(map[string]any)
	if !reflect.DeepEqual(data["commands"], []any{"echo a", "false"}) || !reflect.DeepEqual(data["failed"], []any{"false"}) ||
		!reflect.DeepEqual(data["verified"], []any{"echo a"}) || data["tier"] != "fast" || data["logPath"] == "" {
		t.Errorf("data = %v", data)
	}
}

func TestTestRunEmptyAndUnknownTier(t *testing.T) {
	root := tierProject(t, `{}`)
	o := trRun(t, root, "", "test", "run", "fast")
	if o.code != 3 || !strings.Contains(o.stderr, "test.fast") {
		t.Errorf("empty tier: exit %d, stderr %q", o.code, o.stderr)
	}
	if o := trRun(t, root, "", "test", "run", "slow"); o.code != 2 {
		t.Errorf("unknown tier: exit %d, want 2", o.code)
	}
}

func TestTestRunFullHonoursLegacyVerifyCommands(t *testing.T) {
	root := tierProject(t, `{"refactor":{"verifyCommands":["false"]}}`)
	if o := trRun(t, root, "", "test", "run", "full"); o.code != 1 {
		t.Errorf("legacy key not used: exit %d\n%s%s", o.code, o.stdout, o.stderr)
	}
}

func TestTestRunExpandsFilesAgainstBase(t *testing.T) {
	root := tierProject(t, `{"test":{"fast":["printf '%s\\n' {files} > out.txt"]}}`)
	write(t, filepath.Join(root, "keep.txt"), "k")
	write(t, filepath.Join(root, "gone.txt"), "g")
	gitT(t, root, "add", "keep.txt", "gone.txt")
	gitT(t, root, "commit", "-q", "-m", "seed files")
	gitT(t, root, "checkout", "-q", "-b", "feat")
	write(t, filepath.Join(root, "it's here.txt"), "x")
	gitT(t, root, "add", "it's here.txt")
	gitT(t, root, "commit", "-q", "-m", "add")
	gitT(t, root, "rm", "-q", "gone.txt")
	write(t, filepath.Join(root, "keep.txt"), "changed")
	if o := trRun(t, root, "", "test", "run", "fast", "--base", "main"); o.code != 0 {
		t.Fatalf("exit %d\n%s%s", o.code, o.stdout, o.stderr)
	}
	b, _ := os.ReadFile(filepath.Join(root, "out.txt"))
	if string(b) != "it's here.txt\nkeep.txt\n" {
		t.Errorf("expanded files = %q", b)
	}
}

func TestShellQuote(t *testing.T) {
	if got := shellQuote("it's"); got != `'it'\''s'` {
		t.Errorf("shellQuote = %s", got)
	}
}
