package host

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/harness"
)

// The fake speaks actual named Core RPC, with no executable dependency.
func coreFixture(t *testing.T) (Host, *coreFake) {
	t.Helper()
	f := &coreFake{screen: "› \n", styled: "\x18\x1b[H\x1b[2J› \x1b[1;3H\x1b[?9001l", cols: 100, rows: 30, status: "idle", pid: 42, core: "fixture-core", state: "live", events: make(chan map[string]any, 8), labels: map[string]string{"herdr.space": "s1", "herdr.tab": "parent-tab"}}
	t.Cleanup(func() { close(f.events) })
	dir := t.TempDir()
	d := Deps{Run: func(context.Context, string, []string) (Result, error) {
		return Result{}, fmt.Errorf("unexpected executable")
	}, Getenv: func(k string) string {
		return map[string]string{"HC_TERMINAL_SOCKET": "/fixture.sock", "HC_TERMINAL_ID": "t1", "ROTA_CORE_STATE_DIR": dir, "PATH": "/fixture/codex/bin:/usr/bin", "TERM": "xterm-256color"}[k]
	}, Dial: func(ctx context.Context, path string) (net.Conn, error) {
		a, b := net.Pipe()
		go f.serve(b)
		return a, nil
	}, Alive: func(int) bool { return false }, Tree: func(p int) []int { return []int{p} }}
	return New(HerdrCore, d), f
}

type coreFake struct {
	mu                 sync.Mutex
	screen, status     string
	pid, turn          int
	labels             map[string]string
	sends              int
	run                map[string]any
	lose               bool
	core, state, after string
	styled             string
	cols, rows         int
	observes           int
	blocked            bool
	kills              int
	missing            bool
	loseRun            bool
	runs               int
	extras             []map[string]any
	killed             []string
	events             chan map[string]any
	app                string
	sendIf             []any
	unreported         bool
}

func (f *coreFake) serve(c net.Conn) {
	defer c.Close()
	d := json.NewDecoder(c)
	e := json.NewEncoder(c)
	for {
		var q struct {
			ID     int            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if d.Decode(&q) != nil {
			return
		}
		f.mu.Lock()
		var v any
		term := func(addr string) map[string]any {
			state := f.state
			if addr == "t1" {
				state = "live"
			}
			m := map[string]any{"address": addr, "created_ms": 123, "cwd": "/work", "argv": []string{"codex"}, "state": state, "cols": f.cols, "rows": f.rows, "screen": "exact", "pid": f.pid, "labels": f.labels, "reported": !f.unreported, "status": map[string]string{"app": f.appName(), "state": f.status}, "turn": f.turn}
			if f.unreported {
				delete(m, "status")
			}
			return m
		}
		switch q.Method {
		case "hello":
			v = map[string]any{"generation": 1, "framed": q.Params["framed"], "core": map[string]string{"id": f.core}, "methods": []string{"terminal.run", "terminal.get", "terminal.read", "terminal.attach", "terminal.send", "terminal.kill", "terminal.list", "events.subscribe"}}
		case "terminal.get":
			if f.missing && q.Params["terminal"] == "t2" {
				f.mu.Unlock()
				e.Encode(map[string]any{"jsonrpc": "2.0", "id": q.ID, "error": map[string]any{"code": -32000, "message": "not found", "data": map[string]string{"kind": "not_found"}}})
				continue
			}
			v = term(q.Params["terminal"].(string))
		case "terminal.run":
			f.runs++
			f.run = q.Params
			f.labels = map[string]string{}
			for k, v := range q.Params["labels"].(map[string]any) {
				f.labels[k] = v.(string)
			}
			if f.loseRun {
				f.mu.Unlock()
				return
			}
			v = term("t2")
		case "terminal.read":
			v = map[string]any{"text": f.screen, "complete": true, "how": "screen"}
		case "terminal.send":
			if f.blocked {
				f.mu.Unlock()
				e.Encode(map[string]any{"jsonrpc": "2.0", "id": q.ID, "error": map[string]any{"code": -32000, "message": "blocked", "data": map[string]string{"kind": "blocked"}}})
				continue
			}
			f.sends++
			f.sendIf = append(f.sendIf, q.Params["if"])
			if f.lose {
				f.mu.Unlock()
				return
			}
			if f.after != "" {
				f.screen = f.after
			}
			v = map[string]any{"turn": 1, "started": true}
		case "terminal.kill":
			f.kills++
			f.killed = append(f.killed, q.Params["terminal"].(string))
			f.state = "finished"
			v = term("t2")
		case "terminal.list", "events.subscribe":
			terminals := []any{term("t2")}
			for _, extra := range f.extras {
				terminals = append(terminals, extra)
			}
			v = map[string]any{"terminals": terminals, "seq": 1}
		default:
			v = term("t2")
		}
		f.mu.Unlock()
		if e.Encode(map[string]any{"jsonrpc": "2.0", "id": q.ID, "result": v}) != nil {
			return
		}

		if q.Method == "hello" && q.Params["framed"] == true {
			var header [9]byte
			if _, err := io.ReadFull(c, header[:]); err != nil {
				return
			}
			body := make([]byte, binary.LittleEndian.Uint32(header[5:]))
			if _, err := io.ReadFull(c, body); err != nil {
				return
			}
			var req struct {
				ID     int
				Method string
				Params map[string]any
			}
			if json.Unmarshal(body, &req) != nil {
				return
			}
			if req.Method != "terminal.attach" || req.Params["observe"] != true {
				return
			}
			f.mu.Lock()
			f.observes++
			paint := f.styled
			terminal := term("t2")
			f.mu.Unlock()
			write := func(channel uint32, p []byte) {
				var h [9]byte
				binary.LittleEndian.PutUint32(h[:], channel)
				binary.LittleEndian.PutUint32(h[5:], uint32(len(p)))
				c.Write(h[:])
				c.Write(p)
			}
			reply, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"channel": 1, "terminal": terminal, "resize_notifications": false}})
			write(0, reply)
			write(1, []byte(paint))
			return
		}
		if q.Method == "events.subscribe" {
			for n := range f.events {
				if e.Encode(n) != nil {
					return
				}
			}
			return
		}
	}
}
func coreSpawn(t *testing.T, h Host) string {
	t.Helper()
	s, e := h.Spawn(context.Background(), SpawnOpts{Slot: "dana", Cwd: "/work", Launch: "codex", BootTimeout: 1})
	if e != nil {
		t.Fatal(e)
	}
	return s
}

