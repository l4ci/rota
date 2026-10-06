package harness

import (
	"context"
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

// BlockCodexFlags is the blockedBy of a codex worker whose launch line uses a
// flag `codex --help` does not list.
const BlockCodexFlags = "codex flags"

const hookTrustFlag = "--dangerously-bypass-hook-trust"

// codex is the Codex CLI. Its account is a CODEX_HOME per slot, and every
// payload sent to it is signed: Codex follows unsigned text typed into its
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

// AccountEnv is the slot's CODEX_HOME; the claude config dir is not its
// business, and work.accounts is Anthropic's.
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
// session's signing key, which lives in the slot's home.
func (c codex) Prepare(launch string, exe func() (string, error), s Setup) (string, []byte, error) {
	bin, err := exe()
	if err != nil {
		return "", nil, refuse(Unavailable, "cannot find the rota binary for the prompt-check hook: %v", err)
	}
	keyPath, key, err := newPromptKey(s.Home)
	if err != nil {
		return "", nil, refuse(Unavailable, "cannot write the prompt key in %s: %v", s.Home, err)
	}
	out, err := withPromptHook(launch, CodexHookArgs(bin, keyPath))
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
	keyFile := filepath.Join(CodexHome(cd, slot), PromptKeyFile)
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

// CodexHomesDir is where the slot homes live, beside the round lease, under
// the git common dir.
func CodexHomesDir(commonDir string) string { return rotastate.CodexDir(commonDir) }

// CodexHome is the slot's CODEX_HOME: <git-common-dir>/rota/codex/<slot>. Never
// ~/.codex, and never inside the worktree, where it would dirty git status.
func CodexHome(commonDir, slot string) string { return filepath.Join(CodexHomesDir(commonDir), slot) }

// Preflight is everything a codex worker needs before anything is marked or
// killed, shared by `worker dispatch` and `round assign`: codex installed
// (Unavailable), every flag of the launch line listed by `codex --help`
// (Refused, blockedBy BlockCodexFlags), the host herdr, the slot home created and seeded, herdr's
// codex integration installed there, and the home logged in (Unavailable, hint
// `CODEX_HOME=<home> codex login`). It never writes auth.json.
func (c codex) Preflight(ctx context.Context, p Probe, o PreflightOpts) (Setup, error) {
	var set Setup
	bin, ok := p.Look("codex")
	if !ok {
		return set, refuse(Unavailable, "codex is not installed or not runnable")
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
	home := CodexHome(cd, o.Slot)
	set.Home = home

	// The home exists before the first codex call, so even --version runs
	// under it and never under ~/.codex.
	if err := os.MkdirAll(home, 0o700); err != nil {
		return set, refuse(Unavailable, "cannot create the codex home %s: %v", home, err)
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
		r := refuse(Refused, "codex --help does not list %s, which the launch line uses; update codex or change work.codexCommand", strings.Join(names, ", "))
		r.BlockedBy = BlockCodexFlags
		return set, r
	}

	if err := ensureCodexHome(o.Slot, home, o.Worktree); err != nil {
		return set, err
	}
	for _, f := range CheckCodexHome(ctx, p, bin, herdr, Home{Slot: o.Slot, Dir: home}) {
		switch f.Code {
		case IntegrationUnrunnable:
			return set, refuse(Unavailable, "herdr integration status could not run: %v", f.Err)
		case IntegrationStale:
			henv := []string{"CODEX_HOME=" + home}
			in, err := p.Run(ctx, herdr, []string{"integration", "install", "codex"}, henv)
			if err != nil || in.ExitCode != 0 {
				msg := strings.TrimSpace(in.Stderr)
				if err != nil {
					msg = err.Error()
				}
				x := refuse(Unavailable, "herdr integration install codex failed for slot '%s': %s", o.Slot, msg)
				x.Hint = "CODEX_HOME=" + home + " herdr integration install codex"
				return set, x
			}
		case NotLoggedIn:
			x := refuse(Unavailable, "slot '%s' is not logged in to Codex (CODEX_HOME=%s)", o.Slot, home)
			x.Hint = "CODEX_HOME=" + home + " codex login"
			return set, x
		}
	}
	return set, nil
}

// ensureCodexHome creates the slot home (0700) and, only when config.toml is
// absent, seeds it so no dialog opens in an unattended pane: no update check,
// and the slot's worktree trusted. An existing config.toml is never touched.
func ensureCodexHome(slot, home, worktree string) error {
	if err := os.MkdirAll(home, 0o700); err != nil {
		return refuse(Unavailable, "cannot create the codex home %s: %v", home, err)
	}
	cfgPath := filepath.Join(home, "config.toml")
	if _, err := os.Stat(cfgPath); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return refuse(Unavailable, "cannot read %s: %v", cfgPath, err)
	}
	if worktree == "" {
		return refuse(Resolution, "slot '%s' has no worktree to trust", slot)
	}
	if abs, err := filepath.Abs(worktree); err == nil {
		worktree = abs
	}
	body := "check_for_update_on_startup = false\n\n[projects." + tomlString(worktree) + "]\ntrust_level = \"trusted\"\n"
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		return refuse(Unavailable, "cannot write %s: %v", cfgPath, err)
	}
	return nil
}

// The orchestrator facet: codex takes a `$skill` mention.
func (codex) Name() string                  { return Codex }
func (codex) Prompt() string                { return "$rota-orchestrate" }
func (codex) Command(any) ([]string, error) { return []string{"codex"}, nil }
