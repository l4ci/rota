package harness

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/l4ci/rota/internal/blockedby"
	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/rotastate"
	"github.com/l4ci/rota/internal/shlex"
)

// Codex workers (E1, #68). The contract is "E: Codex workers" in
// docs/contributing/contract/.

// DefaultCodexCommand is the launch line of a codex worker. The flag list is
// the one thing here that may change with the Codex CLI, so it lives only in
// this constant. {model} takes the tier's model; with no model the pair is
// dropped (Launch). --no-daemon keeps the session in the pane's own process
// tree, so dispatch's kill is provable.
const DefaultCodexCommand = "codex --model {model} --dangerously-bypass-approvals-and-sandbox --dangerously-bypass-hook-trust --no-daemon --no-alt-screen"

// BlockCodexFlags is the blockedBy of a codex worker whose launch line uses a
// flag `codex --help` does not list.
const BlockCodexFlags = blockedby.CodexFlags

const hookTrustFlag = "--dangerously-bypass-hook-trust"

// codex is the Codex CLI. Its account is the default Codex home, or a named
// home of a codex entry of round.accounts, and every payload sent to it is signed: Codex follows unsigned text typed into its
// pane, so a UserPromptSubmit hook blocks anything without the signature.
type codex struct{ promptSigner }

func (codex) Kind() string       { return Codex }
func (codex) CommandKey() string { return config.RoundCommandsCodexKey }

// Launch is round.commands.codex, else the default launch line. A model fills
// {model}. The default without a model drops its `--model {model}` pair and
// Codex picks its own; a custom command that still holds {model} then has
// nothing to put there, which is a usage error. A custom command without the
// placeholder runs as written.
func (c codex) Launch(cfg Config, model string) (string, error) {
	cmd, custom := DefaultCodexCommand, false
	if s := cfg.Command; s != "" {
		cmd, custom = s, true
	}
	if model == "" {
		if custom {
			if strings.Contains(cmd, ModelPlaceholder) {
				return "", refuse(Usage, "round.commands.codex holds {model} but no model was chosen: pass a tier model (round assign) or drop the placeholder")
			}
			return cmd, nil
		}
		cmd = strings.Replace(cmd, "--model "+ModelPlaceholder+" ", "", 1)
	}
	return strings.ReplaceAll(cmd, ModelPlaceholder, model), nil
}

// NeedsModel: only a custom round.commands.codex that holds {model} does (Launch
// refuses it with none).
func (c codex) NeedsModel(cfg Config) bool {
	_, err := c.Launch(cfg, "")
	return err != nil
}

// ModelApplies: the default command always takes the model.
func (c codex) ModelApplies(cfg Config) bool { return placeholderApplies(cfg) }

// Resume returns the first token after the codex binary that is one of its
// `resume` or `fork` subcommands, which reopen a previous session in the
// "fresh" one and would undo the reset. Codex takes options with values
// (`--model x resume`), so the first non-option token is not necessarily the
// subcommand: any token that is exactly one counts. claude's -c and -r mean
// something else to codex (-c is a config override) and are not looked at.
// Quoted arguments are scanned too (`sh -c "codex resume"`). An error means
// the command cannot be parsed.
func (codex) Resume(launch string) (ResumeHit, error) {
	tok, err := CodexResume(launch)
	return ResumeHit{Token: tok, Noun: "the subcommand "}, err
}

