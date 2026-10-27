package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/host"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type orcaReapHost struct {
	*fakeHost
	killErr error
	killed  bool
}

func (h *orcaReapHost) Kill(context.Context, string, string) error { h.killed = true; return h.killErr }

func TestOrcaPoolReapProvesSessionGoneBeforeRemovingWorktree(t *testing.T) {
	for _, fail := range []bool{true, false} {
		t.Run(map[bool]string{true: "survivor", false: "closed"}[fail], func(t *testing.T) {
			dir := newProject(t, `{"round":{"dispatch":"orca"}}`)
			goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
			UpdateSlot(dir, "w1", func(s *Slot) { s.SetHandle("orca:recorded") })
			h := &orcaReapHost{fakeHost: &fakeHost{name: host.Kind("orca")}}
			if fail {
				h.killErr = errors.New("child process still alive")
			}
			e := envWith(h)
			wt := LoadRegistryTolerant(dir).Slot("w1").Worktree()
			reaped, err := e.Reap(dir, []string{"w1"}, false)
			if !h.killed {
				t.Fatal("reap did not close and prove the session gone")
			}
			if fail {
				if err == nil || len(reaped) != 0 {
					t.Fatalf("unsafe reap = %v, %v", reaped, err)
				}
				if _, err := os.Stat(wt); err != nil {
					t.Fatal("removed worktree while child alive")
				}
				if LoadRegistryTolerant(dir).Slot("w1") == nil {
					t.Fatal("unregistered live slot")
				}
			} else if err != nil || len(reaped) != 1 {
				t.Fatalf("reap = %v, %v", reaped, err)
			}
		})
	}
}

func TestOrcaPoolStartsWithoutInventedTerminalHandle(t *testing.T) {
	dir := newProject(t, `{"round":{"dispatch":"orca"}}`)
	if _, err := goInit(t, dir, InitOpts{Slots: 1, Base: "main"}); err != nil {
		t.Fatal(err)
	}
	if got := LoadRegistryTolerant(dir).Slot("w1").Handle(); got != "" {
		t.Fatalf("undispatched slot handle=%q", got)
	}
}

