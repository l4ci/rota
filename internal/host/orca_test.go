package host

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func orcaWorktree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	return dir
}
func TestOrcaSpawnQuotesEnvAndWaitsForBoot(t *testing.T) {
	cwd := orcaWorktree(t)
	reads := 0
	command := ""
	h := New(Kind("orca"), Deps{Getenv: func(string) string { return "" }, Sleep: func(time.Duration) {}, Run: func(_ context.Context, name string, args []string) (Result, error) {
		if name != "orca" {
			return Result{ExitCode: 1}, nil
		}
		switch strings.Join(args[:2], " ") {
		case "terminal create":
			for i, a := range args {
				if a == "--command" {
					command = args[i+1]
				}
			}
			return Result{Stdout: `{"terminal":{"id":"t1","title":"r:ben","cwd":"` + cwd + `"}}`}, nil
		case "terminal read":
			reads++
			text := "user@host $"
			if reads >= 3 {
				text = "Ask Codex to do anything\n› "
			}
			b, _ := json.Marshal(map[string]string{"screen": text})
			return Result{Stdout: string(b)}, nil
		}
		return Result{}, nil
	}})
	handle, err := h.Spawn(context.Background(), SpawnOpts{Session: "r", Slot: "ben", Cwd: cwd, Launch: "codex", Env: []string{"CODEX_HOME=/tmp/account with 'quote'", "ROTA_SLOT=ben"}, BootTimeout: 10})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if h.Kind() != Kind("orca") || handle == "" {
		t.Fatalf("kind=%q handle=%q; want Orca terminal", h.Kind(), handle)
	}
	if reads != 3 {
		t.Fatalf("readiness reads=%d, want 3", reads)
	}
	if !strings.Contains(command, "CODEX_HOME=") || !strings.Contains(command, "sh -c ") || !strings.Contains(command, "exec codex") {
		t.Fatalf("unsafe/missing shell launch: %q", command)
	}
}

