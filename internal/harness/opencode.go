package harness

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/rotastate"
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

// RawPaste: OpenCode's TUI drops the CRs that tmux pastes for line feeds.
func (opencode) RawPaste() bool { return true }

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

// PaneVocab is OpenCode's own wording. The limit texts are the account caps of
// its provider layer (packages/ai/src/provider-error.ts) and its dead-key text;
// the retryable throttles ("rate limit", "too many requests") are left out:
// they print beside "retrying in", which reads as busy. They apply to opencode
// slots only, so a Claude or Codex pane that echoes them stays as it was.
func (opencode) PaneVocab() PaneVocab {
	return PaneVocab{
		Retry: []string{`retrying in`},
		Busy:  []string{`esc interrupt`},
		Limits: []string{
			`insufficient (?:quota|balance)`,
			`quota exceeded`,
			`budget exceeded`,
			`limit exhausted`,
			`package has expired`,
			`API key has expired`,
		},
		AuthDead: `API key has expired`,
		BadModel: `Model (\S+) is not valid`,
		Gutter:   true,
	}
}

// Tiers: the tier map is optional, unset a worker runs on OpenCode's own
// default model, but a configured one names every tier.
func (opencode) Tiers() TierRule { return TierRule{} }

// Configured is whether the project uses OpenCode as a worker: an opencode
// tier map, a named account, a custom command or it as the default kind.
func (o opencode) Configured(cfg any) bool {
	if config.StringIfSet(cfg, "round.workerKind") == OpenCode {
		return true
	}
	if TierMapSet(cfg, OpenCode) {
		return true
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

// OpencodeInstallHint is the doctor hint for a missing or unrunnable opencode.
const OpencodeInstallHint = "install opencode (https://opencode.ai) and put it on PATH"

// Block reasons of an opencode worker that cannot start.
const (
	BlockOpencodeFlags  = "opencode flags"
	BlockOpencodeBinary = "opencode binary"
)

func opencodeEnv(home string) []string {
	if home == "" {
		return nil
	}
	return []string{"OPENCODE_CONFIG_DIR=" + home}
}

// opencodeVersion runs `opencode --version` under home; the result is for
// display only (DECISIONS, "No harness version pins").
func opencodeVersion(ctx context.Context, p Probe, bin, home string) (string, bool) {
	r, err := p.Run(ctx, bin, []string{"--version"}, opencodeEnv(home))
	if err != nil || r.ExitCode != 0 {
		return "", false
	}
	return strings.TrimSpace(firstLine(r.Stdout)), true
}

func firstLine(s string) string {
	l, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return l
}

// Doctor: the binary, its version for display and, under herdr, the
// integration that reports the TUI's state. No login check: credentials are
// the user's own provider setup and a key can live in the environment. The
// PreToolUse guard hook is Claude's, so OpenCode workers are unguarded.
func (opencode) Doctor() Doctor {
	return Doctor{
		Check: "opencode", Title: "OpenCode", Binary: "opencode", InstallHint: OpencodeInstallHint,
		Runnable: opencodeVersion,
		Home: func(ctx context.Context, p Probe, _, herdr string, h Home) []Finding {
			if herdr == "" {
				return nil
			}
			st, err := p.Run(ctx, herdr, []string{"integration", "status"}, opencodeEnv(h.Dir))
			switch {
			case err != nil:
				return []Finding{{Code: IntegrationUnrunnable, Slot: h.Slot, Dir: h.Dir, Err: err}}
			case st.ExitCode != 0 || ParseIntegration(st.Stdout, OpenCode) != "current":
				return []Finding{{Code: IntegrationStale, Slot: h.Slot, Dir: h.Dir}}
			}
			return nil
		},
		Unguarded: true, IntegrationFix: "herdr integration install opencode", HomeEnv: "OPENCODE_CONFIG_DIR=",
	}
}

// modelsTimeout bounds `opencode models`, which asks the network.
const modelsTimeout = 10 * time.Second

// launchModel is the model a launch line selects (`--model x`, `-m x`,
// `--model=x`), "" for none.
func launchModel(launch string) string {
	toks, err := shlex.Split(launch)
	if err != nil {
		return ""
	}
	for i, t := range toks {
		switch {
		case (t == "--model" || t == "-m") && i+1 < len(toks):
			return toks[i+1]
		case strings.HasPrefix(t, "--model="):
			return strings.TrimPrefix(t, "--model=")
		}
	}
	return ""
}

// CheckLaunch refuses a command that would keep the prompt guard out:
// `--pure` runs OpenCode without external plugins, and a launch that sets
// OPENCODE_CONFIG_CONTENT itself would be replaced by (or replace) the guard's.
func (o opencode) CheckLaunch(launch string) error {
	toks, err := shlex.Split(launch)
	if err != nil {
		return refuse(Usage, "%s cannot be parsed: %s", o.CommandKey(), launch)
	}
	for _, t := range toks {
		if t == "--pure" || strings.HasPrefix(t, opencodeConfigContent+"=") {
			return refuse(Unavailable, "%s holds %s, which would keep rota's prompt guard out of the session: %s", o.CommandKey(), strings.SplitN(t, "=", 2)[0], launch)
		}
	}
	return nil
}

// Preflight is everything an opencode worker needs before anything is marked
// or killed, shared by `worker dispatch` and `round assign`: opencode installed
// (Refused, blockedBy BlockOpencodeBinary), every flag of the launch line
// listed by `opencode --help` (Refused, blockedBy BlockOpencodeFlags), the
// slot's account and its state directory. A tier model that `opencode models`
// does not list, and a herdr without the opencode integration, are warnings:
// the model list is a network call and the integration only sharpens state.
// Both hosts work; it never writes into an OpenCode config dir.
func (o opencode) Preflight(ctx context.Context, p Probe, po PreflightOpts) (Setup, error) {
	var set Setup
	bin, ok := p.Look("opencode")
	if !ok {
		r := refuse(Refused, "opencode is not installed or not on PATH")
		r.BlockedBy = BlockOpencodeBinary
		r.Hint = OpencodeInstallHint
		return set, r
	}
	cd, err := po.CommonDir()
	if err != nil {
		return set, err
	}
	set.StateDir = OpencodeSlotDir(cd, po.Slot)
	set.Worktree = po.Worktree
	acct, _ := PickCodexAccount(po.Accounts, po.Account, po.Load)
	if acct.Name != "" && acct.Home == "" {
		return set, refuse(Usage, "work.opencodeAccounts account '%s' has no configDir", acct.Name)
	}
	set.Home, set.Account = acct.Home, acct.Name
	if err := os.MkdirAll(set.StateDir, 0o700); err != nil {
		return set, refuse(Unavailable, "cannot create the slot state directory %s: %v", set.StateDir, err)
	}
	env := opencodeEnv(acct.Home)
	v, ran := opencodeVersion(ctx, p, bin, acct.Home)
	if !ran {
		r := refuse(Refused, "opencode is not runnable: `opencode --version` failed")
		r.BlockedBy = BlockOpencodeBinary
		r.Hint = OpencodeInstallHint
		return set, r
	}
	set.Version = v
	if miss := checkFlags(ctx, p, bin, env, LaunchFlags(po.Launch)); len(miss) > 0 {
		names := make([]string, len(miss))
		for i, m := range miss {
			names[i] = m.Flag
		}
		r := refuse(Refused, "opencode --help does not list %s, which the launch line uses; update opencode or change work.opencodeCommand", strings.Join(names, ", "))
		r.BlockedBy = BlockOpencodeFlags
		return set, r
	}
	if po.Herdr {
		if herdr, ok := p.Look("herdr"); ok {
			for _, f := range o.Doctor().Home(ctx, p, bin, herdr, Home{Slot: acct.Name, Dir: acct.Home}) {
				if f.Code == IntegrationStale {
					set.Warnings = append(set.Warnings, "herdr's opencode integration is not current: run `herdr integration install opencode`")
				}
			}
		}
	}
	if m := launchModel(po.Launch); m != "" {
		mctx, cancel := context.WithTimeout(ctx, modelsTimeout)
		defer cancel()
		if r, err := p.Run(mctx, bin, []string{"models"}, env); err == nil && r.ExitCode == 0 && mctx.Err() == nil && strings.TrimSpace(r.Stdout) != "" &&
			!slices.Contains(strings.Fields(r.Stdout), m) {
			set.Warnings = append(set.Warnings, fmt.Sprintf("model %s is not in `opencode models`: OpenCode will refuse it and fall back to its last-used model", m))
		}
	}
	return set, nil
}

// opencodeGuard is the per-slot prompt guard plugin (see the file).
//
//go:embed opencode_guard.js
var opencodeGuard []byte

// GuardFile is the plugin Prepare writes into the slot's state directory.
const GuardFile = "rota-guard.js"

const opencodeConfigContent = "OPENCODE_CONFIG_CONTENT"

// Prepare writes the prompt guard and a fresh signing key into the slot's
// state directory and puts the guard on the launch line as an inline config
// (OPENCODE_CONFIG_CONTENT), so no OpenCode config dir is touched. Each task
// dispatch rotates the key; the plugin reads it per prompt.
func (o opencode) Prepare(launch string, _ func() (string, error), s Setup) (string, []byte, error) {
	if err := os.WriteFile(filepath.Join(s.StateDir, GuardFile), opencodeGuard, 0o600); err != nil {
		return "", nil, refuse(Unavailable, "cannot write the prompt guard in %s: %v", s.StateDir, err)
	}
	_, key, err := newPromptKey(s.StateDir)
	if err != nil {
		return "", nil, refuse(Unavailable, "cannot write the prompt key in %s: %v", s.StateDir, err)
	}
	guard, err := filepath.Abs(filepath.Join(s.StateDir, GuardFile))
	if err != nil {
		return "", nil, refuse(Unavailable, "cannot place the prompt guard: %v", err)
	}
	content, _ := json.Marshal(map[string][]string{"plugin": {(&url.URL{Scheme: "file", Path: guard}).String()}})
	return opencodeConfigContent + "=" + shlex.QuoteAlways(string(content)) + " " + launch, key, nil
}

// RelayKey loads the key the session's task dispatch wrote.
func (opencode) RelayKey(commonDir func() (string, error), slot string) ([]byte, error) {
	cd, err := commonDir()
	if err != nil {
		return nil, err
	}
	keyFile := filepath.Join(OpencodeSlotDir(cd, slot), PromptKeyFile)
	key, err := LoadPromptKey(keyFile)
	if err != nil {
		r := refuse(Unavailable, "slot '%s' is an opencode worker but its prompt key is unreadable (%s): %v", slot, keyFile, err)
		r.Hint = "re-dispatch the task: a fresh session gets a fresh key and guard"
		return nil, r
	}
	return key, nil
}

// Sign ends payload with its ROTA-SIG trailer.
func (opencode) Sign(key []byte, payload string) string { return signPrompt(key, payload) }

// OpencodeSlotDir is the slot's own state directory,
// <git-common-dir>/rota/opencode/<slot>: the prompt key and the guard plugin.
// OpenCode never gets it as a config dir, and it stays out of the worktree.
func OpencodeSlotDir(commonDir, slot string) string {
	return filepath.Join(rotastate.OpencodeDir(commonDir), slot)
}

// The orchestrator facet: opencode takes no positional prompt, so Command ends
// in the flag that does and the supervisor appends the prompt as the last
// argument. It loads skills through a model-side tool, so its prompt names the
// skill in words.
func (opencode) Name() string { return OpenCode }
func (opencode) Prompt() string {
	return "You are the orchestrator: load the rota-orchestrate skill and follow it."
}
func (opencode) Command(any) ([]string, error) { return []string{"opencode", "--prompt"}, nil }
