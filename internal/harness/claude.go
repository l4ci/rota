package harness

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/shlex"
)

// claude is Claude Code. Its payloads go unsigned: the worker contract holds
// the provenance rule, there is no key and no hook.
type claude struct{}

func (claude) Kind() string       { return Claude }
func (claude) CommandKey() string { return config.RoundCommandsClaudeKey }

// Launch is round.commands.claude, else the default launch line. Workers run with
// permissions skipped: the contract asks them to git add, git commit, gh pr
// create and run tests, every one of which prompts under a narrower mode with
// nobody in the pane to answer. Scope, not gating, bounds a worker: it owns a
// throwaway branch in its own worktree, and the gate re-verifies the merged
// tree before anything reaches the cycle branch. Override via
// round.commands.claude to narrow it; NEEDS-PERMISSION stays in the classifier
// for exactly that case, so a narrowed mode stalls loudly.
//
// A model chosen for the dispatch (a round's tier, C9) replaces round.tiers.claude.standard
// in the default command and fills the {model} placeholder of a custom one; a
// custom command without the placeholder runs as written (ModelApplies).
func (claude) Launch(cfg Config, chosen string) (string, error) {
	model := cfg.StandardModel
	if chosen != "" {
		model = chosen
	} else if inheritsModel(cfg) {
		model = ""
	}
	if s := cfg.Command; s != "" {
		if model == "" {
			// Stripping the flag leaves a gap; collapse it on this path only.
			s = strings.Join(strings.Fields(modelFlag.ReplaceAllString(s, "")), " ")
		}
		return strings.ReplaceAll(s, ModelPlaceholder, model), nil
	}
	if model == "" {
		return "claude --dangerously-skip-permissions", nil
	}
	return "claude --model " + model + " --dangerously-skip-permissions", nil
}

// modelFlag is `--model {model}` (or `--model={model}`) in a custom command,
// dropped when round.profile is inherit so the launch carries no model.
var modelFlag = regexp.MustCompile(`--model[ =]` + regexp.QuoteMeta(ModelPlaceholder))

// inheritsModel is whether round.profile is inherit: no tier names a model.
func inheritsModel(cfg Config) bool {
	return cfg.Profile == ProfileInherit
}

// NeedsModel: a claude launch takes its model from the tier map, except under
// the inherit profile, which launches without --model.
func (claude) NeedsModel(cfg Config) bool { return !inheritsModel(cfg) }

// ModelApplies: the default command always takes the model, a custom
// round.commands.claude only through the {model} placeholder.
func (c claude) ModelApplies(cfg Config) bool { return placeholderApplies(cfg) }

