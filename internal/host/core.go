package host

// Core is deliberately a separate transport from legacy Herdr. It dials only
// the inherited endpoint and never starts a server or executes hc/herdr.
import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/l4ci/rota/internal/harness"
)

type coreHost struct {
	d               Deps
	mu              sync.Mutex
	coreID, version string
}
type coreStatus struct{ App, State string }
type coreTerminal struct {
	Address                    string `json:"address"`
	Created                    uint64 `json:"created_ms"`
	PID                        int    `json:"pid"`
	ExitCode                   *int   `json:"exit_code"`
	Signal                     *int   `json:"signal"`
	Cwd, State, Screen, Reason string
	Labels                     map[string]string
	Cols, Rows                 int
	Parent                     *string
	DieWithParent              bool `json:"die_with_parent"`
	Reported                   bool
	Status                     *coreStatus
	Turn                       uint64
}
type coreHandle struct {
	Endpoint, Core, Address, Token, Slot, Cwd string
	Created                                   uint64
	PID                                       int
	PIDs                                      []int
}
type coreRPCError struct {
	Code    int
	Message string
	Data    struct{ Kind string }
}

func (e *coreRPCError) Error() string { return "Core RPC: " + e.Message + " (" + e.Data.Kind + ")" }

type coreConn struct {
	net.Conn
	enc           *json.Encoder
	dec           *json.Decoder
	id            int
	core, version string
	methods       map[string]bool
}

