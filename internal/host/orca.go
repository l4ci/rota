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
	"sync"
	"time"

	"github.com/l4ci/rota/internal/shlex"
)

// Orca's JSON contract is deliberately narrow pending live validation: create,
// current return {"terminal":{"id":"…","title":"…","cwd":"…","exited":false}};
// list returns {"terminals":[...]}; read returns {"screen":"…","exited":false}; status
// returns {"running":true}; send returns {"receipt":{"turn_started":true}}.
// Unsupported or incomplete replies fail closed, never imply a dead agent.
type orca struct {
	d           Deps
	currentOnce sync.Once
	current     orcaTerminal
}
type orcaHandle struct{ ID, Title, Cwd, PIDFile string }
type orcaTerminal struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Cwd    string `json:"cwd"`
	Exited bool   `json:"exited"`
}

func (o *orca) Kind() Kind                 { return Orca }
func (o *orca) Capabilities() Capabilities { return CapabilitiesOf(o) }
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
func (o *orca) probeCurrent() {
	o.currentOnce.Do(func() {
		raw, e := o.call(context.Background(), "terminal", "current", "--json")
		if e != nil {
			return
		}
		var r struct {
			Terminal orcaTerminal `json:"terminal"`
		}
		if json.Unmarshal([]byte(raw), &r) == nil && r.Terminal.ID != "" {
			o.current = r.Terminal
		}
	})
}
func (o *orca) InSession() bool { o.probeCurrent(); return o.current.ID != "" }
func (o *orca) Where() string {
	o.probeCurrent()
	if o.current.Title != "" {
		return o.current.Title
	}
	if o.current.ID != "" {
		return o.current.ID
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
	if !strings.HasPrefix(s, "orca:") || e != nil || json.Unmarshal(b, &h) != nil || h.Title == "" || h.Cwd == "" || h.PIDFile == "" {
		return h, fmt.Errorf("unverifiable Orca handle")
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
	pidfile, e := orcaPIDFile(cwd)
	if e != nil {
		return "", e
	}
	// Creation can succeed even if the response is lost or unsupported. Keep
	// enough identity to reacquire it without ever using an empty CLI target.
	h := orcaHandle{Title: title, Cwd: cwd, PIDFile: pidfile}
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
	var v struct {
		Terminal orcaTerminal `json:"terminal"`
	}
	if json.Unmarshal([]byte(raw), &v) != nil || v.Terminal.ID == "" {
		return pending, fmt.Errorf("orca create returned unsupported terminal JSON")
	}
	h.ID = v.Terminal.ID
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
		if e == nil && orcaReady(p) {
			meta, _ := decodeOrca(h)
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
func orcaReady(p string) bool {
	_, dialog := ReadDialog(p)
	return !dialog && looksBooted(p) && strings.ContainsAny(p, promptMarkers)
}
func (o *orca) ReadPane(ctx context.Context, slot, h string, lines int) (string, error) {
	v, e := decodeOrca(h)
	if e != nil {
		return "", e
	}
	return o.readPane(ctx, &v, lines)
}
func (o *orca) readPane(ctx context.Context, v *orcaHandle, lines int) (string, error) {
	if v.ID == "" {
		resolved, e := o.resolve(ctx, *v)
		if e != nil {
			return "", e
		}
		*v = resolved
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

func (o *orca) send(ctx context.Context, handle, text string) error {
	h, err := decodeOrca(handle)
	if err != nil {
		return err
	}
	before, err := o.readPane(ctx, &h, 0)
	handle = encodeOrca(h)
	if err != nil {
		return err
	}
	// Orca has no validated key-only transport. Refuse every dialog without
	// changing its selection or pasting text into it.
	if dialog, ok := ReadDialog(before); ok {
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
	if dialog, ok := ReadDialog(latest); ok {
		return &DialogError{First: dialog.First}
	}
	before = latest
	handle = encodeOrca(h)
	if !orcaReady(before) && !o.inputReady(h, before) {
		return fmt.Errorf("orca input is not ready")
	}
	raw, err := o.call(ctx, "terminal", "send", "--terminal", h.ID, "--text", text, "--enter", "--wait-submit", "20", "--json")
	if err != nil {
		return err
	}
	var r struct {
		Receipt *struct {
			TurnStarted bool `json:"turn_started"`
		} `json:"receipt"`
	}
	if json.Unmarshal([]byte(raw), &r) != nil {
		return fmt.Errorf("orca send returned malformed JSON; pickup unconfirmed")
	}
	if r.Receipt != nil && r.Receipt.TurnStarted {
		return nil
	}
	for i := 0; i < 4; i++ {
		o.d.settle(time.Second)
		after, e := o.ReadPane(ctx, "", handle, 0)
		if e != nil {
			return e
		}
		if after == before || humanDraft(stripDim(after), "") != "" {
			continue
		}
		above := after
		if n := strings.LastIndexAny(after, promptMarkers); n >= 0 {
			above = after[:n]
		}
		// Draft-only changes are not submission. Require new output above the
		// composer containing brief echo or explicit turn/busy evidence.
		oldAbove := before
		if n := strings.LastIndexAny(before, promptMarkers); n >= 0 {
			oldAbove = before[:n]
		}
		if above == oldAbove {
			continue
		}
		tail := briefTail(text)
		if (tail != "" && strings.Contains(squeeze(above), tail) && !strings.Contains(squeeze(oldAbove), tail)) || (orcaBusy.MatchString(above) && !orcaBusy.MatchString(oldAbove)) {
			return nil
		}
	}
	return ErrNotSubmitted
}

var orcaBusy = regexp.MustCompile(`(?i)esc to interrupt|working[.…]|thinking[.…]|turn started`)

func (o *orca) Kill(ctx context.Context, slot, handle string) error {
	if handle == "" {
		return nil
	}
	h, e := decodeOrca(handle)
	if e != nil {
		return e
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
		if t.Title == h.Title && filepath.Clean(t.Cwd) == filepath.Clean(h.Cwd) {
			matches = append(matches, t)
		}
	}
	if len(matches) > 1 {
		return fmt.Errorf("unverifiable Orca close: duplicate title and cwd")
	}
	if len(matches) == 1 {
		if _, e = o.call(ctx, "terminal", "close", "--terminal", matches[0].ID, "--json"); e != nil {
			return e
		}
	}
	gone := func() bool {
		ts, e := o.terminals(ctx)
		if e != nil {
			return false
		}
		for _, t := range ts {
			if t.Title == h.Title && filepath.Clean(t.Cwd) == filepath.Clean(h.Cwd) && !t.Exited {
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

// processes preserves the pre-close tree on disk: a retry after the root exits
// must still check children that were reparented when the terminal closed.
func (o *orca) processes(h orcaHandle, snapshot bool) ([]int, error) {
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
		return nil, fmt.Errorf("missing Orca pre-close process-tree proof")
	}
	if snapshot {
		pids = append(pids, o.d.Tree(pid)...)
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
		if e := os.WriteFile(treeFile, raw, 0600); e != nil {
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
	var r struct {
		Terminals *[]orcaTerminal `json:"terminals"`
	}
	if json.Unmarshal([]byte(raw), &r) != nil || r.Terminals == nil {
		return nil, fmt.Errorf("orca list returned unsupported terminal JSON")
	}
	for _, t := range *r.Terminals {
		if t.ID == "" || t.Cwd == "" {
			return nil, fmt.Errorf("orca list returned incomplete terminal identity")
		}
	}
	return *r.Terminals, nil
}
func (o *orca) resolve(ctx context.Context, h orcaHandle) (orcaHandle, error) {
	ts, e := o.terminals(ctx)
	if e != nil {
		return h, e
	}
	found := []orcaTerminal{}
	for _, t := range ts {
		if t.Title == h.Title && filepath.Clean(t.Cwd) == filepath.Clean(h.Cwd) {
			found = append(found, t)
		}
	}
	if len(found) != 1 {
		return h, fmt.Errorf("unverifiable Orca terminal: title and cwd matched %d terminals", len(found))
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
		if e == nil && h.Title == t.Title && filepath.Clean(h.Cwd) == filepath.Clean(t.Cwd) {
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
		handle, _, _ := o.recorded(t)
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
		agentless := false
		if t.Exited && owned {
			if pids, e := o.processes(h, false); e == nil {
				agentless = true
				for _, p := range pids {
					if o.d.Alive(p) {
						agentless = false
					}
				}
			}
		}
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
	if _, e := o.ReadPane(ctx, slot, handle, 0); e != nil {
		return ""
	}
	return encodeOrca(h)
}
func (o *orca) CapturePane(ctx context.Context, pane string, lines int) string {
	return o.Capture(ctx, "", pane, lines)
}
func (o *orca) SendPane(ctx context.Context, pane, text string) error { return o.send(ctx, pane, text) }

// Once the startup banner scrolls away, a persisted successful boot and the
// still-live exec PID identify this as the same agent rather than a bare shell.
func (o *orca) inputReady(h orcaHandle, p string) bool {
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
