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
)

// The fake speaks actual named Core RPC, with no executable dependency.
func coreFixture(t *testing.T) (Host, *coreFake) {
	t.Helper()
	f := &coreFake{screen: "› \n", status: "idle", pid: 42, core: "fixture-core", state: "live", events: make(chan map[string]any, 8), labels: map[string]string{"herdr.space": "s1", "herdr.tab": "parent-tab"}}
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
	pid                int
	labels             map[string]string
	sends              int
	run                map[string]any
	lose               bool
	core, state, after string
	styled             string
	observes           int
	blocked            bool
	kills              int
	missing            bool
	loseRun            bool
	runs               int
	events             chan map[string]any
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
			return map[string]any{"address": addr, "created_ms": 123, "cwd": "/work", "argv": []string{"codex"}, "state": state, "cols": 100, "rows": 30, "screen": "exact", "pid": f.pid, "labels": f.labels, "reported": true, "status": map[string]string{"app": "codex", "state": f.status}}
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
			f.state = "finished"
			v = term("t2")
		case "terminal.list", "events.subscribe":
			v = map[string]any{"terminals": []any{term("t2")}, "seq": 1}
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
	f.styled = "\x18\x1b[H\x1b[2J› Ask Codex to do anything\x1b[1;3H"
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
