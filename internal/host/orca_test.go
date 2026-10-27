package host

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const orcaEmptyReady = "OpenAI Codex\nmodel: gpt-5.3-codex high\n› "

func orcaWorktree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if e := os.Mkdir(filepath.Join(dir, ".git"), 0700); e != nil {
		t.Fatal(e)
	}
	return dir
}

// This fake models the CLI boundary, with real files for persistent evidence.
// Neither Alive nor Tree consults or kills processes on the test machine.
type orcaFake struct {
	cwd, title, branch, gitBranch         string
	screens                               []string
	after, readReply, receipt, status     string
	list                                  *[]orcaTerminal
	reads, sends, calls                   int
	sent, command                         string
	readFails, closed, closeFails, exited bool
	alive                                 map[int]bool
}

func newOrcaFake(t *testing.T) *orcaFake {
	return &orcaFake{cwd: orcaWorktree(t), title: "r:ben", branch: "ben/2203-orca", gitBranch: "ben/2203-orca", screens: []string{orcaEmptyReady}, receipt: `{"stages":["input_accepted","turn_started"],"provider":"codex","observation":"supported"}`, status: `{"running":true}`, alive: map[int]bool{123: true}}
}
func (f *orcaFake) deps() Deps {
	return Deps{Getenv: func(string) string { return "" }, Sleep: func(time.Duration) {}, KillWait: 1, Alive: func(p int) bool { return f.alive[p] }, Tree: func(p int) []int {
		if f.alive[p] {
			return []int{p, 456}
		}
		return []int{p}
	}, LookPath: func(string) (string, error) { return "/fake/orca", nil }, Run: f.run}
}
func (f *orcaFake) run(_ context.Context, name string, a []string) (Result, error) {
	f.calls++
	if name == "git" {
		return Result{Stdout: f.gitBranch + "\n"}, nil
	}
	if name != "orca" {
		return Result{ExitCode: 1}, nil
	}
	if a[0] == "status" {
		return Result{Stdout: f.status}, nil
	}
	switch a[1] {
	case "create":
		for i, v := range a {
			if v == "--command" {
				f.command = a[i+1]
			}
		}
		return Result{Stdout: `{"handle":"t1","title":"r:ben"}`}, nil
	case "read":
		f.reads++
		if f.readFails {
			return Result{ExitCode: 1}, nil
		}
		if f.readReply != "" {
			return Result{Stdout: f.readReply}, nil
		}
		screen := f.screens[min(f.reads-1, len(f.screens)-1)]
		if f.sends > 0 && f.after != "" {
			screen = f.after
		}
		b, _ := json.Marshal(map[string]string{"screen": screen})
		return Result{Stdout: string(b)}, nil
	case "send":
		f.sends++
		for i, v := range a {
			if v == "--text" {
				f.sent = a[i+1]
			}
		}
		return Result{Stdout: f.receipt}, nil
	case "list":
		ts := []orcaTerminal{{ID: "t1", Title: f.title, Cwd: f.cwd, Branch: f.branch, Exited: f.exited}}
		if f.closed {
			ts = []orcaTerminal{}
		}
		if f.list != nil {
			ts = *f.list
		}
		b, _ := json.Marshal(map[string]any{"terminals": ts})
		return Result{Stdout: string(b)}, nil
	case "close":
		if f.closeFails {
			return Result{ExitCode: 1, Stderr: "close failed"}, nil
		}
		f.closed = true
		f.alive[123] = false
		return Result{Stdout: `{}`}, nil
	}
	return Result{ExitCode: 1, Stderr: "unsupported fixture operation"}, nil
}
func (f *orcaFake) handle(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(f.cwd, ".git", "rota-orca")
	if e := os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	pid := filepath.Join(dir, "agent-test.pid")
	if e := os.WriteFile(pid, []byte("123\n"), 0600); e != nil {
		t.Fatal(e)
	}
	h := encodeOrca(orcaHandle{ID: "t1", Title: "r:ben", Cwd: f.cwd, PIDFile: pid, Branch: "ben/2203-orca"})
	if e := os.WriteFile(pid+".json", []byte(h), 0600); e != nil {
		t.Fatal(e)
	}
	return h
}
func orcaBrief(t *testing.T, text string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "brief")
	if e := os.WriteFile(p, []byte(text), 0600); e != nil {
		t.Fatal(e)
	}
	return p
}
func TestOrcaSpawnQuotesEnvAndWaitsForBoot(t *testing.T) {
	f := newOrcaFake(t)
	f.screens = []string{"user@host $", "model: loading\n› Ask Codex to do anything", orcaEmptyReady}
	h := New(Orca, f.deps())
	handle, e := h.Spawn(context.Background(), SpawnOpts{Session: "r", Slot: "ben", Cwd: f.cwd, Launch: "codex", Env: []string{"CODEX_HOME=/tmp/account with 'quote'", "ROTA_SLOT=ben"}, BootTimeout: 10})
	if e != nil {
		t.Fatal(e)
	}
	if h.Kind() != Orca || handle == "" || f.reads != 3 {
		t.Fatalf("kind=%q handle=%q reads=%d", h.Kind(), handle, f.reads)
	}
	if !strings.Contains(f.command, "CODEX_HOME=") || !strings.Contains(f.command, "sh -c ") || !strings.Contains(f.command, "exec codex") {
		t.Fatalf("unsafe launch: %s", f.command)
	}
}
func TestOrcaSendReceiptPreservesBrief(t *testing.T) {
	f := newOrcaFake(t)
	text := "line one\n\n`cmd` $HOME 'quotes' ö\n"
	if e := New(Orca, f.deps()).Send(context.Background(), "ben", f.handle(t), orcaBrief(t, text)); e != nil {
		t.Fatal(e)
	}
	if f.sent != text || f.sends != 1 {
		t.Fatalf("sent %q %d times", f.sent, f.sends)
	}
}
func TestOrcaStaleNativeHandleIsNeverReplacedByTitle(t *testing.T) {
	f := newOrcaFake(t)
	h := f.handle(t)
	f.readFails = true
	ts := []orcaTerminal{{ID: "different", Title: "r:ben", Cwd: f.cwd, Branch: f.branch}}
	f.list = &ts
	host := New(Orca, f.deps())
	if _, e := host.Capabilities().PaneReader.ReadPane(context.Background(), "ben", h, 2); e == nil || errors.Is(e, ErrPaneGone) {
		t.Fatalf("stale handle inferred gone: %v", e)
	}
	if e := host.Send(context.Background(), "ben", h, orcaBrief(t, "work")); e == nil || f.sends != 0 {
		t.Fatalf("stale send: %v sends=%d", e, f.sends)
	}
	if e := host.Kill(context.Background(), "ben", h); e == nil || f.closed {
		t.Fatalf("replacement was closed: %v closed=%v", e, f.closed)
	}
}
func TestOrcaKillPersistsTreeAcrossInstances(t *testing.T) {
	f := newOrcaFake(t)
	f.alive[456] = true
	h := f.handle(t)
	if e := New(Orca, f.deps()).Kill(context.Background(), "ben", h); e == nil || !strings.Contains(e.Error(), "456") {
		t.Fatalf("surviving child: %v", e)
	}
	if e := New(Orca, f.deps()).Kill(context.Background(), "ben", h); e == nil {
		t.Fatal("retry lost orphan proof")
	}
	f.alive[456] = false
	if e := New(Orca, f.deps()).Kill(context.Background(), "ben", h); e != nil {
		t.Fatalf("idempotent close: %v", e)
	}
}
func TestOrcaOptionalViewsPreserveHandleAfterRestart(t *testing.T) {
	f := newOrcaFake(t)
	h := f.handle(t)
	f.title = "title changed by shell"
	host := New(Orca, f.deps())
	c := host.Capabilities()
	if c.TabLister == nil || c.TabOpener == nil || c.PaneHost == nil || c.Snapshotter == nil {
		t.Fatalf("missing capabilities: %+v", c)
	}
	agents, e := c.Snapshotter.Snapshot(context.Background())
	if e != nil || len(agents) != 1 || agents[0].Handle() != h {
		t.Fatalf("snapshot=%+v %v", agents, e)
	}
	tabs, e := c.TabLister.Tabs(context.Background())
	if e != nil || len(tabs) != 1 || tabs[0].Agentless {
		t.Fatalf("unknown proof became agentless: %+v %v", tabs, e)
	}
	meta, _ := decodeOrca(h)
	os.WriteFile(meta.PIDFile+".tree", []byte("[123]"), 0600)
	f.exited = true
	f.alive[123] = false
	tabs, e = c.TabLister.Tabs(context.Background())
	if e != nil || !tabs[0].Agentless {
		t.Fatalf("explicit exit plus tree: %+v %v", tabs, e)
	}
}
func TestOrcaEmptyKillAndRelayAfterBannerScrolls(t *testing.T) {
	f := newOrcaFake(t)
	h := f.handle(t)
	host := New(Orca, f.deps())
	if e := host.Kill(context.Background(), "ben", ""); e != nil || f.calls != 0 {
		t.Fatalf("empty kill: %v calls=%d", e, f.calls)
	}
	meta, _ := decodeOrca(h)
	os.WriteFile(meta.PIDFile+".ready", []byte("ready"), 0600)
	f.screens = []string{"Finished previous task.\n› "}
	if e := host.Send(context.Background(), "ben", h, orcaBrief(t, "relay")); e != nil {
		t.Fatal(e)
	}
}
func TestOrcaGuardsAndFallback(t *testing.T) {
	for _, tc := range []struct {
		name, before, after, reply string
		want                       error
		sent                       int
	}{
		{"human draft", orcaEmptyReady + " human draft", "", "{}", ErrDraftOnPrompt, 0},
		{"dialog", "Do you trust the contents of this directory?\n› 1. Yes, continue", "", "{}", ErrDialogOpen, 0},
		{"draft only", orcaEmptyReady, "model: gpt-5.3-codex\n› do work", "{}", ErrNotSubmitted, 1},
		{"submitted echo", orcaEmptyReady, "do work\nresult follows\n› ", "{}", ErrNotSubmitted, 1},
		{"busy evidence", orcaEmptyReady, "Working… esc to interrupt\n› ", "{}", nil, 1},
		{"malformed receipt", orcaEmptyReady, "", `not-json`, fmt.Errorf("malformed"), 1},
		{"unchanged", orcaEmptyReady, orcaEmptyReady, "{}", ErrNotSubmitted, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newOrcaFake(t)
			f.screens = []string{tc.before}
			f.after = tc.after
			f.receipt = tc.reply
			e := New(Orca, f.deps()).Send(context.Background(), "ben", f.handle(t), orcaBrief(t, "do work"))
			if tc.want == nil {
				if e != nil {
					t.Fatal(e)
				}
			} else if tc.name == "malformed receipt" {
				if e == nil {
					t.Fatal("malformed reply proved pickup")
				}
			} else if !errors.Is(e, tc.want) {
				t.Fatalf("got %v want %v", e, tc.want)
			}
			if f.sends != tc.sent {
				t.Fatalf("sent %d times want %d", f.sends, tc.sent)
			}
		})
	}
}
func TestOrcaFailClosedMalformedAndMissingProof(t *testing.T) {
	for _, reply := range []string{"bad", `{}`, `{"screen":42}`, `{"screen":null}`} {
		t.Run(reply, func(t *testing.T) {
			f := newOrcaFake(t)
			f.readReply = reply
			_, e := New(Orca, f.deps()).Capabilities().PaneReader.ReadPane(context.Background(), "ben", f.handle(t), 2)
			if e == nil || errors.Is(e, ErrPaneGone) {
				t.Fatalf("malformed read: %v", e)
			}
		})
	}
	f := newOrcaFake(t)
	h := f.handle(t)
	meta, _ := decodeOrca(h)
	os.Remove(meta.PIDFile)
	if e := New(Orca, f.deps()).Kill(context.Background(), "ben", h); e == nil || f.calls != 0 {
		t.Fatalf("missing PID: %v calls=%d", e, f.calls)
	}
}
func TestOrcaRequireAndMarker(t *testing.T) {
	f := newOrcaFake(t)
	d := f.deps()
	d.Getenv = func(k string) string {
		if k == "ORCA_TERMINAL_HANDLE" {
			return "native-here"
		}
		return ""
	}
	h := New(Orca, d)
	if e := h.Require(); e != nil {
		t.Fatal(e)
	}
	calls := f.calls
	if !h.InSession() || h.Where() != "native-here" || f.calls != calls {
		t.Fatal("marker probed external current")
	}
	for _, raw := range []string{`{}`, `{"running":false}`, `{"running":"true"}`, `broken`} {
		f.status = raw
		if e := New(Orca, f.deps()).Require(); e == nil {
			t.Fatalf("unsupported status passed: %s", raw)
		}
	}
}
func TestOrcaCreateUncertaintyRetainsCleanupHandle(t *testing.T) {
	for _, reply := range []Result{{Stdout: `{"unexpected":"terminal metadata"}`}, {ExitCode: 1, Stderr: "connection dropped after create"}} {
		t.Run(reply.Stdout+reply.Stderr, func(t *testing.T) {
			f := newOrcaFake(t)
			d := f.deps()
			d.Run = func(ctx context.Context, name string, a []string) (Result, error) {
				if name == "git" {
					return f.run(ctx, name, a)
				}
				return reply, nil
			}
			h, e := New(Orca, d).Spawn(context.Background(), SpawnOpts{Slot: "ben", Session: "r", Cwd: f.cwd, Launch: "codex", BootTimeout: 2})
			if e == nil || h == "" {
				t.Fatalf("uncertain create lost identity: %q %v", h, e)
			}
			meta, e := decodeOrca(h)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = os.Stat(meta.PIDFile + ".json"); e != nil {
				t.Fatal(e)
			}
		})
	}
}
func TestOrcaStartupRequiresInputNotBanner(t *testing.T) {
	for _, screen := range []string{"user@host $", "Claude Code v2.1.309\nLoading...", "model: loading\n› Ask Codex to do anything", "Do you trust the contents of this directory?\n› 1. Yes, continue"} {
		f := newOrcaFake(t)
		f.screens = []string{screen}
		h, e := New(Orca, f.deps()).Spawn(context.Background(), SpawnOpts{Slot: "ben", Session: "r", Cwd: f.cwd, Launch: "codex", BootTimeout: 4})
		if e == nil || h == "" || f.sends != 0 {
			t.Fatalf("unready screen %q: %q %v", screen, h, e)
		}
	}
}
func TestOrcaRejectsUnapprovedEnvWithoutLeakingValue(t *testing.T) {
	f := newOrcaFake(t)
	_, e := New(Orca, f.deps()).Spawn(context.Background(), SpawnOpts{Cwd: f.cwd, Env: []string{"API_KEY=private-value"}})
	if e == nil || strings.Contains(e.Error(), "private-value") || f.calls != 0 {
		t.Fatalf("unsafe refusal: %v calls=%d", e, f.calls)
	}
}
func TestOrcaDialogAppearingDuringGuardRefusesSend(t *testing.T) {
	f := newOrcaFake(t)
	f.screens = []string{orcaEmptyReady, "Do you trust the contents of this directory?\n› 1. Yes, continue"}
	e := New(Orca, f.deps()).Send(context.Background(), "ben", f.handle(t), orcaBrief(t, "do work"))
	if !errors.Is(e, ErrDialogOpen) || f.sends != 0 {
		t.Fatalf("late dialog: %v sends=%d", e, f.sends)
	}
}
func TestOrcaDeadRootWithoutTreeCannotProveGone(t *testing.T) {
	f := newOrcaFake(t)
	f.alive[123] = false
	e := New(Orca, f.deps()).Kill(context.Background(), "ben", f.handle(t))
	if e == nil || f.calls != 0 {
		t.Fatalf("dead root lost orphan proof: %v calls=%d", e, f.calls)
	}
}
func TestOrcaCloseFailureNeverProvesGone(t *testing.T) {
	f := newOrcaFake(t)
	f.closeFails = true
	if e := New(Orca, f.deps()).Kill(context.Background(), "ben", f.handle(t)); e == nil {
		t.Fatal("failed close proved gone")
	}
}
