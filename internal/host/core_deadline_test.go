package host

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

// deadlineConn records SetDeadline calls on a real pipe-backed conn.
type deadlineConn struct {
	net.Conn
	deadlines []time.Time
}

func (c *deadlineConn) SetDeadline(t time.Time) error {
	c.deadlines = append(c.deadlines, t)
	return c.Conn.SetDeadline(t)
}

func TestCoreCallRefreshesDeadlinePerRPC(t *testing.T) {
	f := &coreFake{screen: "› \n", status: "idle", pid: 42, core: "c", state: "live", cols: 100, rows: 30}
	a, b := net.Pipe()
	go f.serve(b)
	dc := &deadlineConn{Conn: a}
	defer dc.Close()
	c := newCoreConn(dc, time.Now().Add(5*time.Minute))
	var v map[string]any
	for i := 0; i < 3; i++ {
		if e := c.call("terminal.get", map[string]any{"terminal": "t2"}, &v); e != nil {
			t.Fatal(e)
		}
	}
	if len(dc.deadlines) != 3 {
		t.Fatalf("deadline set %d times, want once per call", len(dc.deadlines))
	}
	for _, d := range dc.deadlines {
		if rem := time.Until(d); rem > coreRPCTimeout || rem < coreRPCTimeout-5*time.Second {
			t.Errorf("deadline %v away, want about %v", rem, coreRPCTimeout)
		}
	}
	// A nearer ctx deadline still caps the per-call one.
	near := time.Now().Add(2 * time.Second)
	c = newCoreConn(dc, near)
	dc.deadlines = nil
	if e := c.call("terminal.get", map[string]any{"terminal": "t2"}, &v); e != nil {
		t.Fatal(e)
	}
	if !dc.deadlines[0].Equal(near) {
		t.Errorf("deadline %v, want ctx deadline %v", dc.deadlines[0], near)
	}
}

func claudeTrustScreen(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("testdata/trust-dialog-2.1.288.txt")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestCoreSpawnBootTimeoutNamesTrustDialog(t *testing.T) {
	for _, tc := range []struct {
		name, app, launch, screen string
		want                      []string
		absent                    string
	}{
		{"Claude", "claude-code", "claude", claudeTrustScreen(t),
			[]string{"Accessing workspace:", "worker terminal", "Yes, I trust this folder", `defaults to "No, exit"`, "Down, then Enter", "hc keys t2 down", "hc keys t2 enter", "once per slot worktree", "same request id"}, "open `claude`"},
		{"Codex", "codex", "codex", " Folder access\n /project\n\n › 1. Trust and continue\n   2. Exit\n\n Press Enter to continue\n",
			[]string{"Folder access", "Trust and continue", "repository root", "once per project", "same request id"}, "claude"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, f := coreFixture(t)
			f.app = tc.app
			f.unreported = true
			f.screen = tc.screen
			_, e := h.Spawn(context.Background(), SpawnOpts{Slot: "dana", Cwd: "/work", Launch: tc.launch, BootTimeout: 2})
			if e == nil {
				t.Fatal("spawn succeeded on an unanswered trust dialog")
			}
			for _, want := range tc.want {
				if !strings.Contains(e.Error(), want) {
					t.Errorf("error %q lacks %q", e, want)
				}
			}
			if strings.Contains(e.Error(), tc.absent) || strings.Contains(e.Error(), "timeout") {
				t.Errorf("error has unrelated recovery or reads as a socket timeout: %v", e)
			}
			if f.sends != 0 {
				t.Fatal("startup dialog was answered automatically")
			}
		})
	}
}

func TestCoreSpawnBootTimeoutUnknownScreenShowsTail(t *testing.T) {
	h, f := coreFixture(t)
	f.unreported = true
	f.screen = "Loading something odd\n"
	_, e := h.Spawn(context.Background(), SpawnOpts{Slot: "dana", Cwd: "/work", Launch: "codex", BootTimeout: 1})
	if e == nil || !strings.Contains(e.Error(), "Loading something odd") {
		t.Fatalf("want screen tail in error, got %v", e)
	}
}

func TestCoreCallSurfacesSocketError(t *testing.T) {
	a, b := net.Pipe()
	b.Close()
	c := newCoreConn(a, time.Time{})
	var v map[string]any
	e := c.call("terminal.get", map[string]any{}, &v)
	if e == nil || errors.Is(e, ErrDialogOpen) || strings.Contains(e.Error(), "dialog") {
		t.Fatalf("want a raw socket error, got %v", e)
	}
}
