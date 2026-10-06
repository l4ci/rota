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
	t.Setenv("TMPDIR", t.TempDir()) // a failing tier keeps its verify log
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
	if string(b) != "it's here.txt\nkeep.txt\n.rota/config.json\n" { // config.json is untracked
		t.Errorf("expanded files = %q", b)
	}
}

func TestTestRunFilesIncludesUntrackedNotIgnored(t *testing.T) {
	root := tierProject(t, `{"test":{"fast":["printf '%s\\n' {files} > ../out.txt"]}}`)
	write(t, filepath.Join(root, ".gitignore"), "*.log\n")
	gitT(t, root, "add", ".gitignore")
	gitT(t, root, "commit", "-q", "-m", "ignore")
	gitT(t, root, "checkout", "-q", "-b", "feat")
	write(t, filepath.Join(root, "new one.txt"), "x")
	write(t, filepath.Join(root, "noise.log"), "x")
	if o := trRun(t, root, "", "test", "run", "fast", "--base", "main"); o.code != 0 {
		t.Fatalf("exit %d\n%s%s", o.code, o.stdout, o.stderr)
	}
	b, _ := os.ReadFile(filepath.Join(filepath.Dir(root), "out.txt"))
	if string(b) != ".rota/config.json\nnew one.txt\n" {
		t.Errorf("expanded files = %q, want the untracked files minus the ignored one", b)
	}
}

func TestShellQuote(t *testing.T) {
	if got := shellQuote("it's"); got != `'it'\''s'` {
		t.Errorf("shellQuote = %s", got)
	}
}

// A local main that lags origin/main must not pull the upstream commits into
// {files}: the default base is the remote-tracking ref when it is ahead.
func TestTestRunFilesPrefersAheadRemoteBase(t *testing.T) {
	root := tierProject(t, `{"test":{"fast":["printf '%s\\n' {files} > ../out.txt"]}}`)
	gitT(t, root, "checkout", "-q", "-b", "feat")
	write(t, filepath.Join(root, "upstream.txt"), "u")
	gitT(t, root, "add", "upstream.txt")
	gitT(t, root, "commit", "-q", "-m", "upstream")
	gitT(t, root, "update-ref", "refs/remotes/origin/main", "HEAD") // origin/main is ahead of local main
	write(t, filepath.Join(root, "branch.txt"), "b")
	gitT(t, root, "add", "branch.txt")
	gitT(t, root, "commit", "-q", "-m", "branch")
	run := func(args ...string) string {
		t.Helper()
		if o := trRun(t, root, "", append([]string{"test", "run", "fast"}, args...)...); o.code != 0 {
			t.Fatalf("exit %d\n%s%s", o.code, o.stdout, o.stderr)
		}
		b, _ := os.ReadFile(filepath.Join(filepath.Dir(root), "out.txt"))
		return string(b)
	}
	if got := run(); got != "branch.txt\n.rota/config.json\n" {
		t.Errorf("default base: files = %q, want only the branch changes", got)
	}
	if got := run("--base", "main"); !strings.Contains(got, "upstream.txt") {
		t.Errorf("explicit --base main: files = %q, want it used as given (upstream.txt listed)", got)
	}
}