// CodexResume is Resume's scan of a codex command line.
func CodexResume(cmd string) (string, error) {
	toks, err := shlex.Split(cmd)
	if err != nil {
		return "", err
	}
	seen := false
	for _, t := range toks {
		switch {
		case seen:
			if t == "resume" || t == "fork" {
				return t, nil
			}
		case filepath.Base(t) == "codex":
			seen = true
		case strings.ContainsAny(t, " \t\n\r\f\v"):
			hit, err := CodexResume(t)
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

// SoloRefusal: solo runs Claude subagents only: a Codex subagent cannot be
// given a working directory (E3, #70), so it would edit the orchestrator's
// checkout.
func (codex) SoloRefusal() string {
	return "solo round: workers are Claude subagents; a Codex subagent cannot be given the slot's worktree"
}

func (codex) WorkAccounts() bool { return false }

func (codex) RawPaste() bool { return false }

func (codex) PromptAcknowledgement() PromptAcknowledgement {
	return PromptAcknowledgement{RequireEcho: true, EmptyPrompt: "Ask Codex to do anything"}
}

// AccountEnv is the slot's CODEX_HOME, set only for a configured account: the
// default home is Codex's own. The claude config dir is not its business, and
// round.accounts is Anthropic's.
func (codex) AccountEnv(a Account) []string {
	if a.Home == "" {
		return nil
	}
	return []string{"CODEX_HOME=" + a.Home}
}

func (codex) AmbientAccount(getenv func(string) string) Account {
	return Account{Home: getenv("CODEX_HOME")}
}

func (codex) Accounts(cfg Config) AccountList { return cfg.AccountList }

// UsageMeter: Codex has no usage reading, so its accounts are balanced from
// the limits the watcher logged.
func (codex) UsageMeter() bool { return false }

func (codex) CrossReviewer() bool { return true }

func (codex) PaneVocab() PaneVocab {
	return PaneVocab{
		// The active status starts with elapsed time; Codex truncates the
		// following interrupt hint to the pane width. Require the duration
		// frame so prose quoting the label does not count as active work.
		Waiting: []string{`(?m)^\s*• Waiting for background terminal \(\d+[hms][^\n]*$`},
	}
}

// Tiers: the tier map is optional (#68), unset a worker runs on Codex's own
// default model, but a configured one names every tier.
func (codex) Tiers() TierRule { return TierRule{} }

// Configured is whether the project uses Codex at all: a Codex tier map, a
// named Codex home, a custom Codex command or Codex as the first round.harnesses entry.
func (c codex) Configured(cfg Config) bool {
	if cfg.ProjectKind == Codex {
		return true
	}
	if cfg.TierMap {
		return true
	}
	if len(c.Accounts(cfg).Entries) > 0 {
		return true
	}
	return strings.TrimSpace(cfg.Command) != ""
}

// CodexEfforts are the model_reasoning_effort values every Codex model lists as
// supported. "max" and "ultra" exist on some models only, so a role asking for
// them is refused rather than written into a file that may not load.
var CodexEfforts = []string{"low", "medium", "high", "xhigh"}

// ErrEffort marks a role effort that Codex cannot express.
var ErrEffort = errors.New("effort not supported by Codex")

// AgentFiles is .codex/agents/rota-<role>.toml.
func (codex) AgentFiles(r AgentRole, model string) ([]AgentFile, error) {
	if r.Effort != "" && !slices.Contains(CodexEfforts, r.Effort) {
		return nil, fmt.Errorf("%w: roles.%s.effort %q is not a Codex model_reasoning_effort (%s)", ErrEffort, r.Name, r.Effort, strings.Join(CodexEfforts, ", "))
	}
	var b strings.Builder
	b.WriteString("# " + AgentMarker + " (rota agents write) from roles." + r.Name + " in .rota/config.json; do not edit\n")
	b.WriteString("name = " + strconv.Quote("rota-"+r.Name) + "\n")
	b.WriteString("description = " + strconv.Quote(r.Description) + "\n")
	if model != "" {
		b.WriteString("model = " + strconv.Quote(model) + "\n")
	}
	if r.Effort != "" {
		b.WriteString("model_reasoning_effort = " + strconv.Quote(r.Effort) + "\n")
	}
	if r.Name == "explorer" {
		b.WriteString("sandbox_mode = \"read-only\"\n")
	}
	b.WriteString("developer_instructions = \"\"\"\n" + r.Instructions + "\n\"\"\"\n")
	return []AgentFile{{".codex/agents/rota-" + r.Name + ".toml", b.String()}}, nil
}

// Doctor: the binary, its login per home and the herdr integration.
func (codex) Doctor() Doctor {
	return Doctor{
		Check: "codex", Title: "Codex", Binary: "codex", InstallHint: CodexInstallHint,
		Runnable: func(ctx context.Context, p Probe, bin, home string) (string, bool) {
			v, f := CheckCodexVersion(ctx, p, bin, home)
			if f != nil {
				return "", false
			}
			if v == (CodexVersion{}) {
				return "", true
			}
			return v.String(), true
		},
		Home: func(ctx context.Context, p Probe, bin, herdr string, h Home) []Finding {
			return CheckCodexHome(ctx, p, bin, herdr, h)
		},
		Unguarded: true, LoginFix: "codex login", IntegrationFix: "herdr integration install codex", HomeEnv: "CODEX_HOME=",
	}
}

// CheckLaunch refuses a command without --dangerously-bypass-hook-trust: Codex
// skips a command-line hook unless it is trusted, which would leave unsigned
// pane text unchecked.
func (c codex) CheckLaunch(launch string) error {
	toks, err := shlex.Split(launch)
	if err == nil {
		i := 0
		for i < len(toks) && envAssignRe.MatchString(toks[i]) {
			i++
		}
		if i < len(toks) {
			for _, t := range toks[i+1:] {
				if t == hookTrustFlag {
					return nil
				}
			}
		}
	}
	return refuse(Unavailable, "%s lacks %s: without it Codex skips rota's prompt-check hook, so unsigned pane text would reach the worker: %s", c.CommandKey(), hookTrustFlag, launch)
}

// Prepare adds the prompt-check hook to the launch line and rotates the
// session's signing key, which lives in the slot's state directory. Folder
// trust and the update check ride on the launch line, so no config.toml is
// written into a Codex home.
func (c codex) Prepare(launch string, exe func() (string, error), s Setup) (string, []byte, error) {
	bin, err := exe()
	if err != nil {
		return "", nil, refuse(Unavailable, "cannot find the rota binary for the prompt-check hook: %v", err)
	}
	keyPath, key, err := writePromptKey(s.StateDir)
	if err != nil {
		return "", nil, err
	}
	out, err := withPromptHook(launch, append(codexLaunchArgs(s.Worktree), CodexHookArgs(bin, keyPath)...))
	if err != nil {
		return "", nil, refuse(Unavailable, "cannot add the prompt-check hook to %s: %v", c.CommandKey(), err)
	}
	return out, key, nil
}

// RelayKey loads the key the session's task dispatch wrote.
func (codex) RelayKey(commonDir func() (string, error), slot string) ([]byte, error) {
	return relayPromptKey(commonDir, CodexSlotDir, slot, "a codex", "re-dispatch the task: a fresh session gets a fresh key and hook")
}

// CodexSlotsDir is where the slots' state directories live, beside the round
// lease, under the git common dir.
func CodexSlotsDir(commonDir string) string { return rotastate.CodexDir(commonDir) }

// CodexSlotDir is the slot's own state directory,
// <git-common-dir>/rota/codex/<slot>. It holds the prompt key and nothing
// Codex reads: a Codex process never gets it as CODEX_HOME. Never inside the
// worktree, where it would dirty git status. Directories left by the
// per-slot homes of earlier releases stay as they are.
func CodexSlotDir(commonDir, slot string) string {
	return filepath.Join(CodexSlotsDir(commonDir), slot)
}

// codexLaunchArgs are the codex flags that keep a dialog out of an unattended
// pane without writing to a Codex home: the slot's worktree trusted, and no
// update check.
func codexLaunchArgs(worktree string) []string {
	var out []string
	if worktree != "" {
		if abs, err := filepath.Abs(worktree); err == nil {
			worktree = abs
		}
		out = append(out, "-c", "projects."+tomlString(worktree)+".trust_level=\"trusted\"")
	}
	return append(out, "-c", "check_for_update_on_startup=false")
}

// PickCodexAccount is the configured account a slot runs under: the one it
// ran under last while that is still configured, else the one holding the
// fewest other slots (ties in config order). Nothing configured is "": the
// default Codex home. A nameless account is skipped.
func PickCodexAccount(accts []HomeAccount, current string, load map[string]int) (HomeAccount, bool) {
	var best HomeAccount
	found := false
	for _, a := range accts {
		if a.Name == "" {
			continue
		}
		if a.Name == current {
			return a, true
		}
		if !found || load[a.Name] < load[best.Name] {
			best, found = a, true
		}
	}
	return best, found
}

// homeEnv is the shell prefix that runs a command in a Codex home: nothing
// for the default home.
func homeEnv(dir string) string {
	if dir == "" {
		return ""
	}
	return "CODEX_HOME=" + dir + " "
}

// Preflight is everything a codex worker needs before anything is marked or
// killed, shared by `worker dispatch` and `round assign`: codex installed
// (Unavailable), every flag of the launch line listed by `codex --help`
// (Refused, blockedBy BlockCodexFlags), a supported host, the account's home (the
// default Codex home, or a configured account's), herdr integration when on herdr,
// and login (Unavailable, hint `[CODEX_HOME=<home> ]codex login`),
// and the slot's state directory. It never writes into a Codex home beyond
// herdr's own integration install, and never touches auth.json.
func (c codex) Preflight(ctx context.Context, p Probe, o PreflightOpts) (Setup, error) {
	var set Setup
	bin, ok := p.Look("codex")
	if !ok {
		return set, refuse(Unavailable, "codex is not installed or not runnable")
	}
	if !o.Herdr && !o.Orca {
		return set, refuse(Unavailable, "codex workers need round.dispatch=herdr or orca")
	}
	herdr := ""
	if o.Herdr {
		var ok bool
		herdr, ok = p.Look("herdr")
		if !ok {
			return set, refuse(Unavailable, "herdr is not installed")
		}
	}

	cd, err := o.CommonDir()
	if err != nil {
		return set, err
	}
	set.StateDir = CodexSlotDir(cd, o.Slot)
	set.Worktree = o.Worktree
	if o.Worktree == "" {
		return set, refuse(Resolution, "slot '%s' has no worktree to trust", o.Slot)
	}
	acct, _ := PickCodexAccount(o.Accounts, o.Account, o.Load)
	home := acct.Home
	if acct.Name != "" && home == "" {
		return set, refuse(Usage, "round.accounts codex account '%s' has no dir", acct.Name)
	}
	set.Home, set.Account = home, acct.Name
	who := "Codex"
	if acct.Name != "" {
		who = fmt.Sprintf("Codex account '%s' (CODEX_HOME=%s)", acct.Name, home)
	}

	if err := os.MkdirAll(set.StateDir, 0o700); err != nil {
		return set, refuse(Unavailable, "cannot create the slot state directory %s: %v", set.StateDir, err)
	}
	v, f := CheckCodexVersion(ctx, p, bin, home)
	if f != nil {
		return set, refuse(Unavailable, "codex is not installed or not runnable: `codex --version` failed")
	}
	if v != (CodexVersion{}) {
		set.Version = v.String()
	}
	if miss := CheckCodexFlags(ctx, p, bin, home, LaunchFlags(o.Launch)); len(miss) > 0 {
		names := make([]string, len(miss))
		for i, m := range miss {
			names[i] = m.Flag
		}
		r := refuse(Refused, "codex --help does not list %s, which the launch line uses; update codex or change round.commands.codex", strings.Join(names, ", "))
		r.BlockedBy = BlockCodexFlags
		return set, r
	}

	for _, f := range CheckCodexHome(ctx, p, bin, herdr, Home{Slot: acct.Name, Dir: home}) {
		switch f.Code {
		case IntegrationUnrunnable:
			return set, refuse(Unavailable, "herdr integration status could not run: %v", f.Err)
		case IntegrationStale:
			var henv []string
			if home != "" {
				henv = []string{"CODEX_HOME=" + home}
			}
			in, err := p.Run(ctx, herdr, []string{"integration", "install", "codex"}, henv)
			if err != nil || in.ExitCode != 0 {
				msg := strings.TrimSpace(in.Stderr)
				if err != nil {
					msg = err.Error()
				}
				x := refuse(Unavailable, "herdr integration install codex failed for %s: %s", who, msg)
				x.Hint = homeEnv(home) + "herdr integration install codex"
				return set, x
			}
		case NotLoggedIn:
			x := refuse(Unavailable, "%s is not logged in", who)
			x.Hint = homeEnv(home) + "codex login"
			return set, x
		}
	}
	return set, nil
}

// The orchestrator facet: codex takes a `$skill` mention.
func (codex) Name() string                     { return Codex }
func (codex) Prompt() string                   { return "$rota-orchestrate" }
func (codex) Command(Config) ([]string, error) { return []string{"codex"}, nil }
