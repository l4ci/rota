package cli

import (
	"bufio"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/proc"
)

// setupSignal is a value `rota setup` read from the project for one question,
// with where it was read.
type setupSignal struct {
	Value    string
	Evidence string
}

// readSetupSignals reads the project in dir for the questions it can answer
// (#1782). A question with no signal is absent and keeps its default. Nothing
// here runs a project command: the evidence is a file, a git answer or a PATH
// entry, and the one process started is `gh auth status` / `glab auth status`.
//
// A key in answered (given with --set or held by the global config) is not
// probed: its value is already settled, so the probe would only cost time.
func readSetupSignals(c *Ctx, dir string, answered map[string]bool) map[string]setupSignal {
	sigs := map[string]setupSignal{}
	d := c.deps()
	run := func(args ...string) (string, bool) {
		res, err := d.Git(c.Context(), dir, args...)
		out := strings.TrimSpace(res.Stdout)
		return out, err == nil && res.ExitCode == 0 && out != ""
	}
	onPath := func(name string) bool { _, err := d.LookPath(name); return err == nil }

	if base, ok := baseBranchSignal(run); ok {
		sigs["git.baseBranch"] = base
	}
	if sig, ok := issuesSourceSignal(c, dir, run, onPath, answered["issues.source"]); ok {
		sigs["issues.source"] = sig
	}
	if sig, ok := testSignals(dir); ok {
		sigs["test.fast"] = sig[0]
		sigs["test.full"] = sig[1]
	}
	var kinds []string
	for _, k := range harness.Kinds() { // each kind is also its CLI's name
		if onPath(k) {
			kinds = append(kinds, k)
		}
	}
	if len(kinds) > 0 {
		ev := kinds[0] + " is on PATH"
		if len(kinds) > 1 {
			ev = strings.Join(kinds, ", ") + " are on PATH; first taken"
		}
		sigs["round.workerKind"] = setupSignal{kinds[0], ev}
	}
	if out, ok := run("worktree", "list", "--porcelain"); ok && strings.Count(out, "worktree ") > 1 {
		sigs["work.isolation"] = setupSignal{"worktree", "the repo has linked worktrees (git worktree list)"}
	} else if st, err := os.Stat(filepath.Join(dir, ".worktrees")); err == nil && st.IsDir() {
		sigs["work.isolation"] = setupSignal{"worktree", ".worktrees/ exists"}
	}
	switch {
	case onPath("herdr"):
		sigs["round.dispatch"] = setupSignal{"herdr", "herdr is on PATH"}
	case onPath("tmux") && d.Getenv("TMUX") != "":
		sigs["round.dispatch"] = setupSignal{"tmux", "tmux is on PATH and $TMUX is set"}
	}
	return sigs
}

// baseBranchSignal is origin's default branch, else the first of main, master
// and trunk that exists locally.
func baseBranchSignal(run func(...string) (string, bool)) (setupSignal, bool) {
	if ref, ok := run("symbolic-ref", "--short", "refs/remotes/origin/HEAD"); ok {
		return setupSignal{strings.TrimPrefix(ref, "origin/"), "refs/remotes/origin/HEAD points at " + ref}, true
	}
	if out, ok := run("branch", "--list", "--format=%(refname:short)", "main", "master", "trunk"); ok {
		have := map[string]bool{}
		for _, b := range strings.Fields(out) {
			have[b] = true
		}
		for _, b := range []string{"main", "master", "trunk"} {
			if have[b] {
				return setupSignal{b, "local branch " + b + " exists (git branch --list)"}, true
			}
		}
	}
	return setupSignal{}, false
}

// issuesSourceSignal is the forge named by origin's host, when its CLI is
// installed and signed in; file when origin names a forge whose CLI is not
// ready. No origin, or a host that is neither, is no signal.
func issuesSourceSignal(c *Ctx, dir string, run func(...string) (string, bool), onPath func(string) bool, skip bool) (setupSignal, bool) {
	if skip {
		return setupSignal{}, false
	}
	raw, ok := run("remote", "get-url", "origin")
	if !ok {
		return setupSignal{}, false
	}
	host := remoteHost(raw)
	forge, cli := "", ""
	switch {
	case host == "github.com":
		forge, cli = "github", "gh"
	case host == "gitlab.com" || strings.HasPrefix(host, "gitlab."):
		forge, cli = "gitlab", "glab"
	default:
		return setupSignal{}, false
	}
	if !onPath(cli) {
		return setupSignal{"file", "origin is " + host + " but " + cli + " is not installed"}, true
	}
	res, err := c.deps().Proc(c.Context(), proc.Cmd{Name: cli, Args: []string{"auth", "status", "--hostname", host}, Dir: dir, Timeout: authProbeTimeout})
	if err != nil {
		// A timeout or a failed exec is no answer: keep the schema default.
		return setupSignal{}, false
	}
	if res.ExitCode != 0 {
		return setupSignal{"file", "origin is " + host + " but " + cli + " is not authenticated"}, true
	}
	return setupSignal{forge, "origin is " + host + ", " + cli + " is authenticated"}, true
}