func (c *coreConn) call(method string, p any, v any) error {
	c.id++
	if e := c.enc.Encode(map[string]any{"jsonrpc": "2.0", "id": c.id, "method": method, "params": p}); e != nil {
		return e
	}
	var r struct {
		ID     int
		Result json.RawMessage
		Error  *coreRPCError
	}
	if e := c.dec.Decode(&r); e != nil {
		return e
	}
	if r.ID != c.id {
		return fmt.Errorf("Core RPC mismatched response")
	}
	if r.Error != nil {
		return r.Error
	}
	if len(r.Result) == 0 {
		return fmt.Errorf("Core RPC missing result")
	}
	return json.Unmarshal(r.Result, v)
}
func (h *coreHost) connect(ctx context.Context, endpoint, core string) (*coreConn, error) {
	return h.connectMode(ctx, endpoint, core, false)
}
func (h *coreHost) connectMode(ctx context.Context, endpoint, core string, framed bool) (*coreConn, error) {
	if endpoint == "" || !filepath.IsAbs(endpoint) {
		return nil, fmt.Errorf("herdr-core requires inherited HC_TERMINAL_SOCKET absolute endpoint")
	}
	n, e := h.d.Dial(ctx, endpoint)
	if e != nil {
		return nil, e
	}
	deadline := time.Now().Add(30 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	n.SetDeadline(deadline)
	c := &coreConn{Conn: n, enc: json.NewEncoder(n), dec: json.NewDecoder(n), methods: map[string]bool{}}
	var hello struct {
		Generation int
		Version    string
		Core       struct{ ID string }
		Methods    []string
		Framed     bool
	}
	if e = c.call("hello", map[string]any{"generation": 1, "framed": framed}, &hello); e != nil {
		n.Close()
		return nil, e
	}
	if hello.Generation != 1 || hello.Core.ID == "" || hello.Framed != framed || (core != "" && core != hello.Core.ID) {
		n.Close()
		return nil, fmt.Errorf("Core endpoint identity/protocol changed; inspect and recover workers")
	}
	c.core, c.version = hello.Core.ID, hello.Version
	for _, m := range hello.Methods {
		c.methods[m] = true
	}
	h.mu.Lock()
	h.coreID, h.version = c.core, c.version
	h.mu.Unlock()
	return c, nil
}
func (h *coreHost) caller(ctx context.Context) (*coreConn, coreTerminal, error) {
	c, e := h.connect(ctx, h.d.Getenv("HC_TERMINAL_SOCKET"), "")
	if e != nil {
		return nil, coreTerminal{}, e
	}
	id := h.d.Getenv("HC_TERMINAL_ID")
	var t coreTerminal
	if id == "" || strings.Contains(id, "/") {
		c.Close()
		return nil, t, fmt.Errorf("herdr-core requires a local HC_TERMINAL_ID")
	}
	if e = c.call("terminal.get", map[string]any{"terminal": id}, &t); e != nil {
		c.Close()
		return nil, t, e
	}
	if t.Address != id || t.Created == 0 || t.State != "live" {
		c.Close()
		return nil, t, fmt.Errorf("Core caller terminal identity is not live")
	}
	return c, t, nil
}
func (h *coreHost) Kind() Kind                 { return HerdrCore }
func (h *coreHost) Capabilities() Capabilities { return CapabilitiesOf(h) }
func (h *coreHost) RuntimeScopedHandles() bool { return true }
func (h *coreHost) TreeKillClose() bool        { return true }
func (h *coreHost) InSession() bool {
	return h.d.Getenv("HC_TERMINAL_ID") != "" && h.d.Getenv("HC_TERMINAL_SOCKET") != ""
}
func (h *coreHost) Where() string {
	return h.d.Getenv("HC_TERMINAL_SOCKET") + " / " + h.d.Getenv("HC_TERMINAL_ID")
}
func (h *coreHost) CoreIdentity() (string, string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.coreID, h.version
}
func (h *coreHost) Require() error {
	c, _, e := h.caller(context.Background())
	if e != nil {
		return e
	}
	defer c.Close()
	for _, m := range []string{"terminal.run", "terminal.get", "terminal.read", "terminal.send", "terminal.kill", "terminal.list", "events.subscribe"} {
		if !c.methods[m] {
			return fmt.Errorf("%w: Core missing %s", ErrUnsupported, m)
		}
	}
	return nil
}
func (h *coreHost) stateDir() (string, error) {
	if p := h.d.Getenv("ROTA_CORE_STATE_DIR"); p != "" {
		return p, nil
	}
	p := h.d.Getenv("XDG_STATE_HOME")
	if p == "" {
		home := h.d.Getenv("HOME")
		if home == "" {
			return "", fmt.Errorf("Core ownership persistence requires HOME or XDG_STATE_HOME")
		}
		p = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(p, "rota", "herdr-core"), nil
}
func coreEncode(v coreHandle) string {
	b, _ := json.Marshal(v)
	return "core:" + base64.RawURLEncoding.EncodeToString(b)
}
func coreDecode(s string) (coreHandle, error) {
	var v coreHandle
	b, e := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(s, "core:"))
	if !strings.HasPrefix(s, "core:") || e != nil || json.Unmarshal(b, &v) != nil || v.Endpoint == "" || v.Core == "" || v.Address == "" || v.Token == "" || v.Created == 0 || v.PID <= 0 {
		return v, fmt.Errorf("invalid Core worker handle; recover its original identity")
	}
	return v, nil
}
func (h *coreHost) recordPath(v coreHandle) (string, error) {
	d, e := h.stateDir()
	if e != nil {
		return "", e
	}
	sum := sha256.Sum256([]byte(v.Endpoint + "\x00" + v.Core + "\x00" + v.Token))
	return filepath.Join(d, hex.EncodeToString(sum[:])+".json"), nil
}
func (h *coreHost) save(v coreHandle) error {
	p, e := h.recordPath(v)
	if e != nil {
		return e
	}
	if e = h.d.FS.MkdirAll(filepath.Dir(p), 0700); e != nil {
		return e
	}
	b, _ := json.Marshal(v)
	return h.d.FS.WriteFileAtomic(p, b)
}
func (h *coreHost) owned(s string) (coreHandle, error) {
	v, e := coreDecode(s)
	if e != nil {
		return v, e
	}
	p, e := h.recordPath(v)
	if e != nil {
		return v, e
	}
	b, e := h.d.FS.ReadFile(p)
	if e != nil {
		return v, fmt.Errorf("Core ownership evidence unavailable: %w", e)
	}
	var disk coreHandle
	if json.Unmarshal(b, &disk) != nil || coreEncode(v) != coreEncode(disk) {
		return v, fmt.Errorf("Core ownership evidence differs from handle")
	}
	return v, nil
}
func coreMatch(v coreHandle, t coreTerminal) bool {
	return t.Address == v.Address && t.Created == v.Created && t.Labels["rota.owner"] == v.Token && t.Labels["rota.slot"] == v.Slot
}
func coreProgram(v coreHandle, t coreTerminal) error {
	if !coreMatch(v, t) {
		return fmt.Errorf("Core worker identity changed; recover/reassign")
	}
	if t.State != "live" || t.PID != v.PID || t.Reason != "" || t.Screen != "exact" {
		return fmt.Errorf("Core worker program lost or replaced by a shell; recover/reassign")
	}
	return nil
}
func coreReady(t coreTerminal) error {
	if !t.Reported || t.Status == nil || !harness.CoreStatusSupported(t.Status.App) {
		return fmt.Errorf("Core worker needs supported native OSC 7501 reporting; inspect startup/authentication")
	}
	switch t.Status.State {
	case "idle", "done", "working":
		return nil
	case "blocked":
		return ErrDialogOpen
	default:
		return fmt.Errorf("Core worker native state %q is not ready", t.Status.State)
	}
}

// guard is the pre-send input check. A harness that opts out of the composer
// check (Claude) still refuses an open dialog; everyone else needs an empty,
// recognized composer.
func (h *coreHost) guard(ctx context.Context, v coreHandle, t coreTerminal, plain string) error {
	if t.Status != nil && !harness.CoreComposerChecked(t.Status.App) {
		if d, ok := ReadDialog(plain); ok {
			return &DialogError{First: d.First}
		}
		return nil
	}
	return h.composer(ctx, v, t, plain)
}
func coreComposer(text string) error {
	if d, ok := ReadDialog(text); ok {
		return &DialogError{First: d.First}
	}
	i := coreComposerStart(text)
	if i < 0 {
		return fmt.Errorf("%w: no recognized Codex composer", ErrNotSubmitted)
	}
	// A blank marker row does not establish empty input: retained text may
	// continue on following rows. Plain text is only a conservative hint;
	// the styled observer supplies the authoritative composer boundaries.
	if strings.TrimSpace(text[i+len("›"):]) != "" {
		return ErrDraftOnPrompt
	}
	return nil
}

// Native Codex renders its prompt at the left margin and indents draft
// continuations. A prompt-like rune inside that content is not a boundary.
// This hint never authorizes input without the styled observer.
func coreComposerStart(text string) int {
	start, offset := -1, 0
	for _, line := range strings.SplitAfter(text, "\n") {
		if strings.HasPrefix(line, "› ") || line == "›\n" || line == "›" {
			start = offset
		}
		offset += len(line)
	}
	return start
}
func (h *coreHost) Spawn(ctx context.Context, o SpawnOpts) (string, error) {
	if !filepath.IsAbs(o.Cwd) {
		return "", fmt.Errorf("Core worker requires an absolute worktree directory")
	}
	o.Cwd = filepath.Clean(o.Cwd)
	c, parent, e := h.caller(ctx)
	if e != nil {
		return "", e
	}
	defer c.Close()
	space := parent.Labels["herdr.space"]
	if space == "" || strings.Contains(space, "/") {
		return "", fmt.Errorf("Core caller has no local herdr.space; open an app project terminal")
	}
	path, e := h.spawnIntentPath(c.core, o.Slot, o.Cwd)
	if e != nil {
		return "", e
	}
	if e = h.d.FS.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return "", e
	}
	var handle string
	e = h.d.FS.Locked(path, 30*time.Second, func() error { var err error; handle, err = h.spawnLocked(ctx, c, parent, o, path); return err })
	return handle, e
}
func (h *coreHost) spawnLocked(ctx context.Context, c *coreConn, parent coreTerminal, o SpawnOpts, intentPath string) (result string, failure error) {
	intent, t, e := h.spawnTerminal(ctx, c, parent, o, intentPath)
	if e != nil {
		return h.pendingFor(intent), e
	}
	address := t.Address
	defer func() {
		if failure != nil && address != "" {
			failure = fmt.Errorf("Core worker %s: %w", address, failure)
		}
	}()
	token := intent.Token
	v := coreHandle{Endpoint: h.d.Getenv("HC_TERMINAL_SOCKET"), Core: c.core, Address: t.Address, Token: token, Slot: o.Slot, Cwd: o.Cwd, Created: t.Created, PID: t.PID}
	s := intent.Handle
	if s != "" {
		v, e = h.owned(s)
		if e != nil {
			return s, e
		}
	} else {
		if t.PID > 0 {
			v.PIDs = h.d.Tree(t.PID)
		}
		if v.PID <= 0 || v.Created == 0 || !coreMatch(v, t) || t.Parent != nil || t.DieWithParent {
			return h.pendingFor(intent), fmt.Errorf("Core spawn returned incomplete identity; inspect terminal %s", t.Address)
		}
		s = coreEncode(v)
		if e = h.save(v); e != nil {
			return h.pendingFor(intent), fmt.Errorf("Core worker %s ownership could not persist: %w", t.Address, e)
		}
	}
	intent.Handle = s
	if e = h.writeSpawnIntent(intentPath, intent); e != nil {
		return s, e
	}
	if e = c.call("terminal.get", map[string]any{"terminal": v.Address}, &t); e != nil {
		return s, e
	}
	for i := 0; ; i++ {
		if e = h.checkProgram(v, t); e != nil {
			if t.State == "finished" {
				e = h.startupExit(c, t, e)
			}
			return s, e
		}
		if e = coreReady(t); e == nil && t.Status.State != "working" {
			var r struct{ Text string }
			if e = c.call("terminal.read", map[string]any{"terminal": v.Address, "lines": 0}, &r); e != nil {
				return s, e
			}
			if e = h.guard(ctx, v, t, r.Text); e == nil {
				if e = h.remember(v, t); e != nil {
					return s, e
				}
				return s, nil
			}
		}
		if i >= o.BootTimeout {
			if e == nil {
				e = fmt.Errorf("native state is still working")
			}
			return s, fmt.Errorf("Core worker %s did not establish reporting/readiness: %w", v.Address, e)
		}
		h.d.Sleep(time.Second)
		if e = c.call("terminal.get", map[string]any{"terminal": v.Address}, &t); e != nil {
			return s, e
		}
	}
}
func (h *coreHost) terminal(ctx context.Context, s string) (coreHandle, *coreConn, coreTerminal, error) {
	if strings.HasPrefix(s, corePendingPrefix) {
		return coreHandle{}, nil, coreTerminal{}, fmt.Errorf("Core worker creation outcome pending; recover/reap its original owned terminal before dispatch")
	}
	v, e := h.owned(s)
	if e != nil {
		return v, nil, coreTerminal{}, e
	}
	c, e := h.connect(ctx, v.Endpoint, v.Core)
	if e != nil {
		return v, nil, coreTerminal{}, e
	}
	var t coreTerminal
	e = c.call("terminal.get", map[string]any{"terminal": v.Address}, &t)
	if e != nil {
		c.Close()
		return v, nil, t, e
	}
	if coreProgram(v, t) == nil {
		if e = h.remember(v, t); e != nil {
			c.Close()
			return v, nil, t, e
		}
	}
	return v, c, t, nil
}
func (h *coreHost) ReadPane(ctx context.Context, slot, s string, lines int) (string, error) {
	v, c, t, e := h.terminal(ctx, s)
	if e != nil {
		return "", e
	}
	defer c.Close()
	if e = h.checkProgram(v, t); e != nil {
		return "", e
	}
	var r struct{ Text string }
	e = c.call("terminal.read", map[string]any{"terminal": v.Address, "lines": max(0, lines)}, &r)
	return r.Text, e
}
func (h *coreHost) Capture(ctx context.Context, slot, s string, lines int) string {
	v, _ := h.ReadPane(ctx, slot, s, lines)
	return v
}
func (h *coreHost) Status(ctx context.Context, slot, s string) string {
	if s == "" {
		return ""
	}
	v, c, t, e := h.terminal(ctx, s)
	if e != nil {
		return UnverifiablePrefix + e.Error()
	}
	defer c.Close()
	if h.checkProgram(v, t) != nil {
		return UnverifiablePrefix + "worker program was lost or replaced by a shell; recover/reassign"
	}
	if t.Status != nil && t.Reported {
		return t.Status.State
	}
	return ""
}
func (h *coreHost) Explain(ctx context.Context, slot, s string) string {
	v, c, t, e := h.terminal(ctx, s)
	if e == nil {
		defer c.Close()
		e = h.checkProgram(v, t)
	}
	if e == nil {
		e = coreReady(t)
	}
	if e != nil {
		return e.Error()
	}
	return "Core " + v.Core + " terminal " + v.Address
}
func (h *coreHost) Notify(context.Context, string, string) {}
func (h *coreHost) Label(ctx context.Context, slot, s, title string) {
	v, c, t, e := h.terminal(ctx, s)
	if e != nil {
		return
	}
	defer c.Close()
	if !coreMatch(v, t) {
		return
	}
	var r coreTerminal
	c.call("terminal.label", map[string]any{"terminal": v.Address, "labels": map[string]string{"rota.title": title}}, &r)
}
func (h *coreHost) LabelWorkspace(context.Context, string) {}

