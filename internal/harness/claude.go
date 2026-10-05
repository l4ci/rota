package harness

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/shlex"
)

// claude is Claude Code. Its payloads go unsigned: the worker contract holds
// the provenance rule, there is no key and no hook.
type claude struct{}

func (claude) Kind() string       { return Claude }
func (claude) CommandKey() string { return "work.workerCommand" }

// Launch is work.workerCommand, else the default launch line. Workers run with
// permissions skipped: the contract asks them to git add, git commit, gh pr
// create and run tests, every one of which prompts under a narrower mode with
// nobody in the pane to answer. Scope, not gating, bounds a worker: it owns a
// throwaway branch in its own worktree, and the gate re-verifies the merged
// tree before anything reaches the cycle branch. Override via
// work.workerCommand to narrow it; NEEDS-PERMISSION stays in the classifier
// for exactly that case, so a narrowed mode stalls loudly.
//
// A model chosen for the dispatch (a round's tier, C9) replaces models.worker
// in the default command and fills the {model} placeholder of a custom one; a
// custom command without the placeholder runs as written (ModelApplies).
func (claude) Launch(cfg any, chosen string) (string, error) {
	model := "sonnet"
	if s := config.StringIfSet(cfg, "models.worker"); s != "" {
		model = s
	}
	if chosen != "" {
		model = chosen
	}
	if s := config.StringIfSet(cfg, "work.workerCommand"); s != "" {
		return strings.ReplaceAll(s, ModelPlaceholder, model), nil
	}
	return "claude --model " + model + " --dangerously-skip-permissions", nil
}

// NeedsModel is always true: a claude launch takes its model from the tier map.
func (claude) NeedsModel(any) bool { return true }

// ModelApplies: the default command always takes the model, a custom
// work.workerCommand only through the {model} placeholder.
func (c claude) ModelApplies(cfg any) bool { return placeholderApplies(cfg, c.CommandKey()) }

func placeholderApplies(cfg any, key string) bool {
	if s := config.StringIfSet(cfg, key); s != "" {
		return strings.Contains(s, ModelPlaceholder)
	}
	return true
}

var shortResume = regexp.MustCompile(`^-[A-Za-z]*[cr][A-Za-z]*$`)

// Resume returns the first token after the claude binary that reopens the
// previous conversation in the "fresh" session, which would undo the reset. A
// wrapper's own `-c` is not ours to judge: only tokens after the binary count.
// Quoted arguments are scanned too (`sh -c "claude -c"`), as are `--resume=x`
// and short clusters (`-cr`). An error means the command cannot be parsed.
func (claude) Resume(launch string) (ResumeHit, error) {
	tok, err := ResumeFlag(launch)
	return ResumeHit{Token: tok}, err
}

// ResumeFlag is Resume's scan of a claude command line.
func ResumeFlag(cmd string) (string, error) {
	toks, err := shlex.Split(cmd)
	if err != nil {
		return "", err
	}
	seen := false
	for _, t := range toks {
		switch {
		case seen:
			if t == "--continue" || t == "--resume" || strings.HasPrefix(t, "--continue=") ||
				strings.HasPrefix(t, "--resume=") || shortResume.MatchString(t) {
				return t, nil
			}
		case filepath.Base(t) == "claude":
			seen = true
		case strings.ContainsAny(t, " \t\n\r\f\v"):
			hit, err := ResumeFlag(t)
			if err != nil {
				return "", err
			}
			if hit != "" {
				return hit, nil
			}
		}
	}
	return "", nil
}

func (claude) SoloRefusal() string { return "" }

func (claude) WorkAccounts() bool { return true }

// AccountEnv is the pane's CLAUDE_CONFIG_DIR.
func (claude) AccountEnv(a Account) []string {
	if a.ConfigDir == "" {
		return nil
	}
	return []string{"CLAUDE_CONFIG_DIR=" + a.ConfigDir}
}

func (claude) CheckLaunch(string) error { return nil }

func (claude) Preflight(context.Context, Probe, PreflightOpts) (Setup, error) { return Setup{}, nil }

func (claude) Prepare(launch string, _ func() (string, error), _ Setup) (string, []byte, error) {
	return launch, nil, nil
}

func (claude) RelayKey(func() (string, error), string) ([]byte, error) { return nil, nil }

func (claude) Sign(_ []byte, payload string) string { return payload }

// The orchestrator facet: a fresh session on models.orchestrator in `auto`
// mode (the orchestrator merges and talks to the user, so it keeps a gate the
// workers do not), or work.operatorCommand when set.
func (claude) Name() string   { return Claude }
func (claude) Prompt() string { return "/rota-orchestrate" }
func (claude) Command(cfg any) ([]string, error) {
	if s := config.StringIfSet(cfg, "work.operatorCommand"); s != "" {
		argv, err := shlex.Split(s)
		if err != nil || len(argv) == 0 {
			return nil, fmt.Errorf("work.operatorCommand is not a command line: %q", s)
		}
		return argv, nil
	}
	model := config.StringIfSet(cfg, "models.orchestrator")
	if model == "" {
		model = "opus"
	}
	return []string{"claude", "--model", model, "--permission-mode", "auto"}, nil
}

// hermes and opencode run the orchestrator only. They take no positional
// prompt, so Command ends in the flag that does: the supervisor appends the
// prompt as the last argument, first start and restarts alike. `-s` preloads
// the skill in Hermes (a project skill loads only after `hermes skills
// trust`); opencode loads skills through a model-side tool, so its prompt
// names the skill in words.
type hermes struct{}

func (hermes) Name() string { return "hermes" }
func (hermes) Prompt() string {
	return "You are the orchestrator: run the rota-orchestrate skill."
}
func (hermes) Command(any) ([]string, error) {
	return []string{"hermes", "chat", "-s", "rota-orchestrate", "-q"}, nil
}

type opencode struct{}

func (opencode) Name() string { return "opencode" }
func (opencode) Prompt() string {
	return "You are the orchestrator: load the rota-orchestrate skill and follow it."
}
func (opencode) Command(any) ([]string, error) { return []string{"opencode", "--prompt"}, nil }
