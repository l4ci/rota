package harness

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/shlex"
)

// OpenCode workers (#584). The spike is .rota/spikes/opencode-worker.md.

// DefaultOpencodeCommand is the launch line of an opencode worker. {model}
// takes the tier's `provider/model` id; with no model the pair is dropped
// (Launch) and OpenCode picks its own.
const DefaultOpencodeCommand = "opencode --model {model}"

// opencode is the OpenCode CLI (sst/opencode). Its TUI takes the brief pasted
// after boot, like Claude Code. It has no usage meter: a provider is billed
// per token, so a quota error is a limit without a reset clock. It is also the
// orchestrator in `orchestrator.harness: opencode` (Name, Prompt, Command).
type opencode struct{}

func (opencode) Kind() string       { return OpenCode }
func (opencode) CommandKey() string { return "work.opencodeCommand" }

// Launch is work.opencodeCommand, else the default launch line. A model fills
// {model}. The default without a model drops its `--model {model}` pair; a
// custom command that still holds {model} then has nothing to put there, which
// is a usage error. A custom command without the placeholder runs as written.
func (o opencode) Launch(cfg any, model string) (string, error) {
	cmd, custom := DefaultOpencodeCommand, false
	if s := config.StringIfSet(cfg, o.CommandKey()); s != "" {
		cmd, custom = s, true
	}
	if model == "" {
		if custom {
			if strings.Contains(cmd, ModelPlaceholder) {
				return "", refuse(Usage, "work.opencodeCommand holds {model} but no model was chosen: pass a tier model (round assign) or drop the placeholder")
			}
			return cmd, nil
		}
		cmd = strings.Replace(cmd, " --model "+ModelPlaceholder, "", 1)
	}
	return strings.ReplaceAll(cmd, ModelPlaceholder, model), nil
}

// NeedsModel: only a custom work.opencodeCommand that holds {model} does.
func (o opencode) NeedsModel(cfg any) bool {
	_, err := o.Launch(cfg, "")
	return err != nil
}

// ModelApplies: the default command always takes the model.
func (o opencode) ModelApplies(cfg any) bool { return placeholderApplies(cfg, o.CommandKey()) }

// opencodeResume are the flags that reopen an earlier session (`--prompt`,
// `--model` and `--agent` do not).
var opencodeResume = []string{"-c", "--continue", "-s", "--session", "--fork"}

// Resume returns the first token after the opencode binary that reopens an
// earlier session, which a fresh task dispatch must not do. `--session=x` is
// read too. Quoted arguments are scanned (`sh -c "opencode -c"`). An error
// means the command cannot be parsed.
func (opencode) Resume(launch string) (ResumeHit, error) {
	tok, err := opencodeResumeFlag(launch)
	return ResumeHit{Token: tok}, err
}

func opencodeResumeFlag(cmd string) (string, error) {
	toks, err := shlex.Split(cmd)
	if err != nil {
		return "", err
	}
	seen := false
	for _, t := range toks {
		switch {
		case seen:
			flag, _, _ := strings.Cut(t, "=")
			for _, r := range opencodeResume {
				if flag == r {
					return t, nil
				}
			}
		case filepath.Base(t) == "opencode":
			seen = true
		case strings.ContainsAny(t, " \t\n\r\f\v"):
			hit, err := opencodeResumeFlag(t)
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

// SoloRefusal: solo runs Claude subagents only.
func (opencode) SoloRefusal() string {
	return "solo round: workers are Claude subagents; an OpenCode subagent cannot be given the slot's worktree"
}

// WorkAccounts: work.accounts is Anthropic's.
func (opencode) WorkAccounts() bool { return false }

// AccountEnv is the slot's OPENCODE_CONFIG_DIR, set only for a configured
// account: the default config is OpenCode's own. Credentials stay shared
// (~/.local/share/opencode/auth.json): the config dir moves config, not login.
func (opencode) AccountEnv(a Account) []string {
	if a.Home == "" {
		return nil
	}
	return []string{"OPENCODE_CONFIG_DIR=" + a.Home}
}

func (opencode) Accounts(cfg any) AccountList {
	l := AccountList{Key: "work.opencodeAccounts"}
	for _, a := range config.OpencodeAccounts(cfg) {
		l.Entries = append(l.Entries, AccountEntry{Name: a.Name, Dir: a.ConfigDir})
	}
	return l
}

// UsageMeter: OpenCode has no usage reading; a provider is billed per token.
func (opencode) UsageMeter() bool { return false }

func (opencode) LimitPhrases() []string { return SharedLimitPhrases() }

// Tiers: the tier map is optional, unset a worker runs on OpenCode's own
// default model, but a configured one names every tier.
func (opencode) Tiers() TierRule { return TierRule{} }

// Configured is whether the project uses OpenCode as a worker: an opencode
// tier map, a named account, a custom command or it as the default kind.
func (o opencode) Configured(cfg any) bool {
	if config.StringIfSet(cfg, "round.workerKind") == OpenCode {
		return true
	}
	for _, tier := range []string{"light", "standard", "heavy"} {
		if strings.TrimSpace(config.StringIfSet(cfg, "round.tiers.opencode."+tier)) != "" {
			return true
		}
	}
	if len(o.Accounts(cfg).Entries) > 0 {
		return true
	}
	return strings.TrimSpace(config.StringIfSet(cfg, o.CommandKey())) != ""
}

// AgentFiles is .opencode/agents/rota-<role>.md. OpenCode agents have no
// effort field, so a role's effort is not written. The explorer may not edit
// or run commands.
func (opencode) AgentFiles(r AgentRole, model string) ([]AgentFile, error) {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("description: " + strconv.Quote(r.Description) + "\n")
	b.WriteString("mode: subagent\n")
	if model != "" {
		b.WriteString("model: " + strconv.Quote(model) + "\n")
	}
	if r.Name == "explorer" {
		b.WriteString("permission:\n  edit: deny\n  bash: deny\n")
	}
	b.WriteString("---\n")
	b.WriteString("<!-- " + AgentMarker + " (rota agents write) from roles." + r.Name + " in .rota/config.json; do not edit -->\n\n")
	b.WriteString(r.Instructions + "\n")
	return []AgentFile{{".opencode/agents/rota-" + r.Name + ".md", b.String()}}, nil
}

func (opencode) Doctor() Doctor { return Doctor{} }

func (opencode) CheckLaunch(string) error { return nil }

func (opencode) Preflight(context.Context, Probe, PreflightOpts) (Setup, error) { return Setup{}, nil }

func (opencode) Prepare(launch string, _ func() (string, error), _ Setup) (string, []byte, error) {
	return launch, nil, nil
}

func (opencode) RelayKey(func() (string, error), string) ([]byte, error) { return nil, nil }

func (opencode) Sign(_ []byte, payload string) string { return payload }

// The orchestrator facet: opencode takes no positional prompt, so Command ends
// in the flag that does and the supervisor appends the prompt as the last
// argument. It loads skills through a model-side tool, so its prompt names the
// skill in words.
func (opencode) Name() string { return OpenCode }
func (opencode) Prompt() string {
	return "You are the orchestrator: load the rota-orchestrate skill and follow it."
}
func (opencode) Command(any) ([]string, error) { return []string{"opencode", "--prompt"}, nil }