// Delivery is a durable client receipt, not a Core exactly-once guarantee.
// Persist unknown BEFORE requesting input; any crash or transport failure then
// reconciles evidence without automatically pasting a second copy.
type coreDelivery struct {
	State  string
	Before string
	// Sent marks a send Core accepted; Turn and Was are the terminal's turn
	// and native status read just before it, the baseline pickup evidence
	// is measured against.
	Sent bool
	Turn uint64
	Was  string
}

// corePickedUp reports whether Core's own turn and status show a sent message
// was taken up (#2640). Core's turn advances each time status leaves working.
// From idle, any working state or a later turn follows the message. From
// working, the running turn ends first (Turn+1) and a queued message then
// starts working on it or ends its own turn (Turn+2). This is pickup evidence
// only: it never stands for successful completion.
func corePickedUp(d coreDelivery, t coreTerminal) bool {
	if !d.Sent || !t.Reported || t.Status == nil {
		return false
	}
	if d.Was == "working" {
		return t.Turn >= d.Turn+2 || (t.Turn == d.Turn+1 && t.Status.State == "working")
	}
	return t.Turn > d.Turn || t.Status.State == "working"
}

// deliveryEvidence is the screen evidence, upgraded to picked-up when Core's
// turn shows the message was taken up.
func deliveryEvidence(screen, text string, t coreTerminal, d coreDelivery) string {
	state := coreDeliveryEvidence(screen, text, t, d.Before)
	if state != "picked-up" && corePickedUp(d, t) {
		return "picked-up"
	}
	return state
}

