package worker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/doctor"
	"github.com/l4ci/rota/internal/shlex"
)

// Codex workers (E1, #68). The contract is "E: Codex workers" in
// docs/design/contract/.

// Harness kinds of a worker.
const (
	KindClaude = "claude"
	KindCodex  = "codex"
)

// DefaultCodexCommand is the launch line of a codex worker. The flag list is
// the one thing here that may change with the Codex CLI, so it lives only in
// this constant. {model} takes the tier's model; with no model the pair is
// dropped (codexCommand). --no-daemon keeps the session in the pane's own
// process tree, so dispatch's kill is provable.
const DefaultCodexCommand = "codex --model {model} --dangerously-bypass-approvals-and-sandbox --dangerously-bypass-hook-trust --no-daemon --no-alt-screen"

// Blocked reasons and hints of a codex worker that cannot start.
const (
	BlockCodexVersion = "codex version"
	// CodexVersionFlag is the flag that lets one call through an unsupported
	// Codex CLI version.
	CodexVersionFlag = "--accept-codex-version"
)

// codexCommand is work.codexCommand, else the default launch line. A model
// fills {model}. The default without a model drops its `--model {model}` pair
// and Codex picks its own; a custom command that still holds {model} then has
// nothing to put there, which is a usage error. A custom command without the
// placeholder runs as written.
func codexCommand(root, model string) (string, error) {
	cfg := config.Load(filepath.Join(root, ".rota", "config.json"))
	cmd, custom := DefaultCodexCommand, false
	if v, ok := config.Lookup(cfg, "work.codexCommand"); ok {
		if s, _ := v.(string); s != "" {
			cmd, custom = s, true
		}
	}
	if model == "" {
		if custom {
			if strings.Contains(cmd, ModelPlaceholder) {
				return "", fail(ExitUsage, "work.codexCommand holds {model} but no model was chosen: pass a tier model (round assign) or drop the placeholder")
			}
			return cmd, nil
		}
		cmd = strings.Replace(cmd, "--model "+ModelPlaceholder+" ", "", 1)
	}
	return strings.ReplaceAll(cmd, ModelPlaceholder, model), nil
}

// CodexNeedsModel reports whether a codex launch needs a tier model: only a
// custom work.codexCommand that holds {model} does (codexCommand refuses it
// with none).
func CodexNeedsModel(root string) bool {
	_, err := codexCommand(root, "")
	return err != nil
}

// CodexResume returns the first token after the codex binary that is one of
// its `resume` or `fork` subcommands, which reopen a previous session in the
// "fresh" one and would undo the reset. Codex takes options with values
// (`--model x resume`), so the first non-option token is not necessarily the
// subcommand: any token that is exactly one counts. claude's -c and -r mean
// something else to codex (-c is a config override) and are not looked at.
// Quoted arguments are scanned too (`sh -c "codex resume"`). An error means
// the command cannot be parsed.
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

// CommonDir is the git common dir of root, absolute and symlink-resolved: the
// directory beside which the round lease and the codex homes live.
func CommonDir(ctx context.Context, git GitFunc, root string) (string, error) {
	out, errOut, code, err := git(ctx, root, "rev-parse", "--git-common-dir")
	if err != nil || code != 0 {
		return "", fail(ExitUnavailable, "git rev-parse --git-common-dir failed: "+strings.TrimSpace(errOut))
	}
	p := strings.TrimSpace(out)
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return filepath.Clean(p), nil
}

// CodexHomesDir is where the slot homes live, beside the round lease.
func CodexHomesDir(commonDir string) string { return filepath.Join(commonDir, "rota", "codex") }

// CodexHome is the slot's CODEX_HOME: <git-common-dir>/rota/codex/<slot>. Never
// ~/.codex, and never inside the worktree, where it would dirty git status.
func CodexHome(commonDir, slot string) string { return filepath.Join(CodexHomesDir(commonDir), slot) }

// CodexSetup is what a passed codex preflight leaves for the dispatch.
type CodexSetup struct {
	Home     string
	Version  string
	Warnings []string
}

func codexRefusal(by, msg string) *Error {
	e := fail(ExitRefused, msg)
	e.Data = BlockData{BlockedBy: by}
	return e
}

