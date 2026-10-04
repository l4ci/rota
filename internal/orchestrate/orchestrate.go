// Package orchestrate launches the orchestrator session: one command from a
// shell ends in a live agent that has started /rota-orchestrate, under
// `rota keepalive run`, in a host tab a person can watch (#19).
//
// It decides what to start and where; it does not run `round start` (slate
// choice stays in the skill) and does not run doctor (the verb does, so a
// failing check stops everything before any session exists).
package orchestrate

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/shlex"
)

// Harness is one agent the orchestrator can run in. The table below is the
// whole list of what rota knows how to start: #33 adds rows, nothing else.
type Harness struct {
	Name string
	// Command is the argv that starts the agent, without its first prompt.
	Command func(root string, cfg any) ([]string, error)
	// Prompt starts the orchestrate skill in the harness's own invocation
	// syntax: Claude Code takes a slash command, Codex a `$skill` mention.
	Prompt string
}

// Harnesses is the launch table, in the order the error message lists it.
var Harnesses = []Harness{
	{Name: "claude", Prompt: "/rota-orchestrate", Command: claudeCommand},
	{Name: "codex", Prompt: "$rota-orchestrate", Command: func(string, any) ([]string, error) { return []string{"codex"}, nil }},
	// Hermes and opencode take no positional prompt, so Command ends in the
	// flag that does: the supervisor appends the prompt as the last argument,
	// first start and restarts alike. `-s` preloads the skill in Hermes (a
	// project skill loads only after `hermes skills trust`); opencode loads
	// skills through a model-side tool, so its prompt names the skill in words.
	{Name: "hermes", Prompt: "You are the orchestrator: run the rota-orchestrate skill.",
		Command: func(string, any) ([]string, error) { return []string{"hermes", "chat", "-s", "rota-orchestrate", "-q"}, nil }},
	{Name: "opencode", Prompt: "You are the orchestrator: load the rota-orchestrate skill and follow it.",
		Command: func(string, any) ([]string, error) { return []string{"opencode", "--prompt"}, nil }},
}

// DefaultHarness is the value of orchestrator.harness when unset.
const DefaultHarness = "claude"

// Label names the orchestrator's tab or window.
const Label = "orchestrator"

// claudeCommand is work.operatorCommand when set, else a fresh session on
// models.orchestrator in `auto` mode (the orchestrator merges and talks to the
// user, so it keeps a gate the workers do not).
func claudeCommand(_ string, cfg any) ([]string, error) {
	if s := str(cfg, "work.operatorCommand"); s != "" {
		argv, err := shlex.Split(s)
		if err != nil || len(argv) == 0 {
			return nil, fmt.Errorf("work.operatorCommand is not a command line: %q", s)
		}
		return argv, nil
	}
	model := str(cfg, "models.orchestrator")
	if model == "" {
		model = "opus"
	}
	return []string{"claude", "--model", model, "--permission-mode", "auto"}, nil
}

func str(cfg any, key string) string {
	v, _ := config.Lookup(cfg, key)
	s, _ := v.(string)
	return s
}

// Lookup finds a harness by its orchestrator.harness name.
func Lookup(name string) (Harness, error) {
	var names []string
	for _, h := range Harnesses {
		if h.Name == name {
			return h, nil
		}
		names = append(names, h.Name)
	}
	return Harness{}, fmt.Errorf("orchestrator.harness %q is not one of: %s", name, strings.Join(names, ", "))
}

// Modes of a launch.
const (
	ModeTab     = "tab"     // a new tab or window in the host the caller is inside
	ModeSession = "session" // exec a new tmux session; the caller's terminal becomes it
	ModeInPlace = "inplace" // exec the supervisor in this terminal: no multiplexer
)

// Plan is what Launch will do.
type Plan struct {
	Harness string `json:"harness"`
	Host    string `json:"host"` // herdr, tmux or solo
	Mode    string `json:"mode"`
	Cwd     string `json:"cwd"`
	Session string `json:"session,omitempty"` // ModeSession: the tmux session
	// Supervisor is the whole process: rota keepalive run, then the agent.
	Supervisor []string `json:"command"`
}

// Error is a launch that cannot happen: Code is "refused" (the state forbids
// it; Hint says how to get there), "unavailable" (a tool is missing or failed)
// or "config" (a config key holds a bad value).
type Error struct{ Code, Msg, Hint string }

func (e *Error) Error() string { return e.Msg }