func TestCoreAdapterSpawnCarriesScopedRotaContext(t *testing.T) {
	host, f := coreFixture(t)
	h := host.(*coreHost)
	original := h.d.Getenv
	values := map[string]string{"ROTA_CONFIG_DIR": "/fixture/config", "ROTA_CORE_STATE_DIR": original("ROTA_CORE_STATE_DIR"), "ROTA_ROUND_HOLDER_PID": "1234", "ROTA_SLOT": "parent", "ROTA_ATTEMPT": "parent-attempt", "API_TOKEN": "do-not-copy", "ROTA_UNKNOWN": "do-not-copy"}
	h.d.Getenv = func(k string) string {
		if value, ok := values[k]; ok {
			return value
		}
		return original(k)
	}
	if _, err := h.Spawn(context.Background(), SpawnOpts{Slot: "dana", Cwd: "/work", Launch: "codex", Env: []string{"ROTA_SLOT=dana", "ROTA_PORT_BASE=40000"}}); err != nil {
		t.Fatal(err)
	}
	env := f.run["env"].(map[string]any)
	for _, key := range []string{"ROTA_CONFIG_DIR", "ROTA_CORE_STATE_DIR", "ROTA_ROUND_HOLDER_PID"} {
		if env[key] != values[key] {
			t.Errorf("Core child context %s=%v want %q", key, env[key], values[key])
		}
	}
	if env["ROTA_SLOT"] != "dana" || env["ROTA_PORT_BASE"] != "40000" {
		t.Fatal("child slot allocation changed")
	}
	for _, key := range []string{"HC_TERMINAL_ID", "HC_TERMINAL_SOCKET", "ROTA_ATTEMPT", "API_TOKEN", "ROTA_UNKNOWN"} {
		if _, ok := env[key]; ok {
			t.Errorf("parent identity or unrelated environment copied: %s", key)
		}
	}
}

func TestCoreAdapterPendingSpawnRetainsOriginalRotaContext(t *testing.T) {
	host, f := coreFixture(t)
	h := host.(*coreHost)
	original := h.d.Getenv
	configDir := "/fixture/first"
	h.d.Getenv = func(k string) string {
		if k == "ROTA_CONFIG_DIR" {
			return configDir
		}
		return original(k)
	}
	f.loseRun = true
	opts := SpawnOpts{Slot: "dana", Cwd: "/work", Launch: "codex"}
	if _, err := h.Spawn(context.Background(), opts); err == nil {
		t.Fatal("lost create response acknowledged")
	}
	configDir = "/fixture/second"
	f.loseRun = false
	if _, err := h.Spawn(context.Background(), opts); err == nil || !strings.Contains(err.Error(), "different launch settings") {
		t.Fatalf("pending create reused a different Rota context: %v", err)
	}
	if f.runs != 1 {
		t.Fatal("pending creation duplicated")
	}
}
func TestCoreAdapterIndependentOwnedWorker(t *testing.T) {
	h, f := coreFixture(t)
	if h.Kind() != HerdrCore {
		t.Fatalf("kind=%s", h.Kind())
	}
	s := coreSpawn(t, h)
	if s == "" {
		t.Fatal("no handle")
	}
	if f.run["env"].(map[string]any)["PATH"] != "/fixture/codex/bin:/usr/bin" {
		t.Fatal("caller PATH not passed to Core worker")
	}
	if f.run["no_parent"] != true {
		t.Fatal("worker is parent-bound")
	}
	l := f.run["labels"].(map[string]any)
	if l["herdr.space"] != "s1" || l["herdr.tab"] != nil {
		t.Fatalf("adoption labels %v", l)
	}
	if !h.Capabilities().RuntimeScopedHandles || !h.Capabilities().TreeKillClose {
		t.Fatal("unsafe capabilities")
	}
}
func TestCoreAdapterNeverReplaysUncertainSend(t *testing.T) {
	h, f := coreFixture(t)
	s := coreSpawn(t, h)
	p := filepath.Join(t.TempDir(), "brief")
	os.WriteFile(p, []byte("signed assignment\nROTA-END unique-1"), 0600)
	f.lose = true
	if h.Send(context.Background(), "dana", s, p) == nil {
		t.Fatal("lost reply acknowledged")
	}
	f.lose = false
	h.Send(context.Background(), "dana", s, p)
	if f.sends != 1 {
		t.Fatalf("resubmitted uncertain send: %d", f.sends)
	}
}
func TestCoreAdapterRefusesDraftAndReplacedShell(t *testing.T) {
	h, f := coreFixture(t)
	s := coreSpawn(t, h)
	p := filepath.Join(t.TempDir(), "brief")
	os.WriteFile(p, []byte("hello"), 0600)
	f.screen = "› my human draft\n"
	if h.Send(context.Background(), "dana", s, p) == nil {
		t.Fatal("draft overwritten")
	}
	f.screen = "› \n"
	f.pid = 99
	if h.Send(context.Background(), "dana", s, p) == nil {
		t.Fatal("replacement accepted")
	}
	if f.sends != 0 {
		t.Fatalf("sent %d", f.sends)
	}
}