// authProbeTimeout bounds `gh|glab auth status`, which can reach the network.
const authProbeTimeout = 5 * time.Second

// remoteHost is the host of a git remote URL (https://, ssh:// or scp-like).
func remoteHost(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return strings.ToLower(u.Hostname())
	}
	if _, rest, ok := strings.Cut(raw, "@"); ok {
		raw = rest
	}
	host, _, _ := strings.Cut(raw, ":")
	return strings.ToLower(host)
}

// testSignals is [test.fast, test.full] read from the project's manifests: the
// first manifest that names a test command is the fast one; the full one is a
// command a CI file runs, else a Makefile test target, else the fast one.
func testSignals(dir string) ([2]setupSignal, bool) {
	var fast setupSignal
	for _, m := range manifestTests {
		if cmd, ev, ok := m(dir); ok {
			fast = setupSignal{jsonList(cmd), ev}
			break
		}
	}
	if fast.Value == "" {
		return [2]setupSignal{}, false
	}
	full := fast
	if cmd, ev, ok := ciTest(dir); ok {
		full = setupSignal{jsonList(cmd), ev}
	} else if ok := fileHasLine(filepath.Join(dir, "Makefile"), regexp.MustCompile(`^test:`)); ok && fast.Value != jsonList("make test") {
		full = setupSignal{jsonList("make test"), "Makefile has a test target"}
	}
	return [2]setupSignal{fast, full}, true
}

func jsonList(cmd string) string {
	b, _ := jsonx.MarshalCompact([]any{cmd})
	return string(b)
}

// manifestTests read one manifest each, in precedence order.
var manifestTests = []func(dir string) (cmd, evidence string, ok bool){
	func(dir string) (string, string, bool) {
		return "go test ./...", "go.mod", exists(dir, "go.mod")
	},
	func(dir string) (string, string, bool) {
		raw, err := os.ReadFile(filepath.Join(dir, "package.json"))
		if err != nil {
			return "", "", false
		}
		v, err := jsonx.Decode(raw)
		obj, _ := v.(*jsonx.Object)
		if err != nil || obj == nil {
			return "", "", false
		}
		scripts, _ := obj.Get("scripts")
		if so, ok := scripts.(*jsonx.Object); ok {
			if _, has := so.Get("test"); has {
				return "npm test", `package.json has a "test" script`, true
			}
		}
		return "", "", false
	},
	func(dir string) (string, string, bool) {
		raw, err := os.ReadFile(filepath.Join(dir, "pyproject.toml"))
		if err == nil && strings.Contains(string(raw), "pytest") {
			return "pytest", "pyproject.toml configures pytest", true
		}
		return "", "", false
	},
	func(dir string) (string, string, bool) {
		return "cargo test", "Cargo.toml", exists(dir, "Cargo.toml")
	},
	func(dir string) (string, string, bool) {
		ok := fileHasLine(filepath.Join(dir, "Makefile"), regexp.MustCompile(`^test:`))
		return "make test", "Makefile has a test target", ok
	},
}

var ciTestRun = regexp.MustCompile(`^\s*-?\s*(?:run:\s*)?((?:go test|npm test|npm run test|pytest|cargo test|make test)\b.*)$`)

// ciTest is the first test command a CI file runs, with file:line as evidence.
func ciTest(dir string) (cmd, evidence string, ok bool) {
	files, _ := filepath.Glob(filepath.Join(dir, ".github", "workflows", "*.y*ml"))
	files = append(files, filepath.Join(dir, ".gitlab-ci.yml"))
	for _, f := range files {
		fh, err := os.Open(f)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(fh)
		for n := 1; sc.Scan(); n++ {
			if m := ciTestRun.FindStringSubmatch(sc.Text()); m != nil {
				fh.Close()
				rel, _ := filepath.Rel(dir, f)
				return strings.TrimSpace(m[1]), rel + ":" + strconv.Itoa(n) + " runs it", true
			}
		}
		fh.Close()
	}
	return "", "", false
}

func exists(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}

func fileHasLine(path string, re *regexp.Regexp) bool {
	fh, err := os.Open(path)
	if err != nil {
		return false
	}
	defer fh.Close()
	sc := bufio.NewScanner(fh)
	for sc.Scan() {
		if re.MatchString(sc.Text()) {
			return true
		}
	}
	return false
}
