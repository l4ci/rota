package host

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/l4ci/rota/internal/shlex"
	"path/filepath"
	"strings"
	"time"
)

// Orca uses native handles for identity; titles are mutable display text.
// Decode only direct records and explicit terminal/terminals/receipt wrappers.
// Read's direct screen and status's running fields await a raw live sample.
type orca struct{ d Deps }

type orcaHandle struct{ ID, Title, Cwd, PIDFile, Branch string }

type orcaTerminal struct {
	ID     string `json:"handle"`
	Title  string `json:"title"`
	Cwd    string `json:"worktreePath"`
	Exited bool   `json:"exited"`
	Branch string `json:"branch"`
}

func (o *orca) Kind() Kind { return Orca }

func (o *orca) Capabilities() Capabilities { return kindPolicy(Orca, CapabilitiesOf(o)) }

// RuntimeScopedHandles: an Orca runtime restart orphans handles whose
// processes survive, so list absence proves nothing.
func (o *orca) RuntimeScopedHandles() bool { return true }

// TreeKillClose: Orca owns terminals whose agent can outlive the handle.
func (o *orca) TreeKillClose() bool { return true }

func (o *orca) call(ctx context.Context, args ...string) (string, error) {
	r, e := o.d.Run(ctx, "orca", args)
	if e != nil {
		return "", e
	}
	if r.ExitCode != 0 {
		return "", fmt.Errorf("orca %s failed: %s", strings.Join(args[:2], " "), strings.TrimSpace(r.Stderr))
	}
	return r.Stdout, nil
}

func (o *orca) Require() error {
	if _, e := o.d.LookPath("orca"); e != nil {
		return fmt.Errorf("orca is not installed")
	}
	raw, e := o.call(context.Background(), "status", "--json")
	if e != nil {
		return e
	}
	var v struct {
		Running bool `json:"running"`
	}
	if json.Unmarshal([]byte(raw), &v) != nil || !v.Running {
		return fmt.Errorf("orca app is not running or returned unsupported status JSON")
	}
	return nil
}

func (o *orca) InSession() bool { return o.d.Getenv(envOrcaPane) != "" }

func (o *orca) Where() string {
	if h := o.d.Getenv(envOrcaPane); h != "" {
		return h
	}
	return "?"
}

func (o *orca) create(ctx context.Context, cwd, title, launch string, env []string) (string, error) {
	var pre []string
	for _, entry := range env {
		k, v, ok := strings.Cut(entry, "=")
		switch k {
		case "CLAUDE_CONFIG_DIR", "CODEX_HOME", "OPENCODE_CONFIG_DIR", "ROTA_SLOT", "ROTA_PORT_BASE", "ROTA_PORT_BLOCK", "ROTA_DB_SUFFIX", "TMPDIR":
		default:
			return "", fmt.Errorf("orca refuses environment key %q: shell history may expose secrets", k)
		}
		if !ok {
			return "", fmt.Errorf("invalid environment entry")
		}
		pre = append(pre, k+"="+shlex.QuoteAlways(v))
	}
	cwd, e := filepath.Abs(cwd)
	if e != nil {
		return "", e
	}
	branch, e := o.gitBranch(ctx, cwd)
	if e != nil {
		return "", e
	}
	pidfile, e := o.orcaPIDFile(cwd)
	if e != nil {
		return "", e
	}
	// Creation can succeed even if the response is lost or unsupported. Keep
	// the attempted creation proof, without adopting by title or using a blank target.
	h := orcaHandle{Title: title, Cwd: cwd, PIDFile: pidfile, Branch: branch}
	pending := encodeOrca(h)
	if e := o.d.FS.WriteFile(pidfile+".json", []byte(pending), 0600); e != nil {
		o.d.FS.Remove(pidfile)
		return "", fmt.Errorf("persist Orca creation identity: %w", e)
	}
	// WARNING: Orca types --command into the login shell and its history. Only
	// account paths and slot/port/database values are allowed here; never secrets.
	script := "printf '%s\\n' \"$$\" > " + shlex.QuoteAlways(pidfile) + "; exec " + launch
	if len(pre) > 0 {
		script = "printf '%s\\n' \"$$\" > " + shlex.QuoteAlways(pidfile) + "; " + strings.Join(pre, " ") + " exec " + launch
	}
	raw, e := o.call(ctx, "terminal", "create", "--worktree", "path:"+cwd, "--title", title, "--command", "sh -c "+shlex.QuoteAlways(script), "--json")
	if e != nil {
		return pending, e
	}
	v, e := decodeOrcaTerminal(raw)
	if e != nil {
		return pending, e
	}
	h.ID = v.ID
	encoded := encodeOrca(h)
	if e := o.d.FS.WriteFile(pidfile+".json", []byte(encoded), 0600); e != nil {
		return encoded, fmt.Errorf("persist Orca identity: %w", e)
	}
	// Capture immediately when the new shell has already written its PID.
	// Spawn retries at readiness if the shell is still starting.
	_, _ = o.processes(h, orcaBootEvidence)
	return encoded, nil
}

