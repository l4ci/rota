package host

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/shlex"
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

func (o *orca) Kind() Kind                 { return Orca }
func (o *orca) Capabilities() Capabilities { return CapabilitiesOf(o) }

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
func (o *orca) InSession() bool { return o.d.Getenv("ORCA_TERMINAL_HANDLE") != "" }
func (o *orca) Where() string {
	if h := o.d.Getenv("ORCA_TERMINAL_HANDLE"); h != "" {
		return h
	}
	return "?"
}
func encodeOrca(h orcaHandle) string {
	b, _ := json.Marshal(h)
	return "orca:" + base64.RawURLEncoding.EncodeToString(b)
}
func decodeOrca(s string) (orcaHandle, error) {
	var h orcaHandle
	b, e := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(s, "orca:"))
	if !strings.HasPrefix(s, "orca:") || e != nil || json.Unmarshal(b, &h) != nil || h.Cwd == "" || h.PIDFile == "" {
		return h, fmt.Errorf("unverifiable Orca handle")
	}
	dir, err := orcaStateDir(h.Cwd)
	match, _ := filepath.Match("agent-*.pid", filepath.Base(h.PIDFile))
	if err != nil || !filepath.IsAbs(h.PIDFile) || filepath.Clean(filepath.Dir(h.PIDFile)) != filepath.Clean(dir) || !match {
		return h, fmt.Errorf("unverifiable Orca handle: PID proof must be an agent-*.pid file in the worktree's rota-orca directory")
	}
	return h, nil
}
func orcaStateDir(cwd string) (string, error) {
	git := filepath.Join(cwd, ".git")
	info, e := os.Stat(git)
	if e != nil {
		return "", e
	}
	if !info.IsDir() {
		b, e := os.ReadFile(git)
		if e != nil {
			return "", e
		}
		s := strings.TrimSpace(strings.TrimPrefix(string(b), "gitdir: "))
		if !strings.HasPrefix(string(b), "gitdir: ") {
			return "", fmt.Errorf("invalid worktree git metadata")
		}
		if !filepath.IsAbs(s) {
			s = filepath.Join(cwd, s)
		}
		git = s
	}
	return filepath.Join(git, "rota-orca"), nil
}
func orcaPIDFile(cwd string) (string, error) {
	dir, e := orcaStateDir(cwd)
	if e != nil {
		return "", e
	}
	if e := os.MkdirAll(dir, 0700); e != nil {
		return "", e
	}
	f, e := os.CreateTemp(dir, "agent-*.pid")
	if e != nil {
		return "", e
	}
	if e = f.Close(); e != nil {
		return "", e
	}
	return f.Name(), nil
}
func (o *orca) create(ctx context.Context, cwd, title, launch string, env []string) (string, error) {
	var pre []string
	for _, entry := range env {
		k, v, ok := strings.Cut(entry, "=")
		switch k {
		case "CLAUDE_CONFIG_DIR", "CODEX_HOME", "OPENCODE_CONFIG_DIR", "ROTA_SLOT", "ROTA_PORT_BASE", "ROTA_PORT_BLOCK", "ROTA_DB_SUFFIX":
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
	pidfile, e := orcaPIDFile(cwd)
	if e != nil {
		return "", e
	}
	// Creation can succeed even if the response is lost or unsupported. Keep
	// the attempted creation proof, without adopting by title or using a blank target.
	h := orcaHandle{Title: title, Cwd: cwd, PIDFile: pidfile, Branch: branch}
	pending := encodeOrca(h)
	if e := os.WriteFile(pidfile+".json", []byte(pending), 0600); e != nil {
		os.Remove(pidfile)
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
	if e := os.WriteFile(pidfile+".json", []byte(encoded), 0600); e != nil {
		return encoded, fmt.Errorf("persist Orca identity: %w", e)
	}
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
			meta, _ := decodeOrca(h)
			if _, e := o.processes(meta, true); e != nil {
				return h, fmt.Errorf("record Orca boot process evidence: %w", e)
			}
			if e := os.WriteFile(meta.PIDFile+".ready", []byte("ready"), 0600); e != nil {
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

var orcaCodexPrompt = regexp.MustCompile(`(?m)^[ \t]*›(?:[ \t]|$)`)
var orcaCodexModel = regexp.MustCompile(`(?mi)^[ \t]*(?:[│|][ \t]*)?model:[ \t]*([^\r\n│]+)`)

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
	model := orcaCodexModel.FindStringSubmatch(p)
	if len(model) > 0 || orcaCodexPrompt.MatchString(p) {
		return len(model) > 1 && !strings.HasPrefix(strings.ToLower(strings.TrimSpace(model[1])), "loading") && orcaCodexPrompt.MatchString(p)
	}
	if !strings.Contains(p, "❯") {
		return false
	}
	for _, pattern := range bootPatterns {
		if strings.Contains(pattern.String(), "Ask Codex") {
			continue
		}
		if pattern.MatchString(p) {
			return true
		}
	}
	return false
}
func (o *orca) ReadPane(ctx context.Context, slot, h string, lines int) (string, error) {
	v, e := decodeOrca(h)
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
		_, _ = o.processes(*v, true)
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
func (o *orca) Notify(context.Context, string, string)        {}
func (o *orca) Send(ctx context.Context, slot, handle, file string) error {
	text, err := readFile(file)
	if err != nil {
		return err
	}
	return o.send(ctx, handle, text)
}

const orcaBriefLimit = 120 * 1024

func orcaCheckSize(text string) error {
	if len(text) > orcaBriefLimit {
		return fmt.Errorf("Orca brief is %d UTF-8 bytes; limit is %d bytes; shorten the brief before resending", len(text), orcaBriefLimit)
	}
	return nil
}
func (o *orca) send(ctx context.Context, handle, text string) error {
	if err := orcaCheckSize(text); err != nil {
		return err
	}
	h, err := decodeOrca(handle)
	if err != nil {
		return err
	}
	if h.Branch == "" {
		return fmt.Errorf("cannot submit Orca prompt: saved worktree branch is missing")
	}
	before, err := o.readPane(ctx, &h, 0)
	handle = encodeOrca(h)
	if err != nil {
		return err
	}
	// Orca has no validated key-only transport. Refuse every dialog without
	// changing its selection or pasting text into it.
	if dialog, ok := orcaDialog(before); ok {
		return &DialogError{First: dialog.First}
	}
	readErr := error(nil)
	latest := before
	read := func() string {
		p, e := o.readPane(ctx, &h, 0)
		if e != nil {
			readErr = e
		}
		latest = p
		return stripDim(p)
	}
	if waitNoDraft(&o.d, read, "") != "" {
		return ErrDraftOnPrompt
	}
	if readErr != nil {
		return readErr
	}
	if dialog, ok := orcaDialog(latest); ok {
		return &DialogError{First: dialog.First}
	}
	before = latest
	handle = encodeOrca(h)
	if !orcaReady(before) && !o.inputReady(h, before) {
		return fmt.Errorf("orca input is not ready; wait for the agent prompt in the Orca terminal, then retry")
	}
	raw, err := o.call(ctx, "terminal", "send", "--terminal", h.ID, "--text", text, "--enter", "--wait-submit", "20", "--json")
	if err != nil {
		return err
	}
	started, err := orcaTurnStarted(raw)
	if err != nil {
		return err
	}
	if started {
		return o.verifyBranch(ctx, h)
	}
	return confirmPickup(&o.d, Orca, func(time.Duration) (bool, error) {
		after, e := o.ReadPane(ctx, "", handle, 0)
		if e != nil {
			return false, e
		}
		if after == before || humanDraft(stripDim(after), "") != "" {
			return false, nil
		}
		above := after
		if n := strings.LastIndexAny(after, promptMarkers); n >= 0 {
			above = after[:n]
		}
		// Draft-only changes are not submission. Require new output above the
		// composer containing explicit turn/busy evidence. An echo alone
		// proves input acceptance, not the turn-start required by dispatch.
		oldAbove := before
		if n := strings.LastIndexAny(before, promptMarkers); n >= 0 {
			oldAbove = before[:n]
		}
		if above == oldAbove {
			return false, nil
		}
		if orcaBusy.MatchString(above) && !orcaBusy.MatchString(oldAbove) && !orcaBusy.MatchString(text) {
			return true, o.verifyBranch(ctx, h)
		}
		return false, nil
	}, nil)
}

var orcaBusy = regexp.MustCompile(`(?im)^[ \t]*(?:[•●✶✻✽✢*][ \t]*)?[^\r\n]*\besc to interrupt\b[^\r\n]*$`)

func (o *orca) Kill(ctx context.Context, slot, handle string) error {
	if handle == "" {
		return nil
	}
	h, e := decodeOrca(handle)
	if e != nil {
		return e
	}
	if h.ID == "" {
		return fmt.Errorf("unverifiable Orca close: native handle missing")
	}
	pids, e := o.processes(h, true)
	if e != nil {
		return e
	}
	ts, e := o.terminals(ctx)
	if e != nil {
		return e
	}
	matches := []orcaTerminal{}
	for _, t := range ts {
		if t.ID == h.ID {
			matches = append(matches, t)
		}
	}
	if len(matches) > 1 {
		return fmt.Errorf("unverifiable Orca close: duplicate native handle")
	}
	if len(matches) == 1 {
		if _, e = o.call(ctx, "terminal", "close", "--terminal", h.ID, "--json"); e != nil {
			return e
		}
	}
	// A runtime restart can remove the native handle without a prior close.
	// Absence is not process proof: the retained tree must still be all gone.
	// Round reclaim owns the --force guard for an unverifiable stale handle.
	gone := func() bool {
		ts, e := o.terminals(ctx)
		if e != nil {
			return false
		}
		for _, t := range ts {
			if t.ID == h.ID && !t.Exited {
				return false
			}
		}
		return true
	}
	if alive, ok := killLoop(&o.d, pids, gone); !ok {
		return fmt.Errorf("orca close unproven; surviving processes:%s", pidList(alive))
	}
	os.Remove(h.PIDFile + ".json")
	return nil
}

// processes retains observations from boot, polling and cleanup. Serialize
// updates so a polling process cannot discard descendants saved by cleanup.
func (o *orca) processes(h orcaHandle, snapshot bool) ([]int, error) {
	var pids []int
	err := fsio.Locked(h.PIDFile+".tree", 10*time.Second, func() error {
		var err error
		pids, err = o.processesLocked(h, snapshot)
		return err
	})
	return pids, err
}
func (o *orca) processesLocked(h orcaHandle, snapshot bool) ([]int, error) {
	raw, e := os.ReadFile(h.PIDFile)
	if e != nil {
		return nil, fmt.Errorf("missing Orca PID proof: %w", e)
	}
	pid, e := strconv.Atoi(strings.TrimSpace(string(raw)))
	if e != nil || pid <= 0 {
		return nil, fmt.Errorf("missing Orca PID proof")
	}
	pids := []int{pid}
	treeFile := h.PIDFile + ".tree"
	if raw, e := os.ReadFile(treeFile); e == nil {
		var stored []int
		if json.Unmarshal(raw, &stored) != nil || len(stored) == 0 {
			return nil, fmt.Errorf("unreadable Orca tree proof")
		}
		for _, p := range stored {
			if p <= 0 {
				return nil, fmt.Errorf("invalid Orca tree proof")
			}
		}
		pids = append(pids, stored...)
	} else if !os.IsNotExist(e) {
		return nil, e
	} else if !snapshot || !o.d.Alive(pid) {
		return nil, fmt.Errorf("missing Orca retained process-tree proof; inspect the slot processes before retrying cleanup")
	}
	if snapshot {
		// An observed child may have outlived the original agent and spawned
		// its own descendants. Refresh every retained live root, not just PID.
		for _, root := range append([]int(nil), pids...) {
			if o.d.Alive(root) {
				pids = append(pids, o.d.Tree(root)...)
			}
		}
	}
	unique := []int{}
	seen := map[int]bool{}
	for _, p := range pids {
		if p > 0 && !seen[p] {
			unique = append(unique, p)
			seen[p] = true
		}
	}
	if snapshot {
		raw, _ := json.Marshal(unique)
		if e := fsio.WriteFileAtomic(treeFile, raw); e != nil {
			return nil, e
		}
	}
	return unique, nil
}

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
	dir, e := orcaStateDir(t.Cwd)
	if e != nil {
		return t.ID, orcaHandle{}, false
	}
	files, e := filepath.Glob(filepath.Join(dir, "agent-*.pid.json"))
	if e != nil {
		return t.ID, orcaHandle{}, false
	}
	var candidates []string
	for _, f := range files {
		b, e := os.ReadFile(f)
		if e != nil {
			continue
		}
		h, e := decodeOrca(string(b))
		if e == nil && h.ID == t.ID && filepath.Clean(h.Cwd) == filepath.Clean(t.Cwd) {
			candidates = append(candidates, string(b))
		}
	}
	if len(candidates) != 1 {
		return t.ID, orcaHandle{}, false
	}
	h, e := decodeOrca(candidates[0])
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

// Only rota-owned, retained process evidence can prove a terminal agentless;
// connected/writable/orphaned and absent runtime handles cannot.
func (o *orca) processesGone(h orcaHandle) bool {
	pids, err := o.processes(h, false)
	if err != nil {
		return false
	}
	for _, pid := range pids {
		if o.d.Alive(pid) {
			return false
		}
	}
	return true
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
	h, e := decodeOrca(handle)
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
		h, e := decodeOrca(pane)
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
func (o *orca) SendPane(ctx context.Context, pane, text string) error {
	if pane == "" {
		return fmt.Errorf("no Orca pane to send to")
	}
	if e := orcaCheckSize(text); e != nil {
		return e
	}
	if strings.HasPrefix(pane, "orca:") {
		h, e := decodeOrca(pane)
		if e != nil {
			return e
		}
		pane = h.ID
	}
	if pane == "" {
		return fmt.Errorf("no native Orca pane handle")
	}
	_, e := o.call(ctx, "terminal", "send", "--terminal", pane, "--text", text, "--enter", "--json")
	return e
}

// Once the startup banner scrolls away, a persisted successful boot and the
// still-live exec PID identify this as the same agent rather than a bare shell.
func (o *orca) inputReady(h orcaHandle, p string) bool {
	if _, ok := orcaDialog(p); ok {
		return false
	}
	if m := orcaCodexModel.FindStringSubmatch(p); len(m) > 1 && strings.HasPrefix(strings.ToLower(strings.TrimSpace(m[1])), "loading") {
		return false
	}
	if _, e := os.Stat(h.PIDFile + ".ready"); e != nil {
		return false
	}
	b, e := os.ReadFile(h.PIDFile)
	if e != nil {
		return false
	}
	pid, e := strconv.Atoi(strings.TrimSpace(string(b)))
	if e != nil || pid <= 0 || !o.d.Alive(pid) {
		return false
	}
	return strings.ContainsAny(p, promptMarkers) || orcaBusy.MatchString(p)
}

func decodeOrcaTerminal(raw string) (orcaTerminal, error) {
	var t orcaTerminal
	if e := json.Unmarshal([]byte(raw), &t); e != nil {
		return t, fmt.Errorf("orca create returned malformed JSON: %w", e)
	}
	if t.ID == "" {
		var r struct {
			Terminal orcaTerminal `json:"terminal"`
		}
		if json.Unmarshal([]byte(raw), &r) == nil {
			t = r.Terminal
		}
	}
	if t.ID == "" {
		return t, fmt.Errorf("orca create returned unsupported terminal JSON")
	}
	return t, nil
}
func orcaTurnStarted(raw string) (bool, error) {
	type receipt struct {
		Stages      []string `json:"stages"`
		Provider    string   `json:"provider"`
		Observation string   `json:"observation"`
	}
	var r receipt
	if e := json.Unmarshal([]byte(raw), &r); e != nil {
		return false, fmt.Errorf("orca send returned malformed JSON; pickup unconfirmed: %w", e)
	}
	if r.Stages == nil {
		var w struct {
			Receipt *receipt `json:"receipt"`
		}
		if json.Unmarshal([]byte(raw), &w) == nil && w.Receipt != nil {
			r = *w.Receipt
		}
	}
	if r.Observation != "" && r.Observation != "supported" {
		return false, nil
	}
	for _, stage := range r.Stages {
		if stage == "turn_started" {
			return true, nil
		}
	}
	return false, nil
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