func orcaTestHandle(t *testing.T) string {
	t.Helper()
	cwd := orcaWorktree(t)
	pid := filepath.Join(cwd, ".git", "agent.pid")
	if err := os.WriteFile(pid, []byte("123\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return encodeOrca(orcaHandle{"t1", "r:ben", cwd, pid})
}
func TestOrcaSendReceiptPreservesBrief(t *testing.T) {
	text := "line one\n\n`cmd` $HOME 'quotes' ö\n"
	file := filepath.Join(t.TempDir(), "brief")
	os.WriteFile(file, []byte(text), 0600)
	sent := ""
	h := New(Kind("orca"), Deps{Sleep: func(time.Duration) {}, Run: func(_ context.Context, _ string, a []string) (Result, error) {
		if a[1] == "read" {
			return Result{Stdout: `{"screen":"Ask Codex to do anything\n› "}`}, nil
		}
		for i, s := range a {
			if s == "--text" {
				sent = a[i+1]
			}
		}
		return Result{Stdout: `{"receipt":{"turn_started":true}}`}, nil
	}})
	if e := h.Send(context.Background(), "ben", orcaTestHandle(t), file); e != nil {
		t.Fatal(e)
	}
	if sent != text {
		t.Fatalf("sent %q, want exact %q", sent, text)
	}
}

func TestOrcaStaleHandleRequiresUniqueTitleAndCwd(t *testing.T) {
	for _, count := range []int{0, 1, 2} {
		t.Run(string(rune('0'+count)), func(t *testing.T) {
			handle := orcaTestHandle(t)
			meta, _ := decodeOrca(handle)
			h := New(Kind("orca"), Deps{Run: func(_ context.Context, _ string, a []string) (Result, error) {
				if a[1] == "list" {
					terms := []orcaTerminal{{ID: "other", Title: meta.Title, Cwd: "/other/project"}}
					for i := 0; i < count; i++ {
						terms = append(terms, orcaTerminal{ID: "fresh", Title: meta.Title, Cwd: meta.Cwd})
					}
					b, _ := json.Marshal(map[string]any{"terminals": terms})
					return Result{Stdout: string(b)}, nil
				}
				if a[1] == "read" && a[3] == "fresh" {
					return Result{Stdout: `{"screen":"first\nsecond\nthird"}`}, nil
				}
				return Result{ExitCode: 1}, nil
			}})
			got, e := h.Capabilities().PaneReader.ReadPane(context.Background(), "ben", handle, 2)
			if count == 1 {
				if e != nil || got != "second\nthird" {
					t.Fatalf("got %q, %v", got, e)
				}
			} else if e == nil || errors.Is(e, ErrPaneGone) {
				t.Fatalf("ambiguous/missing terminal must be unreadable, got %q %v", got, e)
			}
		})
	}
}

func TestOrcaKillPersistsTreeAcrossInstances(t *testing.T) {
	handle := orcaTestHandle(t)
	alive := map[int]bool{123: true, 456: true}
	closed := false
	deps := Deps{Getenv: func(string) string { return "" }, Sleep: func(time.Duration) {}, KillWait: 1, Alive: func(p int) bool { return alive[p] }, Tree: func(p int) []int {
		if alive[p] {
			return []int{p, 456}
		}
		return []int{p}
	}, Run: func(_ context.Context, _ string, a []string) (Result, error) {
		if a[1] == "list" {
			if closed {
				return Result{Stdout: `{"terminals":[]}`}, nil
			}
			meta, _ := decodeOrca(handle)
			b, _ := json.Marshal(map[string]any{"terminals": []orcaTerminal{{ID: "t1", Title: meta.Title, Cwd: meta.Cwd}}})
			return Result{Stdout: string(b)}, nil
		}
		if a[1] == "close" {
			closed = true
			alive[123] = false
			return Result{Stdout: `{}`}, nil
		}
		return Result{ExitCode: 1}, nil
	}}
	if e := New(Kind("orca"), deps).Kill(context.Background(), "ben", handle); e == nil || !strings.Contains(e.Error(), "456") {
		t.Fatalf("must report surviving child: %v", e)
	}
	if e := New(Kind("orca"), deps).Kill(context.Background(), "ben", handle); e == nil {
		t.Fatal("retry lost orphan proof")
	}
	alive[456] = false
	if e := New(Kind("orca"), deps).Kill(context.Background(), "ben", handle); e != nil {
		t.Fatalf("idempotent close with full proof: %v", e)
	}
}

func TestOrcaOptionalViewsPreserveHandleAfterRestart(t *testing.T) {
	cwd := orcaWorktree(t)
	exited := false
	deps := Deps{Getenv: func(string) string { return "" }, Sleep: func(time.Duration) {}, Alive: func(int) bool { return false }, Run: func(_ context.Context, _ string, a []string) (Result, error) {
		switch a[1] {
		case "create", "current":
			b, _ := json.Marshal(map[string]any{"terminal": orcaTerminal{ID: "t1", Title: "r:ben", Cwd: cwd}})
			return Result{Stdout: string(b)}, nil
		case "read":
			return Result{Stdout: `{"screen":"Ask Codex to do anything\n› "}`}, nil
		case "list":
			b, _ := json.Marshal(map[string]any{"terminals": []orcaTerminal{{ID: "t2", Title: "r:ben", Cwd: cwd, Exited: exited}, {ID: "unknown", Title: "shell", Cwd: cwd}}})
			return Result{Stdout: string(b)}, nil
		}
		return Result{}, nil
	}}
	h := New(Kind("orca"), deps)
	handle, e := h.Spawn(context.Background(), SpawnOpts{Slot: "ben", Session: "r", Cwd: cwd, Launch: "codex", BootTimeout: 2})
	if e != nil {
		t.Fatal(e)
	}
	h = New(Kind("orca"), deps)
	c := h.Capabilities()
	if c.TabLister == nil || c.TabOpener == nil || c.PaneHost == nil || c.Snapshotter == nil {
		t.Fatalf("missing capabilities: %+v", c)
	}
	agents, e := c.Snapshotter.Snapshot(context.Background())
	if e != nil || len(agents) != 2 || agents[0].Handle() != handle {
		t.Fatalf("snapshot=%+v %v; want original handle %q", agents, e, handle)
	}
	tabs, e := c.TabLister.Tabs(context.Background())
	if e != nil || len(tabs) != 2 || tabs[0].Agentless || tabs[1].Agentless {
		t.Fatalf("unknown process proof became agentless: %+v %v", tabs, e)
	}
	meta, _ := decodeOrca(handle)
	os.WriteFile(meta.PIDFile, []byte("123"), 0600)
	os.WriteFile(meta.PIDFile+".tree", []byte("[123]"), 0600)
	exited = true
	tabs, e = c.TabLister.Tabs(context.Background())
	if e != nil || !tabs[0].Agentless || tabs[1].Agentless {
		t.Fatalf("explicit exit plus process proof: %+v %v", tabs, e)
	}
	if !h.InSession() || h.Where() != "r:ben" {
		t.Fatal("current terminal missing")
	}
}

func TestOrcaSendReacquiresStaleNativeID(t *testing.T) {
	handle := orcaTestHandle(t)
	meta, _ := decodeOrca(handle)
	file := filepath.Join(t.TempDir(), "brief")
	os.WriteFile(file, []byte("do work"), 0600)
	h := New(Orca, Deps{Sleep: func(time.Duration) {}, Run: func(_ context.Context, _ string, a []string) (Result, error) {
		switch a[1] {
		case "list":
			b, _ := json.Marshal(map[string]any{"terminals": []orcaTerminal{{ID: "fresh", Title: meta.Title, Cwd: meta.Cwd}}})
			return Result{Stdout: string(b)}, nil
		case "read":
			if a[3] == "fresh" {
				return Result{Stdout: `{"screen":"Ask Codex to do anything\n› "}`}, nil
			}
		case "send":
			if a[3] == "fresh" {
				return Result{Stdout: `{"receipt":{"turn_started":true}}`}, nil
			}
		}
		return Result{ExitCode: 1}, nil
	}})
	if e := h.Send(context.Background(), "ben", handle, file); e != nil {
		t.Fatalf("stale send: %v", e)
	}
}

func TestOrcaEmptyKillAndRelayAfterBannerScrolls(t *testing.T) {
	h := New(Orca, Deps{Run: func(context.Context, string, []string) (Result, error) {
		t.Fatal("empty kill called host")
		return Result{}, nil
	}})
	if e := h.Kill(context.Background(), "ben", ""); e != nil {
		t.Fatalf("empty kill: %v", e)
	}
	handle := orcaTestHandle(t)
	meta, _ := decodeOrca(handle)
	os.WriteFile(meta.PIDFile+".ready", []byte("ready"), 0600)
	file := filepath.Join(t.TempDir(), "brief")
	os.WriteFile(file, []byte("relay"), 0600)
	h = New(Orca, Deps{Alive: func(int) bool { return true }, Sleep: func(time.Duration) {}, Run: func(_ context.Context, _ string, a []string) (Result, error) {
		if a[1] == "read" {
			return Result{Stdout: `{"screen":"Finished previous task.\n› "}`}, nil
		}
		return Result{Stdout: `{"receipt":{"turn_started":true}}`}, nil
	}})
	if e := h.Send(context.Background(), "ben", handle, file); e != nil {
		t.Fatalf("relay: %v", e)
	}
}

func TestOrcaGuardsAndFallback(t *testing.T) {
	for _, tc := range []struct {
		name, before, after, reply string
		want                       error
		sent                       int
	}{
		{"human draft", "Ask Codex to do anything\n› my unfinished work", "", "{}", ErrDraftOnPrompt, 0},
		{"dialog", "Trust this folder?\n❯ 1. Yes\n2. No\nEnter to confirm", "", "{}", ErrDialogOpen, 0},
		{"draft only", "Ask Codex to do anything\n› ", "Ask Codex to do anything\n› do work", "{}", ErrNotSubmitted, 1},
		{"submitted echo", "Ask Codex to do anything\n› ", "do work\nresult follows\n› ", "{}", nil, 1},
		{"busy evidence", "Ask Codex to do anything\n› ", "Working… esc to interrupt\n› ", "{}", nil, 1},
		{"malformed receipt", "Ask Codex to do anything\n› ", "", `not-json`, errors.New("invalid"), 1},
		{"unchanged", "Ask Codex to do anything\n› ", "Ask Codex to do anything\n› ", "{}", ErrNotSubmitted, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "brief")
			os.WriteFile(file, []byte("do work"), 0600)
			sent := 0
			h := New(Orca, Deps{Sleep: func(time.Duration) {}, Run: func(_ context.Context, _ string, a []string) (Result, error) {
				if a[1] == "send" {
					sent++
					return Result{Stdout: tc.reply}, nil
				}
				screen := tc.before
				if sent > 0 {
					screen = tc.after
				}
				b, _ := json.Marshal(map[string]string{"screen": screen})
				return Result{Stdout: string(b)}, nil
			}})
			e := h.Send(context.Background(), "ben", orcaTestHandle(t), file)
			if tc.want == nil {
				if e != nil {
					t.Fatal(e)
				}
			} else if tc.name == "malformed receipt" {
				if e == nil {
					t.Fatal("malformed JSON proved pickup")
				}
			} else if !errors.Is(e, tc.want) {
				t.Fatalf("got %v want %v", e, tc.want)
			}
			if sent != tc.sent {
				t.Fatalf("sent %d times, want %d", sent, tc.sent)
			}
		})
	}
}
func TestOrcaFailClosedMalformedAndMissingProof(t *testing.T) {
	for _, reply := range []string{"bad", `{}`, `{"screen":42}`, `{"screen":null}`} {
		t.Run(reply, func(t *testing.T) {
			h := New(Orca, Deps{Run: func(context.Context, string, []string) (Result, error) { return Result{Stdout: reply}, nil }})
			_, e := h.Capabilities().PaneReader.ReadPane(context.Background(), "ben", orcaTestHandle(t), 2)
			if e == nil || errors.Is(e, ErrPaneGone) {
				t.Fatalf("malformed read inferred gone: %v", e)
			}
		})
	}
	handle := orcaTestHandle(t)
	meta, _ := decodeOrca(handle)
	os.Remove(meta.PIDFile)
	h := New(Orca, Deps{Run: func(context.Context, string, []string) (Result, error) {
		t.Fatal("missing PID proof touched terminal")
		return Result{}, nil
	}})
	if e := h.Kill(context.Background(), "ben", handle); e == nil {
		t.Fatal("missing PID proof allowed kill")
	}
}
func TestOrcaRequireAndCurrentCache(t *testing.T) {
	calls := 0
	h := New(Orca, Deps{LookPath: func(string) (string, error) { return "/fake/orca", nil }, Run: func(_ context.Context, _ string, a []string) (Result, error) {
		if a[0] == "status" {
			return Result{Stdout: `{"running":true}`}, nil
		}
		calls++
		return Result{Stdout: `{"terminal":{"id":"t1","title":"here"}}`}, nil
	}})
	if e := h.Require(); e != nil {
		t.Fatal(e)
	}
	if !h.InSession() || h.Where() != "here" || calls != 1 {
		t.Fatalf("current calls %d", calls)
	}
	for _, raw := range []string{`{}`, `{"running":false}`, `{"running":"true"}`, `broken`} {
		h = New(Orca, Deps{LookPath: func(string) (string, error) { return "/fake/orca", nil }, Run: func(context.Context, string, []string) (Result, error) { return Result{Stdout: raw}, nil }})
		if e := h.Require(); e == nil {
			t.Fatalf("unsupported status passed: %s", raw)
		}
	}
}