func (o *orca) Spawn(ctx context.Context, s SpawnOpts) (string, error) {
	h, e := o.create(ctx, s.Cwd, s.Session+":"+s.Slot, s.Launch, s.Env)
	if e != nil {
		return h, e
	}
	for waited := 0; waited < s.BootTimeout; waited += 2 {
		p, e := o.ReadPane(ctx, s.Slot, h, 0)
		if e == nil {
			if dialog, ok := orcaDialog(p); ok {
				return h, fmt.Errorf("%w; dismiss it in the Orca terminal, then re-run assign", &DialogError{First: dialog.First})
			}
		}
		if e == nil && orcaReady(p) {
			meta, _ := o.decodeOrca(h)
			if _, e := o.processes(meta, orcaBootEvidence); e != nil {
				return h, fmt.Errorf("record Orca boot process evidence: %w", e)
			}
			if e := o.d.FS.WriteFile(meta.PIDFile+".ready", []byte("ready"), 0600); e != nil {
				return h, e
			}
			return h, nil
		}
		if ctx.Err() != nil {
			return h, ctx.Err()
		}
		o.d.settle(2 * time.Second)
	}
	return h, fmt.Errorf("slot %q input did not come up within %ds", s.Slot, s.BootTimeout)
}

const orcaTrustQuestion = "Do you trust the contents of this directory?"

func orcaDialog(p string) (Dialog, bool) {
	if strings.Contains(p, orcaTrustQuestion) {
		return Dialog{First: orcaTrustQuestion}, true
	}
	return ReadDialog(p)
}

func orcaReady(p string) bool {
	if _, ok := orcaDialog(p); ok {
		return false
	}
	return bootReady(p, true)
}

func (o *orca) ReadPane(ctx context.Context, slot, h string, lines int) (string, error) {
	v, e := o.decodeOrca(h)
	if e != nil {
		return "", e
	}
	return o.readPane(ctx, &v, lines)
}

func (o *orca) readPane(ctx context.Context, v *orcaHandle, lines int) (string, error) {
	if v.PIDFile != "" {
		// Keep observed descendants while the agent is alive, before a crash
		// can reparent them. Missing proof during early boot does not hide a
		// dialog; Spawn requires a successful snapshot before returning ready.
		_, _ = o.processes(*v, orcaRefreshEvidence)
	}
	if v.ID == "" {
		return "", fmt.Errorf("unverifiable Orca terminal: native handle missing")
	}
	raw, e := o.call(ctx, "terminal", "read", "--terminal", v.ID, "--screen", "--json")
	if e != nil {
		*v, e = o.resolve(ctx, *v)
		if e != nil {
			return "", e
		}
		raw, e = o.call(ctx, "terminal", "read", "--terminal", v.ID, "--screen", "--json")
		if e != nil {
			return "", e
		}
	}
	var r struct {
		Screen *string `json:"screen"`
		Exited bool    `json:"exited"`
	}
	if json.Unmarshal([]byte(raw), &r) != nil {
		return "", fmt.Errorf("orca read returned malformed JSON")
	}
	if r.Exited {
		return "", ErrPaneGone
	}
	if r.Screen == nil {
		return "", fmt.Errorf("orca read returned unsupported screen JSON")
	}
	p := *r.Screen
	if lines > 0 {
		l := strings.Split(strings.TrimRight(p, "\n"), "\n")
		if len(l) > lines {
			p = strings.Join(l[len(l)-lines:], "\n")
		}
	}
	return p, nil
}

func (o *orca) Capture(ctx context.Context, s, h string, n int) string {
	p, _ := o.ReadPane(ctx, s, h, n)
	return p
}

func (o *orca) Status(context.Context, string, string) string { return "" }

func (o *orca) terminals(ctx context.Context) ([]orcaTerminal, error) {
	raw, e := o.call(ctx, "terminal", "list", "--json")
	if e != nil {
		return nil, e
	}
	var ts []orcaTerminal
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(trimmed, "[") {
		if e = json.Unmarshal([]byte(raw), &ts); e != nil {
			return nil, fmt.Errorf("orca list returned malformed terminal JSON: %w", e)
		}
	} else {
		var r struct {
			Terminals *[]orcaTerminal `json:"terminals"`
		}
		if json.Unmarshal([]byte(raw), &r) != nil || r.Terminals == nil {
			return nil, fmt.Errorf("orca list returned unsupported terminal JSON")
		}
		ts = *r.Terminals
	}
	seen := map[string]bool{}
	for _, t := range ts {
		if t.ID == "" || seen[t.ID] {
			return nil, fmt.Errorf("orca list returned missing or duplicate native handle")
		}
		seen[t.ID] = true
	}
	return ts, nil
}

func (o *orca) resolve(ctx context.Context, h orcaHandle) (orcaHandle, error) {
	ts, e := o.terminals(ctx)
	if e != nil {
		return h, e
	}
	found := []orcaTerminal{}
	for _, t := range ts {
		if t.ID == h.ID {
			found = append(found, t)
		}
	}
	if len(found) != 1 {
		return h, fmt.Errorf("unverifiable Orca terminal: native handle matched %d terminals", len(found))
	}
	if found[0].Exited {
		return h, ErrPaneGone
	}
	h.ID = found[0].ID
	return h, nil
}

