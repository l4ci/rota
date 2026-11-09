package cli

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/initproj"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/proc"
	"github.com/l4ci/rota/internal/remoteurl"
)

// setupSignal is a value `rota setup` read from the project for one question,
// with where it was read.
type setupSignal struct {
	Value    string
	Evidence string
}

func fromSignal(s initproj.Signal) setupSignal { return setupSignal{s.Value, s.Evidence} }

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

	if base, ok, err := d.Repo(dir).Base(c.Context(), ""); err == nil && ok {
		sigs["git.baseBranch"] = setupSignal{base, "git base-branch fallback resolved " + base}
	}
	if sig, ok := issuesSourceSignal(c, dir, run, onPath, answered["issues.source"]); ok {
		sigs["issues.source"] = sig
	}
	if sig, ok := initproj.TestCommands(dir); ok {
		sigs["test.fast"] = fromSignal(sig[0])
		sigs["test.full"] = fromSignal(sig[1])
	}
	if setup, shared, ok := initproj.DepSignals(dir); ok {
		sigs[config.RoundEnvSetupKey] = fromSignal(setup)
		sigs[config.RoundSharedDirsKey] = fromSignal(shared)
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
		list, _ := jsonx.MarshalCompact(jsonx.Strings(kinds[:1]))
		sigs[config.RoundHarnessesKey] = setupSignal{string(list), ev}
	}
	if out, ok := run("worktree", "list", "--porcelain"); ok && strings.Count(out, "worktree ") > 1 {
		sigs["work.isolation"] = setupSignal{"worktree", "the repo has linked worktrees (git worktree list)"}
	} else if st, err := os.Stat(filepath.Join(dir, ".worktrees")); err == nil && st.IsDir() {
		sigs["work.isolation"] = setupSignal{"worktree", ".worktrees/ exists"}
	}
	switch {
	case d.Getenv("HC_TERMINAL_ID") != "" || d.Getenv("HC_TERMINAL_SOCKET") != "":
		sigs[config.RoundDispatchKey] = setupSignal{"herdr-core", "HC_TERMINAL_ID/HC_TERMINAL_SOCKET identify the associated Herdr App Core"}
		delete(sigs, config.RoundHarnessesKey)
		for _, kind := range harness.CoreWorkerKinds() {
			if onPath(kind) {
				list, _ := jsonx.MarshalCompact(jsonx.Strings([]string{kind}))
				sigs[config.RoundHarnessesKey] = setupSignal{string(list), kind + " is on PATH and supports native Core status reporting"}
				break
			}
		}
	case onPath("herdr"):
		sigs[config.RoundDispatchKey] = setupSignal{"herdr", "herdr is on PATH"}
	case onPath("tmux") && d.Getenv("TMUX") != "":
		sigs[config.RoundDispatchKey] = setupSignal{"tmux", "tmux is on PATH and $TMUX is set"}
	}
	return sigs
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
	host := remoteurl.Parse(raw).Host
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