// CodexPreflight is everything a codex worker needs before anything is marked
// or killed, shared by `worker dispatch` and `round assign`: codex installed
// (exit 5), its version in range (exit 4, blockedBy "codex version", unless
// accept), the host herdr (5), the slot home created and seeded, herdr's codex
// integration installed there, and the home logged in (5, hint
// `CODEX_HOME=<home> codex login`). It never writes auth.json.
func (e Env) CodexPreflight(ctx context.Context, root, slot string, accept bool) (CodexSetup, error) {
	e = e.withDefaults()
	var set CodexSetup
	bin, err := e.LookPath("codex")
	if err != nil {
		return set, fail(ExitUnavailable, "codex is not installed (codex workers need codex-cli "+doctor.CodexRange+")")
	}
	if dispatchKind(root) != "herdr" {
		return set, fail(ExitUnavailable, "codex workers need work.dispatch=herdr")
	}
	herdr, err := e.LookPath("herdr")
	if err != nil {
		return set, fail(ExitUnavailable, "herdr is not installed")
	}

	cd, err := CommonDir(ctx, e.Git, root)
	if err != nil {
		return set, err
	}
	home := CodexHome(cd, slot)
	set.Home = home

	// The home exists before the first codex call, so even --version runs
	// under it and never under ~/.codex.
	if err := os.MkdirAll(home, 0o700); err != nil {
		return set, fail(ExitUnavailable, "cannot create the codex home "+home+": "+err.Error())
	}
	r, err := e.Run(ctx, bin, []string{"--version"}, []string{"CODEX_HOME=" + home})
	v, ok := doctor.ParseCodexVersion(r.Stdout + "\n" + r.Stderr)
	switch {
	case err == nil && r.ExitCode == 0 && ok && v.InRange():
		set.Version = v.String()
	default:
		shown := "(version unreadable)"
		if err == nil && r.ExitCode == 0 && ok {
			shown = v.String()
		}
		if !accept {
			return set, codexRefusal(BlockCodexVersion, fmt.Sprintf("codex %s is outside the supported range %s; install codex-cli 0.159.x or pass %s", shown, doctor.CodexRange, CodexVersionFlag))
		}
		set.Version = shown
		set.Warnings = append(set.Warnings, fmt.Sprintf("codex %s is outside the supported range %s", shown, doctor.CodexRange))
	}

	if err := e.ensureCodexHome(root, slot, home); err != nil {
		return set, err
	}
	henv := []string{"CODEX_HOME=" + home}
	st, err := e.Run(ctx, herdr, []string{"integration", "status"}, henv)
	if err != nil {
		return set, fail(ExitUnavailable, "herdr integration status could not run: "+err.Error())
	}
	if st.ExitCode != 0 || doctor.ParseIntegration(st.Stdout, "codex") != "current" {
		in, err := e.Run(ctx, herdr, []string{"integration", "install", "codex"}, henv)
		if err != nil || in.ExitCode != 0 {
			msg := strings.TrimSpace(in.Stderr)
			if err != nil {
				msg = err.Error()
			}
			x := fail(ExitUnavailable, "herdr integration install codex failed for slot '"+slot+"': "+msg)
			x.Hint = "CODEX_HOME=" + home + " herdr integration install codex"
			return set, x
		}
	}

	lg, err := e.Run(ctx, bin, []string{"login", "status"}, henv)
	if err != nil || lg.ExitCode != 0 {
		x := fail(ExitUnavailable, fmt.Sprintf("slot '%s' is not logged in to Codex (CODEX_HOME=%s)", slot, home))
		x.Hint = "CODEX_HOME=" + home + " codex login"
		return set, x
	}
	return set, nil
}

// ensureCodexHome creates the slot home (0700) and, only when config.toml is
// absent, seeds it so no dialog opens in an unattended pane: no update check,
// and the slot's worktree trusted. An existing config.toml is never touched.
func (e Env) ensureCodexHome(root, slot, home string) error {
	if err := os.MkdirAll(home, 0o700); err != nil {
		return fail(ExitUnavailable, "cannot create the codex home "+home+": "+err.Error())
	}
	cfgPath := filepath.Join(home, "config.toml")
	if _, err := os.Stat(cfgPath); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fail(ExitUnavailable, "cannot read "+cfgPath+": "+err.Error())
	}
	wt := ""
	if s := LoadRegistry(root).Slot(slot); s != nil {
		wt = Str(s, "worktree")
	}
	if wt == "" {
		return fail(ExitResolution, fmt.Sprintf("slot '%s' has no worktree to trust", slot))
	}
	if abs, err := filepath.Abs(wt); err == nil {
		wt = abs
	}
	body := "check_for_update_on_startup = false\n\n[projects." + tomlString(wt) + "]\ntrust_level = \"trusted\"\n"
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		return fail(ExitUnavailable, "cannot write "+cfgPath+": "+err.Error())
	}
	return nil
}

// tomlString is s as a TOML basic string: quoted, with backslash, quote and
// control characters escaped.
func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\b':
			b.WriteString(`\b`)
		case r == '\f':
			b.WriteString(`\f`)
		case r < 0x20 || r == 0x7f:
			b.WriteString(`\u` + fmt.Sprintf("%04X", r))
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
