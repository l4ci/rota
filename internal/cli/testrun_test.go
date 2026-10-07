package cli

import (
	"fmt"
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

// envReport is a command that writes the variables test.isolate touches to out.txt.
const envReport = `printf 'herdr=%s\ntmux=%s\nsock=%s\npid=%s\nhome=%s\nxdg=%s\ngocache=%s\n' "$HERDR_PANE_ID" "$TMUX" "$SSH_AUTH_SOCK" "$SSH_AGENT_PID" "$HOME" "$XDG_CONFIG_HOME" "$GOCACHE" > out.txt`

func envOf(t *testing.T, root string) map[string]string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, "out.txt"))
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]string{}
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		k, v, _ := strings.Cut(l, "=")
		m[k] = v
	}
	return m
}

func TestTestRunIsolatesEveryTier(t *testing.T) {
	realHome := t.TempDir()
	t.Setenv("HOME", realHome)
	t.Setenv("HERDR_PANE_ID", "w1:p1")
	t.Setenv("TMUX", "/s,1,0")
	t.Setenv("SSH_AUTH_SOCK", "/real/agent")
	t.Setenv("SSH_AGENT_PID", "42")
	t.Setenv("GOCACHE", "/real/gocache")
	for _, tier := range []string{"fast", "full", "e2e"} {
		for _, isolate := range []bool{true, false} {
			cfg := fmt.Sprintf(`{"test":{%q:[%q],"isolate":%v}}`, tier, envReport, isolate)
			root := tierProject(t, cfg)
			if o := trRun(t, root, "", "test", "run", tier); o.code != 0 {
				t.Fatalf("%s isolate=%v: exit %d\n%s%s", tier, isolate, o.code, o.stdout, o.stderr)
			}
			got := envOf(t, root)
			if !isolate {
				want := map[string]string{"herdr": "w1:p1", "tmux": "/s,1,0", "sock": "/real/agent", "pid": "42", "home": realHome, "gocache": "/real/gocache"}
				for k, v := range want {
					if got[k] != v {
						t.Errorf("%s isolate=false: %s = %q, want %q", tier, k, got[k], v)
					}
				}
				continue
			}
			for _, k := range []string{"herdr", "tmux", "sock", "pid"} {
				if got[k] != "" {
					t.Errorf("%s: %s = %q survived isolation", tier, k, got[k])
				}
			}
			if got["home"] == realHome || got["home"] == "" || !strings.HasPrefix(got["xdg"], filepath.Dir(got["home"])) {
				t.Errorf("%s: HOME %q / XDG %q not pinned to a temp root", tier, got["home"], got["xdg"])
			}
			if got["gocache"] != "/real/gocache" {
				t.Errorf("%s: GOCACHE = %q, want it kept", tier, got["gocache"])
			}
			if _, err := os.Stat(filepath.Dir(got["home"])); !os.IsNotExist(err) {
				t.Errorf("%s: temp root %s was not removed", tier, filepath.Dir(got["home"]))
			}
		}
	}
}

func TestTestRunIsolatesByDefault(t *testing.T) {
	t.Setenv("HERDR_PANE_ID", "w1:p1")
	root := tierProject(t, fmt.Sprintf(`{"test":{"fast":[%q]}}`, envReport))
	if o := trRun(t, root, "", "test", "run", "fast"); o.code != 0 {
		t.Fatalf("exit %d\n%s%s", o.code, o.stdout, o.stderr)
	}
	if got := envOf(t, root); got["herdr"] != "" {
		t.Errorf("HERDR_PANE_ID = %q survived the default", got["herdr"])
	}
}