func TestOrcaDispatchClaudeAndCodexThroughAdapter(t *testing.T) {
	for _, kind := range []string{harness.Claude, harness.Codex} {
		t.Run(kind, func(t *testing.T) {
			dir := newProject(t, `{"round":{"dispatch":"orca","commands":{"claude":"claude --model haiku"}}}`)
			goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
			command, sent, cwd := "", "", ""
			screen := "model: gpt-test\n› "
			unreadable := false
			reads := 0
			sends := 0
			h := host.New(host.Kind("orca"), host.Deps{Getenv: func(string) string { return "" }, LookPath: func(string) (string, error) { return "/fake/orca", nil }, Sleep: func(time.Duration) {}, Run: func(ctx context.Context, name string, a []string) (host.Result, error) {
				if name == "git" {
					return host.ExecRunner(ctx, name, a)
				}
				switch strings.Join(a[:2], " ") {
				case "status --json":
					return host.Result{Stdout: `{"running":true}`}, nil
				case "terminal create":
					for i, v := range a {
						if v == "--command" {
							command = a[i+1]
						}
						if v == "--worktree" {
							cwd = strings.TrimPrefix(a[i+1], "path:")
						}
					}
					b, _ := json.Marshal(map[string]string{"handle": "t1", "title": "rota:w1"})
					return host.Result{Stdout: string(b)}, nil
				case "terminal list":
					branch, err := host.ExecRunner(ctx, "git", []string{"-C", cwd, "symbolic-ref", "--short", "HEAD"})
					if err != nil {
						return host.Result{}, err
					}
					b, _ := json.Marshal(map[string]any{"terminals": []map[string]any{{"handle": "t1", "title": "agent overwrites this", "worktreePath": cwd, "branch": strings.TrimSpace(branch.Stdout), "connected": true, "writable": true}}})
					return host.Result{Stdout: string(b)}, nil
				case "terminal read":
					if unreadable {
						return host.Result{ExitCode: 1}, nil
					}
					reads++
					text := screen
					if screen == "moving" {
						text = fmt.Sprintf("output %d\n", reads)
					}
					b, _ := json.Marshal(map[string]string{"screen": text})
					return host.Result{Stdout: string(b)}, nil
				case "terminal send":
					sends++
					for i, v := range a {
						if v == "--text" {
							sent = a[i+1]
						}
					}
					return host.Result{Stdout: `{"stages":["input_accepted","turn_started"],"observation":"supported"}`}, nil
				}
				return host.Result{ExitCode: 1, Stderr: "unexpected call"}, nil
			}})
			rig := &codexRig{loggedIn: true, missing: map[string]bool{"herdr": true}}
			e := rig.env(h)
			e.Getenv = func(string) string { return "" }
			brief := "first line\n\n`literal` $HOME 'quoted' ö\n"
			_, err := e.Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, brief), Task: "T1", Kind: kind})
			t.Logf("harness=%s Orca prompt submissions=%d", kind, sends)
			if err != nil {
				t.Fatal(err)
			}
			if sends != 1 {
				t.Fatalf("prompt submitted %d times, want exactly one", sends)
			}
			if cwd != filepath.Join(dir, ".worktrees", "w1") {
				t.Fatalf("cwd=%q", cwd)
			}
			if !strings.Contains(command, "ROTA_SLOT=") || !strings.Contains(command, "ROTA_PORT_BLOCK=") {
				t.Fatalf("slot env absent: %s", command)
			}
			if !strings.Contains(sent, brief) {
				t.Fatalf("brief changed: %q", sent)
			}
			if rig.ran("herdr") != 0 {
				t.Fatal("Orca required herdr integration")
			}
			for _, tc := range []struct{ screen, want string }{
				{"moving", StateBusy},
				{"ROTA-DONE w1 https://github.com/o/r/pull/9\n", StateDone},
				{"ROTA-BLOCKED w1: need a decision\n", StateBlocked},
				{"You've hit your usage limit. resets at 5pm\n", StateLimited},
			} {
				screen = tc.screen
				res, err := e.Poll(bg, dir, PollOpts{Slot: "w1", Lines: 60})
				if err != nil || len(res.Slots) != 1 || res.Slots[0].State != tc.want {
					t.Fatalf("poll %q = %+v, %v", screen, res, err)
				}
			}
			unreadable = true
			res, err := e.Poll(bg, dir, PollOpts{Slot: "w1", Lines: 60})
			if err != nil || len(res.Slots) != 1 || res.Slots[0].State != StateUnverifiable {
				t.Fatalf("stale poll = %+v, %v", res, err)
			}

			if kind == harness.Codex {
				if strings.Contains(command, "CODEX_HOME=") {
					t.Fatal("default Codex account overridden")
				}
				if !strings.Contains(command, "worker prompt-check") || !strings.Contains(sent, "--- ROTA-SIG ") {
					t.Fatal("Codex prompt signing absent")
				}
			}
		})
	}
}

func TestOrcaDispatchPreservesHostFailureEvidence(t *testing.T) {
	for _, detail := range []string{"prompt already submitted: branch changed from ben/2203 to auto-name; disable autoRenameBranchFromWork", "brief has 122881 bytes; limit is 122880 bytes"} {
		dir := newProject(t, `{"round":{"dispatch":"orca","commands":{"claude":"claude --model haiku"}}}`)
		goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
		h := &fakeHost{name: host.Orca, sendErr: errors.New(detail)}
		_, err := envWith(h).Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "brief"), Task: "T1"})
		if exitOf(err) != 5 || !strings.Contains(err.Error(), detail) {
			t.Fatalf("Orca safety evidence hidden: %v", err)
		}
	}
}
