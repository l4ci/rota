package host

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const orcaRelayReady = "OpenAI Codex\nmodel: gpt-5.3-codex high\n› A changing placeholder"

func orcaRelayHandle(t *testing.T) string {
	t.Helper()
	cwd := orcaWorktree(t)
	dir := filepath.Join(cwd, ".git", "rota-orca")
	if e := os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	pid := filepath.Join(dir, "agent-live.pid")
	if e := os.WriteFile(pid, []byte("123\n"), 0600); e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(map[string]string{"ID": "native-1", "Title": "r:ben", "Cwd": cwd, "PIDFile": pid, "Branch": "ben/2203-orca"})
	h := "orca:" + base64.RawURLEncoding.EncodeToString(b)
	if e := os.WriteFile(pid+".json", []byte(h), 0600); e != nil {
		t.Fatal(e)
	}
	return h
}
func TestOrcaRelayReadiness(t *testing.T) {
	for _, tc := range []struct {
		name, screen string
		want         bool
	}{
		{"loaded changing placeholder", orcaRelayReady, true},
		{"loading with old placeholder", "model: loading\n› Ask Codex to do anything", false},
		{"placeholder alone", "Ask Codex to do anything\n› ", false},
		{"inline marker", "model: gpt-5.3-codex\noutput mentions › inline", false},
		{"trust", "model: gpt-5.3-codex\nDo you trust the contents of this directory?\n› 1. Yes, continue", false},
		{"Claude", "Claude Code v2.1.309\n❯ ", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := orcaReady(tc.screen); got != tc.want {
				t.Fatalf("ready=%v want=%v: %q", got, tc.want, tc.screen)
			}
		})
	}
}
func TestOrcaRelayMarker(t *testing.T) {
	getenv := func(k string) string {
		if k == "ORCA_TERMINAL_HANDLE" {
			return "native-here"
		}
		if k == "TMUX_PANE" {
			return "%unrelated"
		}
		return ""
	}
	h := New(Orca, Deps{Getenv: getenv, Run: func(context.Context, string, []string) (Result, error) {
		t.Fatal("ambient marker must avoid terminal current/show probes")
		return Result{}, nil
	}})
	if !h.InSession() || h.Where() != "native-here" || CurrentPane(Orca, getenv) != "native-here" {
		t.Fatal("Orca marker was not used consistently")
	}
}
func TestOrcaRelayCreateCapturesBranchAndHandle(t *testing.T) {
	cwd := orcaWorktree(t)
	gitBeforeCreate := false
	created := false
	h := New(Orca, Deps{Getenv: func(string) string { return "" }, Sleep: func(time.Duration) {}, Alive: func(int) bool { return true }, Tree: func(p int) []int { return []int{p} }, Run: func(_ context.Context, name string, a []string) (Result, error) {
		if name == "git" {
			if strings.Join(a, " ") != "-C "+cwd+" symbolic-ref --quiet --short HEAD" {
				t.Fatalf("unexpected branch probe: %v", a)
			}
			if !created {
				gitBeforeCreate = true
			}
			return Result{Stdout: "ben/2203-orca\n"}, nil
		}
		if a[1] == "create" {
			created = true
			files, _ := filepath.Glob(filepath.Join(cwd, ".git", "rota-orca", "agent-*.pid"))
			for _, file := range files {
				if err := os.WriteFile(file, []byte("123\n"), 0600); err != nil {
					return Result{}, err
				}
			}
			return Result{Stdout: `{"handle":"native-1","title":"r:ben","tabId":"tab-1"}`}, nil
		}
		if a[1] == "read" {
			b, _ := json.Marshal(map[string]string{"screen": orcaRelayReady})
			return Result{Stdout: string(b)}, nil
		}
		return Result{ExitCode: 1}, nil
	}})
	handle, e := h.Spawn(context.Background(), SpawnOpts{Session: "r", Slot: "ben", Cwd: cwd, Launch: "codex", BootTimeout: 2})
	if e != nil {
		t.Fatal(e)
	}
	b, e := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(handle, "orca:"))
	if e != nil {
		t.Fatal(e)
	}
	var meta map[string]string
	if e = json.Unmarshal(b, &meta); e != nil {
		t.Fatal(e)
	}
	if !gitBeforeCreate || meta["Branch"] != "ben/2203-orca" || meta["ID"] != "native-1" {
		t.Fatalf("branch/handle not preserved before create: %+v gitBefore=%v", meta, gitBeforeCreate)
	}
}
func TestOrcaRelayReceiptAndBranchGuard(t *testing.T) {
	for _, tc := range []struct {
		name, nativeBranch, gitBranch, receipt string
		want                                   string
	}{
		{"matching", "ben/2203-orca", "ben/2203-orca", `{"stages":["input_accepted","turn_started"],"provider":"codex","observation":"supported"}`, ""},
		{"native renamed", "renamed-by-orca", "ben/2203-orca", `{"stages":["turn_started"],"provider":"codex","observation":"supported"}`, "branch"},
		{"git renamed", "ben/2203-orca", "renamed-on-disk", `{"stages":["turn_started"],"provider":"codex","observation":"supported"}`, "branch"},
		{"native missing branch", "", "ben/2203-orca", `{"stages":["turn_started"],"provider":"codex","observation":"supported"}`, "branch"},
		{"fallback renamed", "renamed-by-orca", "ben/2203-orca", `{}`, "branch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handle := orcaRelayHandle(t)
			meta, _ := decodeOrca(handle)
			file := filepath.Join(t.TempDir(), "brief")
			os.WriteFile(file, []byte("do work"), 0600)
			sends := 0
			h := New(Orca, Deps{Sleep: func(time.Duration) {}, Run: func(_ context.Context, name string, a []string) (Result, error) {
				if name == "git" {
					return Result{Stdout: tc.gitBranch + "\n"}, nil
				}
				switch a[1] {
				case "read":
					screen := "model: gpt-5.3-codex\n› Ask Codex to do anything"
					if tc.name == "fallback renamed" && sends > 0 {
						screen = "do work\nWorking… esc to interrupt\nmodel: gpt-5.3-codex\n› "
					}
					b, _ := json.Marshal(map[string]string{"screen": screen})
					return Result{Stdout: string(b)}, nil
				case "send":
					sends++
					return Result{Stdout: tc.receipt}, nil
				case "list":
					b, _ := json.Marshal(map[string]any{"terminals": []map[string]any{{"handle": "native-1", "title": "changed by agent", "worktreePath": meta.Cwd, "branch": tc.nativeBranch}}})
					return Result{Stdout: string(b)}, nil
				}
				return Result{ExitCode: 1}, nil
			}})
			e := h.Send(context.Background(), "ben", handle, file)
			if tc.want == "" {
				if e != nil {
					t.Fatal(e)
				}
			} else if e == nil || !strings.Contains(strings.ToLower(e.Error()), tc.want) {
				t.Fatalf("missing branch refusal: %v", e)
			}
			if sends != 1 {
				t.Fatalf("sent %d times, want once", sends)
			}
		})
	}
}
func TestOrcaRelayIdentityUsesOnlyNativeHandle(t *testing.T) {
	handle := orcaRelayHandle(t)
	meta, _ := decodeOrca(handle)
	h := New(Orca, Deps{Run: func(_ context.Context, _ string, a []string) (Result, error) {
		if a[1] == "list" {
			b, _ := json.Marshal(map[string]any{"terminals": []map[string]string{{"handle": "native-1", "worktreePath": meta.Cwd, "title": "shell changed title", "branch": "ben/2203-orca"}}})
			return Result{Stdout: string(b)}, nil
		}
		return Result{ExitCode: 1}, nil
	}})
	agents, e := h.Capabilities().Snapshotter.Snapshot(context.Background())
	if e != nil || len(agents) != 1 || agents[0].Handle() != handle {
		t.Fatalf("changed title lost native identity: %+v %v", agents, e)
	}
	// A different terminal with the original title cannot be adopted or closed.
	closed := false
	h = New(Orca, Deps{Alive: func(int) bool { return true }, Tree: func(p int) []int { return []int{p} }, Run: func(_ context.Context, _ string, a []string) (Result, error) {
		if a[1] == "close" {
			closed = true
		}
		if a[1] == "list" {
			b, _ := json.Marshal(map[string]any{"terminals": []map[string]string{{"handle": "different", "worktreePath": meta.Cwd, "title": meta.Title, "branch": "ben/2203-orca"}}})
			return Result{Stdout: string(b)}, nil
		}
		return Result{ExitCode: 1}, nil
	}})
	if _, e := h.Capabilities().PaneReader.ReadPane(context.Background(), "ben", handle, 1); e == nil || errors.Is(e, ErrPaneGone) {
		t.Fatalf("missing native handle must be unverifiable: %v", e)
	}
	if e := h.Kill(context.Background(), "ben", handle); e == nil || closed {
		t.Fatalf("closed replacement terminal: closed=%v error=%v", closed, e)
	}
}
func TestOrcaRelayPortableBriefLimit(t *testing.T) {
	handle := orcaRelayHandle(t)
	file := filepath.Join(t.TempDir(), "brief")
	text := strings.Repeat("ö", 61441)
	os.WriteFile(file, []byte(text), 0600)
	calls := 0
	h := New(Orca, Deps{Run: func(context.Context, string, []string) (Result, error) {
		calls++
		return Result{Stdout: `{"screen":"Ask Codex to do anything\n› "}`}, nil
	}})
	e := h.Send(context.Background(), "ben", handle, file)
	if e == nil || !strings.Contains(e.Error(), "122882") || !strings.Contains(e.Error(), "122880") || calls != 0 {
		t.Fatalf("oversize send: calls=%d err=%v", calls, e)
	}
}

