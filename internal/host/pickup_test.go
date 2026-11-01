package host

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Exercise the retry policy through the real adapters. The only fakes are the
// terminal processes and clock, so these also pin each backend's protocol.
func TestPickupTmux(t *testing.T) {
	for _, pickupOn := range []int{1, 4, 5} {
		t.Run(fmt.Sprint(pickupOn), func(t *testing.T) {
			c := &clock{}
			enters := 0
			f := &fake{handler: func(_ string, a []string) Result {
				switch a[0] {
				case "send-keys":
					if a[len(a)-1] != "C-m" {
						t.Errorf("submit key = %q, want C-m", a[len(a)-1])
					}
					enters++
					if want := time.Duration(3*enters-2) * time.Second; c.slept != want {
						t.Errorf("Enter at %s, want %s", c.slept, want)
					}
				case "capture-pane":
					if enters > 0 {
						if want := time.Duration(3*enters) * time.Second; c.slept != want {
							t.Errorf("confirmation at %s, want %s", c.slept, want)
						}
					}
					if enters >= pickupOn {
						return Result{Stdout: "working"}
					}
					return Result{Stdout: "before"}
				}
				return Result{}
			}}
			file := filepath.Join(t.TempDir(), "brief")
			if err := os.WriteFile(file, []byte("do the task"), 0600); err != nil {
				t.Fatal(err)
			}
			var want error
			if pickupOn == 5 {
				want = ErrNotSubmitted
			}
			if err := New(Tmux, deps(f, nil, c)).Send(bg, "ben", "r:ben", file); err != want {
				t.Fatalf("Send = %v, want %v", err, want)
			}
			if enters != min(pickupOn, 4) || f.count("tmux paste-buffer") != 1 {
				t.Fatalf("enters=%d; brief must be pasted only once:\n%s", enters, f.log())
			}
		})
	}
}

func TestPickupHerdr(t *testing.T) {
	for _, tc := range []struct {
		name       string
		drops      int
		before     string
		after      string
		want       error
		wantEnters int
	}{
		{"first retry", 1, "", "", nil, 1},
		{"last retry", 3, "", "", nil, 3},
		{"exhausted", 4, "", "", ErrNotSubmitted, 3},
		{"already working", 1, "working", "", nil, 0},
		{"dialog before Enter", 1, "blocked", "", ErrDialogOpen, 0},
		{"unknown before Enter", 1, "unknown", "", ErrNotSubmitted, 0},
		{"dialog after Enter", 1, "", "blocked", ErrDialogOpen, 1},
		{"wait times out", 4, "", "unknown", ErrNotSubmitted, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &promptHost{dropEnters: tc.drops}
			f := &fake{handler: func(name string, a []string) Result {
				if a[1] == "get" && tc.before != "" {
					return Result{Stdout: agentJSON(tc.before)}
				}
				if a[1] == "wait" {
					if a[len(a)-1] != "10000" {
						t.Errorf("wait timeout = %s, want 10000ms", a[len(a)-1])
					}
					if tc.after != "" {
						return Result{Stdout: agentJSON(tc.after)}
					}
				}
				return p.handle(name, a)
			}}
			c := &clock{}
			file := orcaBrief(t, "--- ORCHESTRATOR (round 1) ---\nline one that wraps\nthe last line of the brief\n")
			err := New(Herdr, deps(f, herdrEnv, c)).Send(bg, "ben", "w9:t7", file)
			if err != tc.want {
				t.Fatalf("Send = %v, want %v", err, tc.want)
			}
			if n := f.count("herdr agent send-keys"); n != tc.wantEnters || p.typedTimes != 1 || c.slept != 0 {
				t.Fatalf("enters=%d typed=%d slept=%s; want %d, 1, 0", n, p.typedTimes, c.slept, tc.wantEnters)
			}
			if n := f.count("herdr agent send-keys rota-ben-w9-t7 enter"); n != tc.wantEnters {
				t.Fatalf("Enter sent to the wrong agent or with the wrong key:\n%s", f.log())
			}
		})
	}
}

func TestPickupOrca(t *testing.T) {
	for _, pickupOn := range []int{1, 4, 5} {
		t.Run(fmt.Sprint(pickupOn), func(t *testing.T) {
			f := newOrcaFake(t)
			f.receipt = "{}" // no authoritative turn-start receipt; must poll
			polls := 0
			c := &clock{}
			d := f.deps()
			d.Sleep = c.sleep
			d.Run = func(ctx context.Context, name string, a []string) (Result, error) {
				if name == "orca" && len(a) > 1 && a[1] == "read" && f.sends > 0 {
					polls++
					if want := time.Duration(polls) * time.Second; c.slept != want {
						t.Errorf("poll at %s, want %s", c.slept, want)
					}
					if polls >= pickupOn {
						f.after = "Working (esc to interrupt)\n› "
					}
				}
				return f.run(ctx, name, a)
			}
			var want error
			if pickupOn == 5 {
				want = ErrNotSubmitted
			}
			if err := New(Orca, d).Send(bg, "ben", f.handle(t), orcaBrief(t, "do the task")); err != want {
				t.Fatalf("Send = %v, want %v", err, want)
			}
			if polls != min(pickupOn, 4) || f.sends != 1 {
				t.Fatalf("polls=%d sends=%d; want %d, 1", polls, f.sends, min(pickupOn, 4))
			}
		})
	}
}