func coreBrief(t *testing.T, text string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "brief")
	if e := os.WriteFile(p, []byte(text), 0600); e != nil {
		t.Fatal(e)
	}
	return p
}
func TestCoreAdapterDeliveryReconciliation(t *testing.T) {
	h, f := coreFixture(t)
	s := coreSpawn(t, h)
	text := "--- ORCHESTRATOR ---\n[relay 7]\ninspect this\n--- ROTA-SIG " + strings.Repeat("a", 64) + " ---"
	p := coreBrief(t, text)
	f.status = "working"
	startTurn := f.turn
	f.after = "• Queued follow-up inputs\n  ↳ --- ORCHESTRATOR ---\n    [relay 7]\n    inspect this…\n› \n"
	if e := h.Send(context.Background(), "dana", s, p); !errors.Is(e, ErrQueued) {
		t.Fatalf("queue=%v", e)
	}
	// A new adapter represents a subsequent rota invocation with the same receipt.
	fresh := New(HerdrCore, h.(*coreHost).d)
	if e := fresh.Send(context.Background(), "dana", s, p); !errors.Is(e, ErrQueued) {
		t.Fatalf("retry=%v", e)
	}
	if f.sends != 1 {
		t.Fatalf("queued replayed %d times", f.sends)
	}
	f.status = "done"
	f.screen = text + "\n› \n"
	if e := fresh.Send(context.Background(), "dana", s, p); e != nil {
		t.Fatalf("exact pickup=%v", e)
	}
	if f.sends != 1 {
		t.Fatalf("pickup reconciliation resent %d", f.sends)
	}
}
func TestCoreAdapterNativeStartedIsOnlySubmitted(t *testing.T) {
	h, f := coreFixture(t)
	s := coreSpawn(t, h)
	p := coreBrief(t, "task")
	if e := h.Send(context.Background(), "dana", s, p); !errors.Is(e, ErrSubmitted) {
		t.Fatalf("started=true falsely proves pickup: %v", e)
	}
	if h.Capabilities().TurnReader != nil {
		t.Fatal("raw turn exposed as successful completion")
	}
	h.Send(context.Background(), "dana", s, p)
	if f.sends != 1 {
		t.Fatal("submitted payload replayed")
	}
}
func TestCoreAdapterBlockedRefusalCanRetry(t *testing.T) {
	h, f := coreFixture(t)
	s := coreSpawn(t, h)
	p := coreBrief(t, "task")
	f.blocked = true
	if e := h.Send(context.Background(), "dana", s, p); !errors.Is(e, ErrDialogOpen) {
		t.Fatalf("blocked=%v", e)
	}
	f.blocked = false
	h.Send(context.Background(), "dana", s, p)
	if f.sends != 1 {
		t.Fatalf("definite refusal prevented safe retry: %d", f.sends)
	}
}
func TestCoreAdapterIdentityAndSnapshot(t *testing.T) {
	h, f := coreFixture(t)
	s := coreSpawn(t, h)
	a, e := h.Capabilities().Snapshotter.Snapshot(context.Background())
	if e != nil || len(a) != 1 || a[0].Handle() != s {
		t.Fatalf("snapshot=%v %v", a, e)
	}
	f.core = "replacement-core"
	if e = h.Kill(context.Background(), "dana", s); e == nil {
		t.Fatal("killed on changed endpoint identity")
	}
	if f.kills != 0 {
		t.Fatal("foreign Core mutated")
	}
}
func TestCoreAdapterWatchInvalidatesRestartAndReconnect(t *testing.T) {
	h, f := coreFixture(t)
	s := coreSpawn(t, h)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	w, e := h.Capabilities().Watcher.Watch(ctx, []WatchTarget{{Slot: "dana", Handle: s}})
	if e != nil {
		t.Fatal(e)
	}
	defer w.Close()
	if slot, e := w.Next(ctx); slot != "dana" || e != nil {
		t.Fatalf("snapshot wake %q %v", slot, e)
	}
	f.events <- map[string]any{"method": "event", "params": map[string]any{"seq": 2, "type": "restarted", "terminal": "t2"}}
	if _, e = w.Next(ctx); e != nil {
		t.Fatal(e)
	}
	if !strings.HasPrefix(h.Status(ctx, "dana", s), UnverifiablePrefix) {
		t.Fatal("restart with reused pid retained readiness")
	}
	a, err := h.Capabilities().Snapshotter.Snapshot(ctx)
	if err != nil || len(a) != 1 || !strings.HasPrefix(a[0].Status, UnverifiablePrefix) {
		t.Fatalf("snapshot hid restarted program: %v %v", a, err)
	}
	f.events <- map[string]any{"method": "missed", "params": map[string]any{"seq": 3, "terminals": []any{}}}
	if _, e = w.Next(ctx); !errors.Is(e, ErrStreamLost) {
		t.Fatalf("missed=%v", e)
	}
}
func TestCoreAdapterKillProvesRetainedChildren(t *testing.T) {
	h, f := coreFixture(t)
	c := h.(*coreHost)
	childAlive := true
	c.d.Tree = func(p int) []int { return []int{p, 77} }
	c.d.Alive = func(p int) bool { return p == 77 && childAlive }
	c.d.KillWait = 1
	s := coreSpawn(t, h)
	if e := h.Kill(context.Background(), "dana", s); e == nil {
		t.Fatal("surviving child counted killed")
	}
	v, e := c.owned(s)
	if e != nil || v.Token == "" {
		t.Fatal("failure discarded ownership")
	}
	childAlive = false
	if e = h.Kill(context.Background(), "dana", s); e != nil {
		t.Fatal(e)
	}
	if f.kills != 2 {
		t.Fatalf("kills=%d", f.kills)
	}
}
func TestCoreAdapterConservativePlainComposer(t *testing.T) {
	for _, screen := range []string{"› Try \"fix tests\"\n", "› Ask Codex to do anything\n", "› 1. Trust folder\nEnter to confirm", "$ \n"} {
		if e := coreComposer(screen); e == nil {
			t.Fatalf("ambiguous composer accepted %q", screen)
		}
	}
}