func deliveryError(state string) error {
	switch state {
	case "picked-up":
		return nil
	case "queued":
		return ErrQueued
	case "submitted":
		return ErrSubmitted
	default:
		return ErrDeliveryUnknown
	}
}
func (h *coreHost) Send(ctx context.Context, slot, s, file string) error {
	text, e := h.d.readFile(file)
	if e != nil {
		return fmt.Errorf("%w: %v", ErrNotSubmitted, e)
	}
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return ErrNotSubmitted
	}
	v, e := h.owned(s)
	if e != nil {
		return e
	}
	p, e := h.recordPath(v)
	if e != nil {
		return e
	}
	sum := sha256.Sum256([]byte(text))
	receipt := p + "." + hex.EncodeToString(sum[:]) + ".delivery"
	return h.d.FS.Locked(receipt, 30*time.Second, func() error {
		var prior coreDelivery
		b, readErr := h.d.FS.ReadFile(receipt)
		if readErr == nil {
			if json.Unmarshal(b, &prior) != nil {
				return ErrDeliveryUnknown
			}
		} else if !errors.Is(readErr, os.ErrNotExist) {
			return readErr
		}
		v, c, t, e := h.terminal(ctx, s)
		if e != nil {
			return e
		}
		defer c.Close()
		if e = h.checkProgram(v, t); e != nil {
			return e
		}
		var screen struct{ Text string }
		if e = c.call("terminal.read", map[string]any{"terminal": v.Address, "lines": 200}, &screen); e != nil {
			return e
		}
		save := func(state string) error {
			prior.State = state
			b, _ := json.Marshal(prior)
			if e = h.d.FS.WriteFileAtomic(receipt, b); e != nil {
				return fmt.Errorf("%w: receipt persistence: %v", ErrDeliveryUnknown, e)
			}
			return deliveryError(state)
		}
		if prior.State != "" {
			state := deliveryEvidence(screen.Text, text, t, prior)
			if state != "" {
				return save(state)
			}
			return deliveryError(prior.State)
		}
		if e = coreReady(t); e != nil {
			return e
		}
		if e = h.guard(ctx, v, t, screen.Text); e != nil {
			return e
		}
		prior.Before = screen.Text
		prior.State = "unknown"
		prior.Turn, prior.Was = t.Turn, t.Status.State
		b, _ = json.Marshal(prior)
		if e = h.d.FS.WriteFileAtomic(receipt, b); e != nil {
			return fmt.Errorf("%w: cannot persist receipt: %v", ErrNotSubmitted, e)
		}
		var result struct{ Turn uint64 }
		params := map[string]any{"terminal": v.Address, "text": text, "force": false, "wait": false}
		switch t.Status.State {
		case "working":
			params["if"] = "working"
		case "idle":
			params["if"] = "idle"
		case "done":
			params["if"] = "done"
		}
		if e = c.call("terminal.send", params, &result); e != nil {
			var rpc *coreRPCError
			if errors.As(e, &rpc) && (rpc.Data.Kind == "blocked" || rpc.Data.Kind == "status_mismatch") {
				if e = h.d.FS.Remove(receipt); e != nil {
					return ErrDeliveryUnknown
				}
				if rpc.Data.Kind == "blocked" {
					return ErrDialogOpen
				}
				return ErrNotSubmitted
			}
			return fmt.Errorf("%w: %v", ErrDeliveryUnknown, e)
		}
		prior.Sent = true
		if e = c.call("terminal.get", map[string]any{"terminal": v.Address}, &t); e != nil {
			return save("submitted")
		}
		if h.checkProgram(v, t) != nil {
			return save("unknown")
		}
		if e = c.call("terminal.read", map[string]any{"terminal": v.Address, "lines": 200}, &screen); e != nil {
			return save("submitted")
		}
		if state := deliveryEvidence(screen.Text, text, t, prior); state != "" {
			return save(state)
		}
		return save("submitted")
	})
}
func coreDeliveryEvidence(screen, text string, t coreTerminal, before string) string {
	output := screen
	if i := strings.LastIndex(screen, "›"); i >= 0 {
		output = screen[:i]
	}
	old := before
	if i := strings.LastIndex(before, "›"); i >= 0 {
		old = before[:i]
	}
	queueStart := func(s string) int {
		at := -1
		for _, heading := range []string{"Queued follow-up inputs", "Messages to be submitted after next tool call", "Messages to be submitted at end of turn"} {
			if i := strings.Index(s, heading); i >= 0 && (at < 0 || i < at) {
				at = i
			}
		}
		return at
	}
	marker := ""
	for _, line := range strings.Split(text, "\n") {
		if seq := harness.RelaySequence(line); seq != "" {
			marker = seq
			break
		}
	}
	seenMarker := func(s string) bool {
		for _, line := range strings.Split(s, "\n") {
			line = strings.TrimSpace(line)
			line = strings.TrimSpace(strings.TrimPrefix(line, "↳"))
			if harness.RelaySequence(line) == marker && marker != "" {
				return true
			}
		}
		return false
	}
	// Only actual pending-input preview sections establish queue delivery. A
	// working native status plus a normal prompt echo is pickup, never a queue.
	q := queueStart(output)
	oldQueue := queueStart(old)
	if q >= 0 && seenMarker(output[q:]) && (oldQueue < 0 || !seenMarker(old[oldQueue:])) {
		return "queued"
	}
	// A signed trailer uniquely identifies the submitted payload even when a
	// long brief's opening lines are outside the captured history. Exclude the
	// active composer and every pending-input preview, and require fresh echo.
	if q >= 0 {
		output = output[:q]
	}
	if oldQueue >= 0 {
		old = old[:oldQueue]
	}
	signature := ""
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "--- ROTA-SIG ") && strings.HasSuffix(line, " ---") {
			digest := strings.TrimSuffix(strings.TrimPrefix(line, "--- ROTA-SIG "), " ---")
			if b, e := hex.DecodeString(digest); e == nil && len(b) == 32 {
				signature = line
			}
		}
	}
	if signature != "" && strings.Contains(squeeze(output), squeeze(signature)) && !strings.Contains(squeeze(old), squeeze(signature)) {
		return "picked-up"
	}
	return ""
}
func (h *coreHost) Kill(ctx context.Context, slot, s string) error {
	if s == "" {
		return nil
	}
	if strings.HasPrefix(s, corePendingPrefix) {
		resolved, e := h.resolvePending(ctx, s)
		if e != nil {
			return e
		}
		s = resolved
	}
	v, e := h.owned(s)
	if e != nil {
		return e
	}
	c, e := h.connect(ctx, v.Endpoint, v.Core)
	if e != nil {
		return e
	}
	var t coreTerminal
	e = c.call("terminal.get", map[string]any{"terminal": v.Address}, &t)
	var rpc *coreRPCError
	missing := errors.As(e, &rpc) && rpc.Data.Kind == "not_found"
	if e != nil && !missing {
		c.Close()
		return e
	}
	defer c.Close()
	if !missing && !coreMatch(v, t) {
		return fmt.Errorf("refusing Core kill: terminal ownership changed")
	}
	ev, e := h.killEvidence(v, t)
	if e != nil {
		return e
	}
	if e = h.rememberPIDs(v, ev.observed); e != nil {
		return fmt.Errorf("Core pre-close process evidence could not persist: %w", e)
	}
	var out coreTerminal
	if !missing {
		if e = c.call("terminal.kill", map[string]any{"terminal": v.Address}, &out); e != nil {
			return e
		}
	}
	probe := ev.aliveDeps(h)
	alive, ok := killLoop(&probe, ev.pids(), func() bool {
		if missing {
			return true
		}
		var now coreTerminal
		err := c.call("terminal.get", map[string]any{"terminal": v.Address}, &now)
		return err == nil && coreMatch(v, now) && now.State == "finished"
	})
	if !ok {
		return fmt.Errorf("Core terminal %s cleanup unproved; surviving pids:%s; ownership evidence retained", v.Address, pidList(alive))
	}
	return h.clearSpawnIntent(v)
}
func (h *coreHost) Snapshot(ctx context.Context) ([]Agent, error) {
	c, _, e := h.caller(ctx)
	if e != nil {
		return nil, e
	}
	defer c.Close()
	var r struct{ Terminals *[]coreTerminal }
	if e = c.call("terminal.list", map[string]any{"all": true}, &r); e != nil {
		return nil, e
	}
	if r.Terminals == nil {
		return nil, fmt.Errorf("Core snapshot omitted terminals")
	}
	d, e := h.stateDir()
	if e != nil {
		return nil, e
	}
	files, e := h.d.FS.Glob(filepath.Join(d, "*.json"))
	if e != nil {
		return nil, e
	}
	owned := map[string]coreHandle{}
	for _, p := range files {
		b, e := h.d.FS.ReadFile(p)
		var v coreHandle
		if e == nil && json.Unmarshal(b, &v) == nil && v.Address != "" && v.Token != "" && v.Created > 0 && v.PID > 0 && v.Core == c.core && v.Endpoint == h.d.Getenv("HC_TERMINAL_SOCKET") {
			owned[v.Address] = v
		}
	}
	out := []Agent{}
	for _, t := range *r.Terminals {
		v, ok := owned[t.Address]
		if !ok || t.State != "live" {
			continue
		}
		status := ""
		if t.Status != nil {
			status = t.Status.State
		}
		if h.checkProgram(v, t) != nil {
			status = UnverifiablePrefix + "worker program was lost or replaced by a shell; recover/reassign"
		}
		out = append(out, Agent{Tab: coreEncode(v), Name: v.Slot, Cwd: v.Cwd, Status: status})
	}
	return out, nil
}

