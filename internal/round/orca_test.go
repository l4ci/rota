package round

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/worker"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOrcaMissingSnapshotHandleCannotBeReclaimed(t *testing.T) {
	f := newMoveFx(t)
	f.env.HostName = host.Kind("orca")
	f.env.Snapshot = func(context.Context) ([]host.Agent, error) { return []host.Agent{}, nil }
	rawSlot(f.root, "ben", func(s *jsonx.Object) { s.Set("handle", "orca:old-runtime"); s.Set("state", "busy") })
	h := f.env.Health(bg, f.root, f.slot("ben"), f.now)
	if h.Health != HealthUnverifiable || h.Known || h.Reason != "stale-handle" {
		t.Fatalf("missing handle health = %+v", h)
	}
	if _, err := f.reclaim("ben", nil); blockedBy(t, err) != BlockUnverifiable {
		t.Fatalf("reclaim did not refuse missing handle: %v", err)
	}
	if len(f.killed) != 0 {
		t.Fatalf("reclaim killed %v", f.killed)
	}
	rep, err := f.env.Status(bg, f.root)
	if err != nil {
		t.Fatal(err)
	}
	for _, finding := range rep.Findings {
		if finding.Kind == DeadTab && finding.Slot == "ben" {
			t.Fatal("missing handle produced destructive dead-tab repair")
		}
	}
	found := false
	for _, row := range rep.Rows {
		if row.Name == "ben" {
			found = true
			if row.HostState != "unverifiable" || row.Evidence != "stale-handle" {
				t.Fatalf("row = %+v", row)
			}
		}
	}
	if !found {
		t.Fatal("slot missing from status")
	}
	res, err := f.reclaim("ben", func(o *ReclaimOpts) { o.Force = true })
	if err != nil || !res.Changed || len(f.killed) != 1 {
		t.Fatalf("forced reclaim must still kill/prove session: %+v, %v, killed=%v", res, err, f.killed)
	}
}

// Exercise the actual worker -> Orca adapter cleanup path. A runtime restart
// loses the native handle while the retained process evidence survives it.
func TestOrcaForceReclaimUsesAdapterRetainedProcessProof(t *testing.T) {
	for _, survivor := range []bool{false, true} {
		t.Run(map[bool]string{false: "all-gone", true: "orphan-child"}[survivor], func(t *testing.T) {
			f := newMoveFx(t)
			cwd := f.wt("ben")
			present := true
			closed := 0
			treeCalls := 0
			alive := map[int]bool{123: true, 456: true}
			deps := host.Deps{Getenv: noEnv, LookPath: func(string) (string, error) { return "/fake/orca", nil }, Sleep: func(time.Duration) {}, KillWait: 1,
				Alive: func(pid int) bool { return alive[pid] }, Tree: func(pid int) []int {
					treeCalls++
					if alive[pid] {
						return []int{pid, 456}
					}
					return []int{pid}
				},
				Run: func(ctx context.Context, name string, a []string) (host.Result, error) {
					if name == "git" {
						return host.ExecRunner(ctx, name, a)
					}
					switch strings.Join(a[:2], " ") {
					case "status --json":
						return host.Result{Stdout: `{"running":true}`}, nil
					case "terminal create":
						metadata, e := host.ExecRunner(ctx, "git", []string{"-C", cwd, "rev-parse", "--absolute-git-dir"})
						if e != nil || metadata.ExitCode != 0 {
							return host.Result{}, fmt.Errorf("fixture git metadata: %v %s", e, metadata.Stderr)
						}
						files, e := filepath.Glob(filepath.Join(strings.TrimSpace(metadata.Stdout), "rota-orca", "agent-*.pid"))
						if e != nil || len(files) != 1 {
							return host.Result{}, fmt.Errorf("fixture PID files: %v %v", files, e)
						}
						if e = os.WriteFile(files[0], []byte("123\n"), 0600); e != nil {
							return host.Result{}, e
						}
						return host.Result{Stdout: `{"handle":"native-ben","title":"rota:ben"}`}, nil
					case "terminal read":
						if !present {
							return host.Result{ExitCode: 1}, nil
						}
						return host.Result{Stdout: `{"screen":"model: gpt-test\n› "}`}, nil
					case "terminal list":
						if !present {
							return host.Result{Stdout: `{"terminals":[]}`}, nil
						}
						b, _ := json.Marshal(map[string]any{"terminals": []map[string]string{{"handle": "native-ben", "title": "agent changed title", "worktreePath": cwd, "branch": gitIn(t, cwd, "symbolic-ref", "--short", "HEAD")}}})
						return host.Result{Stdout: string(b)}, nil
					case "terminal close":
						closed++
						return host.Result{ExitCode: 1, Stderr: "stale handle must never be closed"}, nil
					}
					return host.Result{ExitCode: 1, Stderr: "unexpected fixture operation"}, nil
				}}
			h := host.New(host.Orca, deps)
			handle, e := h.Spawn(bg, host.SpawnOpts{Session: "rota", Slot: "ben", Cwd: cwd, Launch: "codex", BootTimeout: 2})
			if e != nil {
				t.Fatal(e)
			}
			if e = worker.Update(f.root, func(d *worker.Doc) { d.SetHost(host.Orca) }); e != nil {
				t.Fatal(e)
			}
			if e = rawSlot(f.root, "ben", func(s *jsonx.Object) { s.Set("handle", handle); s.Set("state", "busy") }); e != nil {
				t.Fatal(e)
			}
			f.env.HostName = host.Orca
			f.env.Snapshot = func(ctx context.Context) ([]host.Agent, error) {
				return host.New(host.Orca, deps).Capabilities().Snapshotter.Snapshot(ctx)
			}
			f.wire(func(w *worker.Env) {
				w.NewHost = func(k host.Kind) host.Host {
					if k != host.Orca {
						t.Fatalf("cleanup selected %s", k)
					}
					return host.New(host.Orca, deps)
				}
			})
			present = false
			alive[123] = false
			alive[456] = survivor
			if health := f.env.Health(bg, f.root, f.slot("ben"), f.now); health.Health != HealthUnverifiable {
				t.Fatalf("stale health=%+v", health)
			}
			before := treeCalls
			if _, e = f.reclaim("ben", nil); blockedBy(t, e) != BlockUnverifiable {
				t.Fatalf("unforced reclaim: %v", e)
			}
			if treeCalls != before || closed != 0 {
				t.Fatal("unforced reclaim attempted process cleanup")
			}
			result, e := f.reclaim("ben", func(o *ReclaimOpts) { o.Force = true })
			if survivor {
				if e == nil || result.Changed || !strings.Contains(e.Error(), "456") {
					t.Fatalf("reclaimed with orphan child: %+v %v", result, e)
				}
				if f.slot("ben").Task() != "12" || f.slot("ben").Handle() != handle {
					t.Fatal("failed reclaim discarded live slot ownership")
				}
				if _, e = os.Stat(cwd); e != nil {
					t.Fatalf("moved worktree under surviving child: %v", e)
				}
			} else if e != nil || !result.Changed {
				t.Fatalf("force could not reclaim all-gone retained tree: %+v %v", result, e)
			}
			if closed != 0 {
				t.Fatal("cleanup tried to close an absent native handle")
			}
		})
	}
}