func TestCoreAdapterStyledObserverAcceptsOnlyDimPlaceholder(t *testing.T) {
	h, f := coreFixture(t)
	f.screen = "› Ask Codex to do anything\n"
	f.styled = "\x18\x1b[!p\x1b[H\x1b[2J› \x1b[2mAsk Codex to do anything\x1b[0m\x1b[1;3H\x1b[?9001l"
	s, e := h.Spawn(context.Background(), SpawnOpts{Slot: "dana", Cwd: "/work", Launch: "codex", BootTimeout: 0})
	if e != nil || s == "" {
		t.Fatalf("dim placeholder readiness: %v", e)
	}
	if f.observes == 0 {
		t.Fatal("no observer attached")
	}
	f.styled = "\x18\x1b[H\x1b[2J› Ask Codex to do anything\x1b[1;3H\x1b[?9001l"
	if e = h.Send(context.Background(), "dana", s, coreBrief(t, "task")); !errors.Is(e, ErrDraftOnPrompt) {
		t.Fatalf("normal-style placeholder was not protected: %v", e)
	}
	if f.sends != 0 {
		t.Fatal("human draft overwritten")
	}
}

func TestCoreAdapterMissingHandleRequiresProcessProof(t *testing.T) {
	h, f := coreFixture(t)
	s := coreSpawn(t, h)
	c := h.(*coreHost)
	f.missing = true
	c.d.Alive = func(int) bool { return true }
	if e := h.Kill(context.Background(), "dana", s); e == nil {
		t.Fatal("missing handle declared dead with surviving process")
	}
	c.d.Alive = func(int) bool { return false }
	if e := h.Kill(context.Background(), "dana", s); e != nil {
		t.Fatalf("retained process proof did not permit reclaim: %v", e)
	}
	if f.kills != 0 {
		t.Fatal("missing terminal close attempted")
	}
}

func TestCoreAdapterKillUndispatchedSlot(t *testing.T) {
	h, f := coreFixture(t)
	if e := h.Kill(context.Background(), "dana", ""); e != nil {
		t.Fatalf("empty undispatched slot kill: %v", e)
	}
	if f.kills != 0 {
		t.Fatal("undispatched kill mutated Core")
	}
}

func TestCoreAdapterLostSpawnReplyReconcilesOwnedTerminal(t *testing.T) {
	h, f := coreFixture(t)
	f.loseRun = true
	o := SpawnOpts{Slot: "dana", Cwd: "/work", Launch: "codex"}
	if _, e := h.Spawn(context.Background(), o); e == nil {
		t.Fatal("lost spawn reply acknowledged")
	}
	f.loseRun = false
	s, e := h.Spawn(context.Background(), o)
	if e != nil || s == "" {
		t.Fatalf("pending spawn not reconciled: %v", e)
	}
	if f.runs != 1 {
		t.Fatalf("uncertain spawn duplicated terminal: runs=%d", f.runs)
	}
	if e = h.Kill(context.Background(), "dana", s); e != nil {
		t.Fatalf("reconciled owned terminal cannot clean up: %v", e)
	}
}
func TestCoreAdapterStyledPaintGuards(t *testing.T) {
	base := "\x18\x1b[!p\x1b[?1049l\x1b[H\x1b[2J\x1b[2;1H› \x1b[38;5;245mTry \"fix tests\"\x1b[0m\x1b[2;3H\x1b[?9001l"
	if e := coreStyledComposer([]byte(base), 80, 24, "› Try \"fix tests\"\n"); e != nil {
		t.Fatalf("upstream positioned gray ghost: %v", e)
	}
	for name, paint := range map[string]string{"unsupported control": strings.Replace(base, "\x1b[2;3H", "\x1b[3L", 1), "wrong cursor": strings.Replace(base, "\x1b[2;3H", "\x1b[1;3H", 1), "truncated escape": base + "\x1b["} {
		if e := coreStyledComposer([]byte(paint), 80, 24, "› Try \"fix tests\"\n"); e == nil {
			t.Fatalf("accepted %s", name)
		}
	}
	if e := coreStyledComposer([]byte(base), 80, 24, "› different human text\n"); e == nil {
		t.Fatal("conflicting plain capture accepted")
	}
	if e := coreStyledComposer([]byte(base), 10000, 24, "› Try \"fix tests\"\n"); e == nil {
		t.Fatal("oversized grid accepted")
	}
}