func TestOrcaCurrentPaneNeverUsesTmuxMarker(t *testing.T) {
	if got := CurrentPane(Orca, func(string) string { return "%other-pane" }); got != "" {
		t.Fatalf("Orca adopted tmux pane %q", got)
	}
}
func TestOrcaCreateUncertaintyRetainsCleanupHandle(t *testing.T) {
	for _, reply := range []Result{{Stdout: `{"unexpected":"terminal metadata"}`}, {ExitCode: 1, Stderr: "connection dropped after create"}} {
		t.Run(reply.Stdout+reply.Stderr, func(t *testing.T) {
			h := New(Orca, Deps{Run: func(context.Context, string, []string) (Result, error) { return reply, nil }})
			handle, e := h.Spawn(context.Background(), SpawnOpts{Slot: "ben", Session: "r", Cwd: orcaWorktree(t), Launch: "codex", BootTimeout: 2})
			if e == nil || handle == "" {
				t.Fatalf("uncertain create lost cleanup identity: handle=%q err=%v", handle, e)
			}
			meta, e := decodeOrca(handle)
			if e != nil {
				t.Fatalf("cleanup handle invalid: %v", e)
			}
			if _, e = os.Stat(meta.PIDFile + ".json"); e != nil {
				t.Fatalf("cleanup metadata missing: %v", e)
			}
		})
	}
}
func TestOrcaStartupRequiresInputNotBanner(t *testing.T) {
	for _, screen := range []string{"user@host $", "Claude Code v2.1.309\nLoading...", "Welcome to Claude Code\nTrust folder?\n❯ 1. Yes\nEnter to confirm"} {
		h := New(Orca, Deps{Sleep: func(time.Duration) {}, Run: func(_ context.Context, _ string, a []string) (Result, error) {
			if a[1] == "create" {
				return Result{Stdout: `{"terminal":{"id":"t1"}}`}, nil
			}
			if a[1] == "send" {
				t.Fatal("brief reached unready terminal")
			}
			b, _ := json.Marshal(map[string]string{"screen": screen})
			return Result{Stdout: string(b)}, nil
		}})
		handle, e := h.Spawn(context.Background(), SpawnOpts{Slot: "ben", Session: "r", Cwd: orcaWorktree(t), Launch: "claude", BootTimeout: 4})
		if e == nil || handle == "" {
			t.Fatalf("unready screen %q: handle %q err %v", screen, handle, e)
		}
	}
}
func TestOrcaRejectsUnapprovedEnvWithoutLeakingValue(t *testing.T) {
	h := New(Orca, Deps{Run: func(context.Context, string, []string) (Result, error) {
		t.Fatal("unapproved env reached Orca")
		return Result{}, nil
	}})
	if _, e := h.Spawn(context.Background(), SpawnOpts{Cwd: orcaWorktree(t), Env: []string{"API_KEY=private-value"}}); e == nil || strings.Contains(e.Error(), "private-value") {
		t.Fatalf("unsafe refusal: %v", e)
	}
}