// Env is everything the launcher touches outside its own memory.
type Env struct {
	Getenv   func(string) string
	LookPath func(string) (string, error)
	// Host builds the host for a kind, "herdr" or "tmux".
	Host func(kind string) host.Host
	// Exec replaces this process, as syscall.Exec does. It returns only on failure.
	Exec func(path string, argv, env []string) error
	Env  []string // the environment Exec passes on
	// Self is the rota binary, so the tab runs this very build.
	Self string
}

// Resolve plans the launch for the project at root: harness from
// orchestrator.harness, host from where the caller is and from work.dispatch.
func (e Env) Resolve(root string, cfg any) (Plan, error) {
	name := str(cfg, "orchestrator.harness")
	if name == "" {
		name = DefaultHarness
	}
	h, err := Lookup(name)
	if err != nil {
		return Plan{}, &Error{Code: "config", Msg: err.Error(), Hint: "fix it with: rota config set orchestrator.harness claude"}
	}
	agent, err := h.Command(root, cfg)
	if err != nil {
		return Plan{}, &Error{Code: "config", Msg: err.Error()}
	}
	sup := append([]string{e.Self, "keepalive", "run", "--first-prompt", h.Prompt, "--"}, agent...)
	p := Plan{Harness: h.Name, Cwd: root, Supervisor: sup}

	herdr, tmux := e.Host("herdr"), e.Host("tmux")
	switch dispatch := str(cfg, "work.dispatch"); {
	case herdr.InSession() && herdr.Require() == nil:
		p.Host, p.Mode = "herdr", ModeTab
	case tmux.InSession():
		p.Host, p.Mode = "tmux", ModeTab
	case dispatch == "herdr":
		// herdr has no startup command to exec into, and its own guide forbids
		// driving its server from outside a managed pane.
		return p, &Error{Code: "refused", Msg: "work.dispatch=herdr needs rota to run inside a herdr pane",
			Hint: fmt.Sprintf("open herdr, then run rota in a pane at %s; the orchestrator tab opens beside it", root)}
	case dispatch == "tmux" && tmux.Require() != nil:
		return p, &Error{Code: "unavailable", Msg: "work.dispatch=tmux but tmux is not installed"}
	case tmux.Require() == nil:
		p.Host, p.Mode, p.Session = "tmux", ModeSession, "rota-"+sessionName(root)
	default:
		p.Host, p.Mode = host.Solo, ModeInPlace
	}
	return p, nil
}

// sessionName is the tmux session for a project: tmux rejects '.' and ':'.
func sessionName(root string) string {
	return strings.Map(func(r rune) rune {
		if r == '.' || r == ':' || r == ' ' {
			return '-'
		}
		return r
	}, filepath.Base(root))
}

// Launched is what a launch that returned left behind.
type Launched struct {
	Handle string // the tab or window; empty for a mode that replaces the process
}

// Launch carries the plan out. ModeSession and ModeInPlace replace this
// process and return only on failure.
func (e Env) Launch(ctx context.Context, p Plan) (Launched, error) {
	line := shlex.Join(p.Supervisor)
	switch p.Mode {
	case ModeTab:
		o, ok := e.Host(p.Host).(host.TabOpener)
		if !ok {
			return Launched{}, &Error{Code: "unavailable", Msg: p.Host + " host cannot open a tab"}
		}
		tab, err := o.OpenTab(ctx, host.TabOpts{Label: Label, Cwd: p.Cwd, Command: line})
		if err != nil {
			return Launched{}, &Error{Code: "unavailable", Msg: err.Error()}
		}
		return Launched{Handle: tab}, nil
	case ModeSession:
		tmux, err := e.LookPath("tmux")
		if err != nil {
			return Launched{}, &Error{Code: "unavailable", Msg: "tmux is not installed"}
		}
		// -A attaches when the session exists; its command is then ignored, and
		// the keepalive lease refuses a second orchestrator if one asks for it.
		// The shell after the command keeps a failure message on screen.
		argv := []string{"tmux", "new-session", "-A", "-s", p.Session, "-c", p.Cwd, "-n", Label, line + `; exec "${SHELL:-sh}"`}
		return Launched{}, e.exec(tmux, argv)
	default:
		return Launched{}, e.exec(p.Supervisor[0], p.Supervisor)
	}
}

func (e Env) exec(path string, argv []string) error {
	if err := e.Exec(path, argv, e.Env); err != nil {
		return &Error{Code: "unavailable", Msg: fmt.Sprintf("cannot start %s: %v", argv[0], err)}
	}
	return nil
}