func TestCoreAdapterQueueAndPromptEvidence(t *testing.T) {
	sig := "--- ROTA-SIG " + strings.Repeat("b", 64) + " ---"
	text := "--- ORCHESTRATOR ---\n[relay 9]\n" + strings.Repeat("long body\n", 300) + sig
	working := coreTerminal{Status: &coreStatus{App: "codex", State: "working"}}
	for name, tc := range map[string]struct{ screen, want string }{"queued exact marker": {"• Queued follow-up inputs\n  ↳ --- ORCHESTRATOR ---\n    [relay 9]\n    first payload line…\n› \n", "queued"}, "steered exact marker": {"• Messages to be submitted after next tool call\n    [relay 9]\n› \n", "queued"}, "normal picked prompt": {"› --- ORCHESTRATOR ---\n[relay 9]\n" + sig + "\n› \n", "picked-up"}, "long clipped pickup": {"earlier body missing from requested history\n" + sig + "\n› \n", "picked-up"}, "bare marker is insufficient": {"[relay 9]\n› \n", ""}, "wrong queued relay": {"• Queued follow-up inputs\n    [relay 8]\n› \n", ""}, "trailer still on composer": {"› " + sig + "\n", ""}} {
		if got := coreDeliveryEvidence(tc.screen, text, working, "› \n"); got != tc.want {
			t.Fatalf("%s: got %q want %q", name, got, tc.want)
		}
	}
	if got := coreDeliveryEvidence(sig+"\n› \n", text, working, sig+"\n› \n"); got != "" {
		t.Fatal("old signature acknowledged as fresh pickup")
	}
}

// The same evidence for a relay in the rota-relay element (#2658): the opening
// line carries the sequence, so a preview that keeps only the first lines still
// names the relay, and herdr-core's own wrapper may sit ahead of it.
func TestCoreAdapterQueueAndPromptEvidenceForTheElement(t *testing.T) {
	sig := "--- ROTA-SIG " + strings.Repeat("b", 64) + " ---"
	open := harness.RelayOpen(35, 9)
	text := open + "\n[ORCHESTRATOR RELAY — forwarded.]\n\n" + strings.Repeat("long body\n", 300) + harness.RelayClose + "\n" + sig
	working := coreTerminal{Status: &coreStatus{App: "codex", State: "working"}}
	for name, tc := range map[string]struct{ screen, want string }{
		"queued, whole opening": {"• Queued follow-up inputs\n  ↳ " + open + "\n    [ORCHESTRATOR RELAY — forwarded.]\n    long body…\n› \n", "queued"},
		"queued, only the tag":  {"• Queued follow-up inputs\n  ↳ " + open + "\n› \n", "queued"},
		"queued behind hc":      {"• Queued follow-up inputs\n  ↳ <hc from=\"t1\">" + open + "\n› \n", "queued"},
		"steered":               {"• Messages to be submitted after next tool call\n    " + open + "\n› \n", "queued"},
		"end of turn":           {"• Messages to be submitted at end of turn\n    " + open + "\n› \n", "queued"},
		"normal picked prompt":  {"› " + open + "\n" + harness.RelayClose + "\n" + sig + "\n› \n", "picked-up"},
		"long clipped pickup":   {"earlier body missing from requested history\n" + sig + "\n› \n", "picked-up"},
		"opening proves pickup": {open + "\n› \n", "picked-up"},
		"wrong queued relay":    {"• Queued follow-up inputs\n    " + harness.RelayOpen(35, 8) + "\n› \n", ""},
		"a task brief's tag":    {"• Queued follow-up inputs\n    " + harness.RelayOpen(35, 0) + "\n› \n", ""},
	} {
		if got := coreDeliveryEvidence(tc.screen, text, working, "› \n"); got != tc.want {
			t.Fatalf("%s: got %q want %q", name, got, tc.want)
		}
	}
	if got := coreDeliveryEvidence("• Queued follow-up inputs\n  ↳ "+open+"\n› \n", text, working, "• Queued follow-up inputs\n  ↳ "+open+"\n› \n"); got != "" {
		t.Fatal("an already queued relay acknowledged as fresh")
	}
}

func TestCoreAdapterSpawnIntentScopedToWorktree(t *testing.T) {
	h, f := coreFixture(t)
	first := coreSpawn(t, h)
	second, e := h.Spawn(context.Background(), SpawnOpts{Slot: "dana", Cwd: "/other-project/worktree", Launch: "codex"})
	if e != nil || second == "" {
		t.Fatalf("unrelated project slot blocked: %v", e)
	}
	if f.runs != 2 || first == second {
		t.Fatalf("independent projects share worker identity: runs=%d", f.runs)
	}
}