func TestOrcaDialogAppearingDuringGuardRefusesSend(t *testing.T) {
	file := filepath.Join(t.TempDir(), "brief")
	os.WriteFile(file, []byte("do work"), 0600)
	reads := 0
	sent := false
	h := New(Orca, Deps{Sleep: func(time.Duration) {}, Run: func(_ context.Context, _ string, a []string) (Result, error) {
		if a[1] == "send" {
			sent = true
			return Result{Stdout: `{"receipt":{"turn_started":true}}`}, nil
		}
		reads++
		screen := "Ask Codex to do anything\n› "
		if reads > 1 {
			screen = "Trust folder?\n❯ 1. Yes\nEnter to confirm"
		}
		b, _ := json.Marshal(map[string]string{"screen": screen})
		return Result{Stdout: string(b)}, nil
	}})
	if e := h.Send(context.Background(), "ben", orcaTestHandle(t), file); !errors.Is(e, ErrDialogOpen) || sent {
		t.Fatalf("dialog guard race: sent=%v err=%v", sent, e)
	}
}

func TestOrcaDeadRootWithoutTreeCannotProveGone(t *testing.T) {
	h := New(Orca, Deps{Alive: func(int) bool { return false }, Tree: func(int) []int { t.Fatal("dead root tree cannot find orphans"); return nil }, Run: func(context.Context, string, []string) (Result, error) {
		t.Fatal("missing process tree touched host")
		return Result{}, nil
	}})
	if e := h.Kill(context.Background(), "ben", orcaTestHandle(t)); e == nil {
		t.Fatal("dead root without tree proved gone")
	}
}
func TestOrcaCloseFailureNeverProvesGone(t *testing.T) {
	handle := orcaTestHandle(t)
	meta, _ := decodeOrca(handle)
	h := New(Orca, Deps{Alive: func(int) bool { return true }, Tree: func(p int) []int { return []int{p} }, Run: func(_ context.Context, _ string, a []string) (Result, error) {
		if a[1] == "list" {
			b, _ := json.Marshal(map[string]any{"terminals": []orcaTerminal{{ID: "t1", Title: meta.Title, Cwd: meta.Cwd}}})
			return Result{Stdout: string(b)}, nil
		}
		return Result{ExitCode: 1, Stderr: "close failed"}, nil
	}})
	if e := h.Kill(context.Background(), "ben", handle); e == nil {
		t.Fatal("failed close proved gone")
	}
}