// Process evidence lives beside the immutable identity so later descendants
// remain provable after their root exits or Core replaces it with a shell.
func (h *coreHost) remember(v coreHandle, t coreTerminal) error {
	if coreProgram(v, t) != nil {
		return nil
	}
	return h.rememberPIDs(v, h.d.Tree(t.PID))
}
func (h *coreHost) invalidate(v coreHandle) error {
	p, e := h.recordPath(v)
	if e != nil {
		return e
	}
	return h.d.FS.WriteFileAtomic(p+".lost", []byte("worker program was lost or replaced; recover/reassign"))
}
func (h *coreHost) checkProgram(v coreHandle, t coreTerminal) error {
	p, e := h.recordPath(v)
	if e != nil {
		return e
	}
	if _, e = h.d.FS.ReadFile(p + ".lost"); e == nil {
		return fmt.Errorf("Core worker program was lost or replaced by a shell; recover/reassign")
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	return coreProgram(v, t)
}

// Startup output belongs to this newly owned worker, before any brief is sent.
// Preserve the original failure if its best-effort read is unavailable.
func (h *coreHost) startupExit(c *coreConn, t coreTerminal, cause error) error {
	diagnostic := fmt.Sprintf("program finished during startup (reason=%s", t.Reason)
	if t.ExitCode != nil {
		diagnostic += fmt.Sprintf(", exit=%d", *t.ExitCode)
	}
	if t.Signal != nil {
		diagnostic += fmt.Sprintf(", signal=%d", *t.Signal)
	}
	diagnostic += ")"
	var read struct{ Text string }
	if e := c.call("terminal.read", map[string]any{"terminal": t.Address, "lines": 20}, &read); e == nil {
		lines := strings.Split(strings.TrimSpace(read.Text), "\n")
		if len(lines) > 8 {
			lines = lines[len(lines)-8:]
		}
		tail := strings.Join(lines, "\n")
		if len(tail) > 2048 {
			tail = tail[len(tail)-2048:]
		}
		if tail != "" {
			diagnostic += "; terminal output:\n" + tail
		}
	}
	return fmt.Errorf("%s: %w", diagnostic, cause)
}