func TestCoreAdapterStartupExitKeepsAddressAndOutput(t *testing.T) {
	h, f := coreFixture(t)
	f.state = "finished"
	f.screen = "/bin/sh: codex: not found\n"
	s, e := h.Spawn(context.Background(), SpawnOpts{Slot: "dana", Cwd: "/work", Launch: "codex"})
	if s == "" || e == nil || !strings.Contains(e.Error(), "t2") || !strings.Contains(e.Error(), "codex: not found") {
		t.Fatalf("startup failure lost identity/diagnostic: handle=%q error=%v", s, e)
	}
}

// Captured from an owned, never-dispatched Core Codex worker. Paths, model
// and build text are sanitized; VT layout/tab initialization is preserved.
func TestCoreAdapterCapturedCodexFirstPaint(t *testing.T) {
	data, e := os.ReadFile("testdata/core-codex-idle.json")
	if e != nil {
		t.Fatal(e)
	}
	var fixture struct {
		Paint string `json:"paint"`
		Plain string `json:"plain"`
	}
	if e = json.Unmarshal(data, &fixture); e != nil {
		t.Fatal(e)
	}
	if e = coreStyledComposer([]byte(fixture.Paint), 200, 120, fixture.Plain); e != nil {
		t.Fatalf("captured idle Codex first paint: %v", e)
	}
}
func TestCoreAdapterObserverCustomTabs(t *testing.T) {
	paint := "\x18\x1b[H\x1b[2J\x1b[3g\x1b[5G\x1bH\x1b[1G\t› \x1b[2mAsk Codex to do anything\x1b[0m\x1b[1;7H\x1b[?9001l"
	if e := coreStyledComposer([]byte(paint), 80, 24, "› Ask Codex to do anything\n"); e != nil {
		t.Fatalf("custom tabstop positioning: %v", e)
	}
}

func TestCoreAdapterObserverBackgroundIsNotDim(t *testing.T) {
	paint := "\x18\x1b[H\x1b[2J› \x1b[48;2;65;69;76mAsk Codex to do anything\x1b[0m\x1b[1;3H\x1b[?9001l"
	if e := coreStyledComposer([]byte(paint), 80, 24, "› Ask Codex to do anything\n"); !errors.Is(e, ErrDraftOnPrompt) {
		t.Fatalf("normal-style draft with Codex background was treated as ghost text: %v", e)
	}
}

func TestCoreAdapterWorkerCreationName(t *testing.T) {
	for _, tc := range []struct{ slot, prefix string }{{"Dana", "rota-dana-"}, {"Long worker@slot!", "rota-long-w-"}, {"..///", "rota-worker-"}} {
		t.Run(tc.slot, func(t *testing.T) {
			h, f := coreFixture(t)
			if _, e := h.Spawn(context.Background(), SpawnOpts{Slot: tc.slot, Cwd: "/work", Launch: "codex"}); e != nil {
				t.Fatal(e)
			}
			name, ok := f.run["name"].(string)
			if !ok || name == "" {
				t.Fatal("Core worker has no creation name")
			}
			if len(name) > 32 || !strings.HasPrefix(name, tc.prefix) {
				t.Fatalf("invalid worker name %q", name)
			}
			for _, r := range name {
				if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || strings.ContainsRune("-_.", r)) {
					t.Fatalf("invalid Core name character %q", r)
				}
			}
			token := f.run["labels"].(map[string]any)["rota.owner"].(string)
			if !strings.HasSuffix(name, "-"+token[:20]) {
				t.Fatalf("name lacks unique ownership suffix: %q", name)
			}
		})
	}
}