func TestOrcaRelayCurrentPaneAny(t *testing.T) {
	pane, kind := CurrentPaneAny(func(k string) string {
		switch k {
		case "ORCA_TERMINAL_HANDLE":
			return "native-here"
		case "TMUX_PANE":
			return "%inherited"
		}
		return ""
	})
	if pane != "%inherited" || kind != Tmux {
		t.Fatalf("current pane=%q kind=%q", pane, kind)
	}
}
func TestOrcaRelayNativePaneOperations(t *testing.T) {
	calls := 0
	sent := false
	h := New(Orca, Deps{Run: func(_ context.Context, name string, a []string) (Result, error) {
		calls++
		if name != "orca" {
			t.Fatalf("native pane invoked %s", name)
		}
		if len(a) < 4 || a[3] != "native-here" {
			t.Fatalf("wrong native target: %v", a)
		}
		switch a[1] {
		case "read":
			return Result{Stdout: `{"screen":"one\ntwo\nthree"}`}, nil
		case "send":
			sent = true
			for _, v := range a {
				if v == "--wait-submit" {
					t.Fatal("native pane send waits for pickup")
				}
			}
			return Result{Stdout: `{}`}, nil
		}
		t.Fatalf("unexpected operation: %v", a)
		return Result{}, nil
	}})
	p := h.Capabilities().PaneHost
	if got := p.CapturePane(context.Background(), "native-here", 2); got != "two\nthree" {
		t.Fatalf("native capture=%q", got)
	}
	if e := p.SendPane(context.Background(), "native-here", "literal\ntext"); e != nil || !sent {
		t.Fatalf("native send: sent=%v %v", sent, e)
	}
	before := calls
	if e := p.SendPane(context.Background(), "", "text"); e == nil || calls != before {
		t.Fatalf("empty native target called host: %v", e)
	}
	if e := p.SendPane(context.Background(), "native-here", strings.Repeat("x", 122881)); e == nil || calls != before {
		t.Fatalf("oversized native send called host: %v", e)
	}
}

func TestOrcaRelayEchoAloneDoesNotProveTurnStarted(t *testing.T) {
	f := newOrcaFake(t)
	f.receipt = `{"stages":["input_accepted"],"observation":"supported"}`
	f.after = "do work\n› "
	err := New(Orca, f.deps()).Send(context.Background(), "ben", f.handle(t), orcaBrief(t, "do work"))
	if !errors.Is(err, ErrNotSubmitted) {
		t.Fatalf("input echo is not turn-start evidence: %v", err)
	}
}