func placeholderApplies(cfg Config) bool {
	if s := cfg.Command; s != "" {
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

func (claude) RawPaste() bool { return false }

func (claude) PromptAcknowledgement() PromptAcknowledgement { return PromptAcknowledgement{} }

// AccountEnv is the pane's CLAUDE_CONFIG_DIR.
func (claude) AccountEnv(a Account) []string {
	if a.ConfigDir == "" {
		return nil
	}
	return []string{"CLAUDE_CONFIG_DIR=" + a.ConfigDir}
}

func (claude) AmbientAccount(getenv func(string) string) Account {
	return Account{ConfigDir: getenv("CLAUDE_CONFIG_DIR")}
}

func (claude) Accounts(cfg Config) AccountList { return cfg.AccountList }

// UsageMeter: Anthropic's usage endpoint reads an account's headroom.
func (claude) UsageMeter() bool { return true }

func (claude) CrossReviewer() bool { return true }

// A turn in flight (a running command or monitor) shows `esc to interrupt` on
// the status line while the pane itself may sit still between captures.
func (claude) PaneVocab() PaneVocab {
	return PaneVocab{
		// The bare status form is anchored to a line start so a pane quoting it
		// mid-line (a doc, a diff, a brief) is not logged out.
		LoggedOut: `Login expired|(?m:^\s*Not logged in\s*·\s*Please run /login)`,
		Busy:      []string{`(?m)(?:\(|· )[^()\n]*esc to interrupt\s*(?:·[^()\n]*)?\)?\s*$`, `(?m)^\s*⎿\s+Running…`},
		// The footer counts the session's own background tasks:
		// `⏵⏵ bypass permissions on · 1 shell, 2 monitors`.
		Background: []string{`(?m)^\s*⏵⏵[^\n]*[·,]\s*[1-9]\d* (?:shell|monitor)s?\b`},
	}
}

// Tiers: claude must name every tier; round.tiers.claude.standard defaults to
// sonnet, so an unset tier map still launches a model.
func (claude) Tiers() TierRule {
	return TierRule{Required: true, Profile: true}
}

// Configured: Claude is every project's baseline worker.
func (claude) Configured(Config) bool { return true }

// ClaudeIntegration is the name of Claude's herdr integration.
const ClaudeIntegration = Claude

// ExplorerTools is the Claude tool allowlist of the explorer role. Bash stays
// (git log, rg), so it is read-mostly rather than strictly read-only.
const ExplorerTools = "Read, Grep, Glob, Bash"

// AgentFiles is .claude/agents/rota-<role>.md.
func (claude) AgentFiles(r AgentRole, model string) ([]AgentFile, error) {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("name: rota-" + r.Name + "\n")
	b.WriteString("description: " + strconv.Quote(r.Description) + "\n")
	if model == "" {
		model = "inherit" // the subagent runs on its parent's model
	}
	b.WriteString("model: " + model + "\n")
	if r.Effort != "" {
		b.WriteString("effort: " + r.Effort + "\n")
	}
	if r.Name == "explorer" {
		b.WriteString("tools: " + ExplorerTools + "\n")
	}
	b.WriteString("---\n")
	b.WriteString("<!-- " + AgentMarker + " (rota agents write) from roles." + r.Name + " in .rota/config.json; do not edit -->\n\n")
	b.WriteString(r.Instructions + "\n")
	return []AgentFile{{".claude/agents/rota-" + r.Name + ".md", b.String()}}, nil
}

// Doctor: claude has no check of its own.
func (claude) Doctor() Doctor { return Doctor{} }

func (claude) CheckLaunch(string) error { return nil }

func (claude) Preflight(context.Context, Probe, PreflightOpts) (Setup, error) { return Setup{}, nil }

func (claude) Prepare(launch string, _ func() (string, error), _ Setup) (string, []byte, error) {
	return launch, nil, nil
}

func (claude) RelayKey(func() (string, error), string) ([]byte, error) { return nil, nil }

func (claude) Sign(_ []byte, payload string) string { return payload }

// The orchestrator facet: a fresh session on orchestrator.model in `auto`
// mode (the orchestrator merges and talks to the user, so it keeps a gate the
// workers do not), or orchestrator.command when set.
func (claude) Name() string                         { return Claude }
func (claude) Prompt() string                       { return "/rota-orchestrate" }
func (claude) Command(cfg Config) ([]string, error) { return orchestratorCommand(cfg, false) }

// OrchestratorCommand is the one builder of the claude orchestrator command
// line, for `rota orchestrate` and the round's operator session alike. A set
// orchestrator.command wins as written; both refuse one that is not a command
// line. resume adds --continue to the default line.
func OrchestratorCommand(cfg any, resume bool) ([]string, error) {
	return orchestratorCommand(ConfigFor(cfg, Claude), resume)
}

func orchestratorCommand(cfg Config, resume bool) ([]string, error) {
	if s := cfg.OrchestratorCommand; s != "" {
		argv, err := shlex.Split(s)
		if err != nil || len(argv) == 0 {
			return nil, fmt.Errorf("orchestrator.command is not a command line: %q", s)
		}
		return argv, nil
	}
	model := cfg.OrchestratorModel
	argv := []string{"claude"}
	if resume {
		argv = append(argv, "--continue")
	}
	return append(argv, "--model", model, "--permission-mode", "auto"), nil
}

// OrchestratorCommandLine is OrchestratorCommand as one shell line. A custom
// orchestrator.command is validated by the same split but returned as typed, so
// `~`, `$VAR`, `&&` and env assignments keep their shell meaning.
func OrchestratorCommandLine(raw any, resume bool) (string, error) {
	cfg := ConfigFor(raw, Claude)
	argv, err := orchestratorCommand(cfg, resume)
	if err != nil {
		return "", err
	}
	if s := cfg.OrchestratorCommand; s != "" {
		return s, nil
	}
	return shlex.Join(argv), nil
}

// hermes runs the orchestrator only. It takes no positional prompt, so
// Command ends in the flag that does: the supervisor appends the prompt as the
// last argument, first start and restarts alike. `-s` preloads the skill (a
// project skill loads only after `hermes skills trust`).
type hermes struct{}

func (hermes) Name() string { return "hermes" }

// WorkAccounts: round.accounts is Anthropic's.
func (hermes) WorkAccounts() bool { return false }
func (hermes) Prompt() string {
	return "You are the orchestrator: run the rota-orchestrate skill."
}
func (hermes) Command(Config) ([]string, error) {
	return []string{"hermes", "chat", "-s", "rota-orchestrate", "-q"}, nil
}