func TestCoreAdapterSnapshotOnlyOwnedLiveIncarnations(t *testing.T) {
	h, f := coreFixture(t)
	saved := coreSpawn(t, h)
	f.extras = []map[string]any{{"address": "t-unrelated", "created_ms": 124, "cwd": "/work", "pid": 55, "state": "live", "screen": "exact", "labels": map[string]string{}, "status": map[string]string{"state": "idle"}}}
	a, e := h.Capabilities().Snapshotter.Snapshot(context.Background())
	if e != nil || len(a) != 1 || a[0].Handle() != saved {
		t.Fatalf("snapshot exposed unrelated cwd fallback candidate: %v %v", a, e)
	}
	f.state = "finished"
	a, e = h.Capabilities().Snapshotter.Snapshot(context.Background())
	if e != nil || len(a) != 0 {
		t.Fatalf("snapshot exposed finished history: %v %v", a, e)
	}
	f.state = "live"
	f.pid = 99
	a, e = h.Capabilities().Snapshotter.Snapshot(context.Background())
	if e != nil || len(a) != 1 || a[0].Handle() != saved || !strings.HasPrefix(a[0].Status, UnverifiablePrefix) {
		t.Fatalf("replacement lost owned recovery identity: %v %v", a, e)
	}
}
func corePendingCreate(t *testing.T, h Host, f *coreFake) string {
	t.Helper()
	f.loseRun = true
	s, e := h.Spawn(context.Background(), SpawnOpts{Slot: "dana", Cwd: "/work", Launch: "codex"})
	f.loseRun = false
	if e == nil || s == "" {
		t.Fatalf("uncertain create lost residual handle: handle=%q error=%v", s, e)
	}
	return s
}
func TestCoreAdapterPendingCreationCleanup(t *testing.T) {
	h, f := coreFixture(t)
	s := corePendingCreate(t, h, f)
	if !strings.HasPrefix(h.Status(context.Background(), "dana", s), UnverifiablePrefix) {
		t.Fatal("pending create was classified ready")
	}
	if _, e := h.Capabilities().PaneReader.ReadPane(context.Background(), "dana", s, 0); e == nil {
		t.Fatal("pending handle read as normal agent")
	}
	if e := h.Send(context.Background(), "dana", s, coreBrief(t, "task")); e == nil || f.sends != 0 {
		t.Fatal("brief sent into pending creation")
	}
	if e := h.Kill(context.Background(), "dana", s); e != nil {
		t.Fatalf("owned pending terminal not reaped: %v", e)
	}
	if f.kills != 1 || len(f.killed) != 1 || f.killed[0] != "t2" {
		t.Fatalf("wrong pending target killed: %v", f.killed)
	}
	f.state = "live"
	if _, e := h.Spawn(context.Background(), SpawnOpts{Slot: "dana", Cwd: "/work", Launch: "codex"}); e != nil || f.runs != 2 {
		t.Fatalf("verified cleanup did not permit fresh worker: runs=%d error=%v", f.runs, e)
	}
}
func TestCoreAdapterPendingCleanupRefusesUnknownOrUnproved(t *testing.T) {
	for _, kind := range []string{"unknown", "ambiguous", "no-process-evidence"} {
		t.Run(kind, func(t *testing.T) {
			h, f := coreFixture(t)
			s := corePendingCreate(t, h, f)
			switch kind {
			case "unknown":
				f.labels = map[string]string{"rota.owner": "unrelated", "herdr.space": "s1"}
			case "ambiguous":
				labels := map[string]string{}
				for k, v := range f.labels {
					labels[k] = v
				}
				f.extras = []map[string]any{{"address": "t3", "created_ms": 124, "cwd": "/work", "pid": 43, "state": "live", "screen": "exact", "labels": labels}}
			case "no-process-evidence":
				h.(*coreHost).d.Tree = func(int) []int { return nil }
			}
			if e := h.Kill(context.Background(), "dana", s); e == nil {
				t.Fatal("uncertain pending cleanup counted as proved")
			}
			if f.kills != 0 {
				t.Fatalf("pending refusal killed a terminal: %v", f.killed)
			}
			if _, e := h.Spawn(context.Background(), SpawnOpts{Slot: "dana", Cwd: "/work", Launch: "codex"}); e == nil && kind != "no-process-evidence" {
				t.Fatal("unknown creation was recreated")
			}
			if f.runs != 1 {
				t.Fatal("pending record was dropped and duplicate created")
			}
		})
	}
}

func TestCoreAdapterPendingCleanupRetainsReplacementTree(t *testing.T) {
	h, f := coreFixture(t)
	s := corePendingCreate(t, h, f)
	c := h.(*coreHost)
	child := 77
	c.d.Tree = func(pid int) []int { return []int{pid, child} }
	c.d.KillWait = 1
	survivor := 77
	c.d.Alive = func(pid int) bool { return pid == survivor }
	if e := h.Kill(context.Background(), "dana", s); e == nil {
		t.Fatal("pending cleanup ignored surviving child")
	}
	f.state = "live"
	f.pid = 99
	child = 88
	survivor = 88
	if e := h.Kill(context.Background(), "dana", s); e == nil {
		t.Fatal("replacement-shell cleanup ignored surviving child")
	}
	if e := h.Kill(context.Background(), "dana", s); e == nil {
		t.Fatal("failed cleanup lost retained replacement child evidence")
	}
	survivor = 0
	if e := h.Kill(context.Background(), "dana", s); e != nil {
		t.Fatalf("retained tree gone but cleanup refused: %v", e)
	}
}

// #2640: Core's turn confirms pickup the screen never showed. From idle the
// next turn (or working) follows the message; from working the running turn
// ends first, and only working again or a second turn is the message's own.
func TestCoreAdapterTurnConfirmsPickup(t *testing.T) {
	for _, tc := range []struct {
		name, was string
		steps     []struct {
			turn  int
			state string
			want  error
		}
	}{
		{"idle", "idle", []struct {
			turn  int
			state string
			want  error
		}{{0, "idle", ErrSubmitted}, {1, "done", nil}}},
		{"working", "working", []struct {
			turn  int
			state string
			want  error
		}{{1, "done", ErrSubmitted}, {1, "working", nil}}},
		{"working two turns", "working", []struct {
			turn  int
			state string
			want  error
		}{{2, "idle", nil}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, f := coreFixture(t)
			s := coreSpawn(t, h)
			p := coreBrief(t, "task")
			f.status = tc.was
			if e := h.Send(context.Background(), "dana", s, p); !errors.Is(e, ErrSubmitted) {
				t.Fatalf("first send = %v, want submitted", e)
			}
			for _, st := range tc.steps {
				f.turn, f.status = st.turn, st.state
				fresh := New(HerdrCore, h.(*coreHost).d)
				if e := fresh.Send(context.Background(), "dana", s, p); !errors.Is(e, st.want) && !(st.want == nil && e == nil) {
					t.Fatalf("turn %d %s: %v, want %v", st.turn, st.state, e, st.want)
				}
			}
			if f.sends != 1 {
				t.Fatalf("reconciliation resent: %d sends", f.sends)
			}
		})
	}
}