// recorded reuses only unambiguous rota-owned identity; unknown terminals
// remain native ids and can never be closed via rota's PID-proof path.
func (o *orca) recorded(t orcaTerminal) (string, orcaHandle, bool) {
	dir, e := o.orcaStateDir(t.Cwd)
	if e != nil {
		return t.ID, orcaHandle{}, false
	}
	files, e := o.d.FS.Glob(filepath.Join(dir, "agent-*.pid.json"))
	if e != nil {
		return t.ID, orcaHandle{}, false
	}
	var candidates []string
	for _, f := range files {
		b, e := o.d.FS.ReadFile(f)
		if e != nil {
			continue
		}
		h, e := o.decodeOrca(string(b))
		if e == nil && h.ID == t.ID && filepath.Clean(h.Cwd) == filepath.Clean(t.Cwd) {
			candidates = append(candidates, string(b))
		}
	}
	if len(candidates) != 1 {
		return t.ID, orcaHandle{}, false
	}
	h, e := o.decodeOrca(candidates[0])
	return candidates[0], h, e == nil
}

func (o *orca) Snapshot(ctx context.Context) ([]Agent, error) {
	ts, e := o.terminals(ctx)
	if e != nil {
		return nil, e
	}
	out := []Agent{}
	for _, t := range ts {
		if t.Exited {
			continue
		}
		handle, h, owned := o.recorded(t)
		if owned && o.processesGone(h) {
			continue
		}
		out = append(out, Agent{Tab: handle, Name: t.Title, Cwd: t.Cwd})
	}
	return out, nil
}

func (o *orca) Tabs(ctx context.Context) ([]Tab, error) {
	ts, e := o.terminals(ctx)
	if e != nil {
		return nil, e
	}
	out := []Tab{}
	for _, t := range ts {
		handle, h, owned := o.recorded(t)
		agentless := owned && o.processesGone(h)
		out = append(out, Tab{ID: handle, Cwds: []string{t.Cwd}, Agentless: agentless})
	}
	return out, nil
}

func (o *orca) CloseTab(ctx context.Context, id string) error { return o.Kill(ctx, "", id) }

func (o *orca) OpenTab(ctx context.Context, t TabOpts) (string, error) {
	return o.create(ctx, t.Cwd, t.Label, t.Command, nil)
}

func (o *orca) PaneOf(ctx context.Context, slot, handle string) string {
	h, e := o.decodeOrca(handle)
	if e != nil {
		return ""
	}
	if _, e = o.readPane(ctx, &h, 0); e != nil {
		return ""
	}
	return h.ID
}

func (o *orca) CapturePane(ctx context.Context, pane string, lines int) string {
	if pane == "" {
		return ""
	}
	if strings.HasPrefix(pane, "orca:") {
		h, e := o.decodeOrca(pane)
		if e != nil {
			return ""
		}
		pane = h.ID
	}
	if pane == "" {
		return ""
	}
	p, _ := o.readPane(ctx, &orcaHandle{ID: pane}, lines)
	return p
}

func (o *orca) gitBranch(ctx context.Context, cwd string) (string, error) {
	r, e := o.d.Run(ctx, "git", []string{"-C", cwd, "symbolic-ref", "--quiet", "--short", "HEAD"})
	branch := strings.TrimSpace(r.Stdout)
	if e != nil || r.ExitCode != 0 || branch == "" || strings.ContainsAny(branch, "\r\n") {
		return "", fmt.Errorf("cannot verify Orca worktree symbolic branch in %s", cwd)
	}
	return branch, nil
}

func (o *orca) verifyBranch(ctx context.Context, h orcaHandle) error {
	ts, e := o.terminals(ctx)
	if e != nil {
		return fmt.Errorf("prompt already submitted; Orca branch verification failed: %w; disable autoRenameBranchFromWork in Orca settings and do not resend the prompt", e)
	}
	var terminal *orcaTerminal
	for i := range ts {
		if ts[i].ID == h.ID {
			terminal = &ts[i]
			break
		}
	}
	gitBranch, e := o.gitBranch(ctx, h.Cwd)
	if e != nil {
		return fmt.Errorf("prompt already submitted; Orca branch verification failed: %w; disable autoRenameBranchFromWork in Orca settings and do not resend the prompt", e)
	}
	nativeBranch := "<native handle missing>"
	if terminal != nil {
		nativeBranch = terminal.Branch
	}
	if h.Branch == "" || terminal == nil || nativeBranch != h.Branch || gitBranch != h.Branch {
		return fmt.Errorf("prompt already submitted; Orca branch changed or is unverifiable: expected %q, terminal %q, worktree %q; disable autoRenameBranchFromWork in Orca settings and do not resend the prompt", h.Branch, nativeBranch, gitBranch)
	}
	return nil
}
