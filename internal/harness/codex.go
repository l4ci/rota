package harness

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/rotastate"
	"github.com/l4ci/rota/internal/shlex"
)

// Codex workers (E1, #68). The contract is "E: Codex workers" in
// docs/design/contract/.

// DefaultCodexCommand is the launch line of a codex worker. The flag list is
// the one thing here that may change with the Codex CLI, so it lives only in
// this constant. {model} takes the tier's model; with no model the pair is
// dropped (Launch). --no-daemon keeps the session in the pane's own process
// tree, so dispatch's kill is provable.
const DefaultCodexCommand = "codex --model {model} --dangerously-bypass-approvals-and-sandbox --dangerously-bypass-hook-trust --no-daemon --no-alt-screen"

// BlockCodexVersion is the blockedBy of a codex worker outside the supported
// version range; CodexVersionFlag is the flag that lets one call through.
const (
	BlockCodexVersion = "codex version"
	CodexVersionFlag  = "--accept-codex-version"
)

const hookTrustFlag = "--dangerously-bypass-hook-trust"

// codex is the Codex CLI. Its account is the default Codex home, or a named
// home of work.codexAccounts, and every payload sent to it is signed: Codex follows unsigned text typed into its
// pane, so a UserPromptSubmit hook blocks anything without the signature.
type codex struct{}

func (codex) Kind() string       { return Codex }
func (codex) CommandKey() string { return "work.codexCommand" }

// Launch is work.codexCommand, else the default launch line. A model fills
// {model}. The default without a model drops its `--model {model}` pair and
// Codex picks its own; a custom command that still holds {model} then has
// nothing to put there, which is a usage error. A custom command without the
// placeholder runs as written.
func (c codex) Launch(cfg any, model string) (string, error) {
	cmd, custom := DefaultCodexCommand, false
	if s := config.StringIfSet(cfg, c.CommandKey()); s != "" {
		cmd, custom = s, true
	}
	if model == "" {
		if custom {
			if strings.Contains(cmd, ModelPlaceholder) {
				return "", refuse(Usage, "work.codexCommand holds {model} but no model was chosen: pass a tier model (round assign) or drop the placeholder")
			}
			return cmd, nil
		}
		cmd = strings.Replace(cmd, "--model "+ModelPlaceholder+" ", "", 1)
	}
	return strings.ReplaceAll(cmd, ModelPlaceholder, model), nil
}

// NeedsModel: only a custom work.codexCommand that holds {model} does (Launch
// refuses it with none).
func (c codex) NeedsModel(cfg any) bool {
	_, err := c.Launch(cfg, "")
	return err != nil
}

// ModelApplies: the default command always takes the model.
func (c codex) ModelApplies(cfg any) bool { return placeholderApplies(cfg, c.CommandKey()) }

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

// AccountEnv is the slot's CODEX_HOME, set only for a configured account: the
// default home is Codex's own. The claude config dir is not its business, and
// work.accounts is Anthropic's.
func (codex) AccountEnv(a Account) []string {
	if a.CodexHome == "" {
		return nil
	}
	return []string{"CODEX_HOME=" + a.CodexHome}
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
	keyPath, key, err := newPromptKey(s.StateDir)
	if err != nil {
		return "", nil, refuse(Unavailable, "cannot write the prompt key in %s: %v", s.StateDir, err)
	}
	out, err := withPromptHook(launch, append(codexLaunchArgs(s.Worktree), CodexHookArgs(bin, keyPath)...))
	if err != nil {
		return "", nil, refuse(Unavailable, "cannot add the prompt-check hook to %s: %v", c.CommandKey(), err)
	}
	return out, key, nil
}

// RelayKey loads the key the session's task dispatch wrote.
func (codex) RelayKey(commonDir func() (string, error), slot string) ([]byte, error) {
	cd, err := commonDir()
	if err != nil {
		return nil, err
	}
	keyFile := filepath.Join(CodexSlotDir(cd, slot), PromptKeyFile)
	key, err := LoadPromptKey(keyFile)
	if err != nil {
		r := refuse(Unavailable, "slot '%s' is a codex worker but its prompt key is unreadable (%s): %v", slot, keyFile, err)
		r.Hint = "re-dispatch the task: a fresh session gets a fresh key and hook"
		return nil, r
	}
	return key, nil
}

// Sign ends payload with its ROTA-SIG trailer.
func (codex) Sign(key []byte, payload string) string { return signPrompt(key, payload) }

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
// (Unavailable), its version in range (Refused, blockedBy BlockCodexVersion,
// unless Accept), the host herdr, the account's home (the default Codex home,
// or a configured account's) with herdr's codex integration installed and
// logged in (Unavailable, hint `[CODEX_HOME=<home> ]codex login`), and the
// slot's state directory. It never writes into a Codex home beyond herdr's own
// integration install, and never touches auth.json.
func (c codex) Preflight(ctx context.Context, p Probe, o PreflightOpts) (Setup, error) {
	var set Setup
	bin, ok := p.Look("codex")
	if !ok {
		return set, refuse(Unavailable, "codex is not installed (codex workers need codex-cli %s)", CodexRange)
	}
	if !o.Herdr {
		return set, refuse(Unavailable, "codex workers need work.dispatch=herdr")
	}
	herdr, ok := p.Look("herdr")
	if !ok {
		return set, refuse(Unavailable, "herdr is not installed")
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
		return set, refuse(Usage, "work.codexAccounts account '%s' has no codexHome", acct.Name)
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
	if f == nil {
		set.Version = v.String()
	} else {
		shown := "(version unreadable)"
		if f.Code == VersionOutOfRange {
			shown = f.Version
		}
		if !o.Accept {
			r := refuse(Refused, "codex %s is outside the supported range %s; install codex-cli 0.159.x or pass %s", shown, CodexRange, CodexVersionFlag)
			r.BlockedBy = BlockCodexVersion
			return set, r
		}
		set.Version = shown
		set.Warnings = append(set.Warnings, fmt.Sprintf("codex %s is outside the supported range %s", shown, CodexRange))
		// An older Codex may ignore -c features.hooks=true, which would leave
		// unsigned pane text unchecked (#3).
		set.Warnings = append(set.Warnings, "prompt check unverified on this Codex: an older version may ignore -c features.hooks=true, so unsigned pane text could reach the worker")
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
func (codex) Name() string                  { return Codex }
func (codex) Prompt() string                { return "$rota-orchestrate" }
func (codex) Command(any) ([]string, error) { return []string{"codex"}, nil }