func TestCorePickedUpNeedsASentMessage(t *testing.T) {
	working := coreTerminal{Reported: true, Status: &coreStatus{State: "working"}, Turn: 5}
	if corePickedUp(coreDelivery{Turn: 0, Was: "idle"}, working) {
		t.Error("a receipt Core never accepted counts as picked up")
	}
	if corePickedUp(coreDelivery{Sent: true, Turn: 5, Was: "idle"}, coreTerminal{Status: &coreStatus{State: "working"}, Turn: 5}) {
		t.Error("an unreported status counts as picked up")
	}
}

// Claude can consume a relay inside the current turn: Core only advances once
// when that turn ends, so the fresh unsigned prompt echo must prove pickup.
func TestCoreAdapterUnsignedRelayAbsorbedIntoRunningTurn(t *testing.T) {
	h, f := coreFixture(t)
	f.app = "claude-code"
	f.screen = "❯ \n"
	s, err := h.Spawn(context.Background(), SpawnOpts{Slot: "dana", Cwd: "/work", Launch: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	text := "<rota-relay n=\"9\" round=\"50\" from=\"orchestrator\">\nRead the relay.\n</rota-relay>"
	p := coreBrief(t, text)
	f.status = "working"
	if err := h.Send(context.Background(), "dana", s, p); !errors.Is(err, ErrSubmitted) {
		t.Fatalf("initial send = %v, want submitted", err)
	}
	f.turn, f.status = 1, "done"
	f.screen = "❯ <hc from=\"t1\"><rota-relay n=\"9\" round=\"50\" from=\"orchestrator\">\n  Read the relay.\n  </rota-relay></hc>\n\n● Followed the relay.\n❯ \n"
	fresh := New(HerdrCore, h.(*coreHost).d)
	t.Logf("Core turn delta=%d; submitted copies=%d", f.turn-startTurn, f.sends)
	if err := fresh.Send(context.Background(), "dana", s, p); err != nil {
		t.Fatalf("fresh unsigned relay echo after one completed turn = %v, want picked up", err)
	}
	if f.sends != 1 {
		t.Fatalf("reconciliation sent %d copies, want 1", f.sends)
	}
}

func TestCoreUnsignedRelayScreenEvidence(t *testing.T) {
	const opening = `<rota-relay n="9" round="50" from="orchestrator">`
	text := opening + "\nRead the relay.\n</rota-relay>"
	for _, tc := range []struct {
		name, screen, before, want string
	}{
		{"fresh Claude echo", "❯ " + opening + "\n  Read the relay.\n  </rota-relay>\n❯ \n", "❯ \n", "picked-up"},
		{"earlier relay", "❯ <rota-relay n=\"8\" round=\"50\" from=\"orchestrator\">\n  </rota-relay>\n❯ \n", "❯ \n", ""},
		{"earlier round", "❯ <rota-relay n=\"9\" round=\"49\" from=\"orchestrator\">\n  </rota-relay>\n❯ \n", "❯ \n", ""},
		{"task brief", "❯ <rota-relay round=\"50\" from=\"orchestrator\">\n  </rota-relay>\n❯ \n", "❯ \n", ""},
		{"old echo", "❯ " + opening + "\n  </rota-relay>\n❯ \n", "❯ " + opening + "\n  </rota-relay>\n❯ \n", ""},
		{"queued", "• Queued follow-up inputs\n  ↳ " + opening + "\n❯ \n", "❯ \n", "queued"},
		{"next tool call", "• Messages to be submitted after next tool call\n    " + opening + "\n❯ \n", "❯ \n", "queued"},
		{"end of turn", "• Messages to be submitted at end of turn\n    " + opening + "\n❯ \n", "❯ \n", "queued"},
		{"earlier queue above current queue", "• Queued follow-up inputs\n  ↳ <rota-relay n=\"8\" round=\"50\" from=\"orchestrator\">\n• Messages to be submitted at end of turn\n    " + opening + "\n❯ \n", "❯ \n", "queued"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			terminal := coreTerminal{Reported: true, Status: &coreStatus{App: "claude-code", State: "done"}, Turn: 1}
			prior := coreDelivery{Sent: true, Was: "working", Before: tc.before}
			if got := deliveryEvidence(tc.screen, text, terminal, prior); got != tc.want {
				t.Fatalf("evidence = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCoreQueuedRelayWinsOverTurnEvidence(t *testing.T) {
	const opening = `<rota-relay n="9" round="50" from="orchestrator">`
	terminal := coreTerminal{Reported: true, Status: &coreStatus{App: "claude-code", State: "working"}, Turn: 1}
	prior := coreDelivery{Sent: true, Was: "working", Before: "❯ \n"}
	screen := "• Messages to be submitted at end of turn\n    " + opening + "\n❯ \n"
	if got := deliveryEvidence(screen, opening+"\n</rota-relay>", terminal, prior); got != "queued" {
		t.Fatalf("visible queued relay with an advanced turn = %q, want queued", got)
	}
}

func (f *coreFake) appName() string {
	if f.app == "" {
		return "codex"
	}
	return f.app
}
