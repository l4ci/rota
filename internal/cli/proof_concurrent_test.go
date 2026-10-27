package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// Exercise the real GitHub adapter and its per-invocation read cache. A fake
// backlog.Tracker would skip the cache that holds the pre-command note read.
func TestProofRecordConcurrentKeepsBothRows(t *testing.T) {
	for _, backend := range []string{"files", "issues"} {
		t.Run(backend, func(t *testing.T) {
			root, id := backlogProject(t), "B07"
			if backend == "issues" {
				root, id = trackerProject(t, issuesConfig), "7"
			}
			var mu sync.Mutex
			comments := []map[string]any{}
			execute := func(_ context.Context, _, _ string, args []string, _ []byte) ([]byte, []byte, int, error) {
				mu.Lock()
				defer mu.Unlock()
				if len(args) > 1 && args[0] == "issue" && args[1] == "view" {
					return []byte(`{"number":7,"title":"Export","state":"OPEN","labels":[{"name":"type:feature"}]}`), nil, 0, nil
				}
				joined := strings.Join(args, " ")
				switch {
				case strings.Contains(joined, "-X POST"), strings.Contains(joined, "-X PATCH"):
					body := ""
					for _, arg := range args {
						if strings.HasPrefix(arg, "body=") {
							body = strings.TrimPrefix(arg, "body=")
						}
					}
					if strings.Contains(joined, "-X POST") {
						comments = append(comments, map[string]any{"id": len(comments) + 1, "body": body})
					} else {
						comments[0]["body"] = body
					}
					return []byte(`{"id":1}`), nil, 0, nil
				case len(args) > 0 && args[0] == "api" && strings.Contains(joined, "/comments"):
					out, err := json.Marshal(comments)
					return out, nil, 0, err
				default:
					return nil, nil, 0, fmt.Errorf("unexpected forge command: %v", args)
				}
			}
			started, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			defer unblock()
			newCtx := func() *Ctx {
				d := testDeps()
				d.Getwd = func() (string, error) { return root, nil }
				d.Getenv = func(key string) string {
					if key == testLockHeldEnv {
						return "1"
					}
					return ""
				}
				d.TrackerOptions = []tracker.Option{tracker.WithExec(execute, func(n string) (string, error) { return "/fake/" + n, nil })}
				d.WorkerEnv = func() worker.Env {
					return worker.Env{Shell: func(ctx context.Context, _, command string) (string, int) {
						if strings.Contains(command, "slow-check") {
							close(started)
							select {
							case <-release:
							case <-ctx.Done():
								return "cancelled", 1
							}
						}
						return "ok", 0
					}}
				}
				return &Ctx{Deps: d, Stderr: io.Discard, dashAt: 1}
			}
			record := func(command string) error {
				run := proofRecord(flag.NewFlagSet("record", flag.ContinueOnError))
				_, err := run(newCtx(), []string{id, command})
				return err
			}
			if err := record("seed-check"); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- record("slow-check") }()
			select {
			case <-started:
			case err := <-done:
				t.Fatalf("slow record ended before command started: %v", err)
			case <-time.After(10 * time.Second):
				t.Fatal("slow command never started")
			}
			fastErr := record("fast-check")
			unblock()
			if err := <-done; err != nil || fastErr != nil {
				t.Fatalf("records: slow=%v fast=%v", err, fastErr)
			}
			show := proofShow(flag.NewFlagSet("show", flag.ContinueOnError))
			res, err := show(newCtx(), []string{id})
			if err != nil {
				t.Fatal(err)
			}
			for _, check := range []string{"seed-check", "fast-check", "slow-check"} {
				if !strings.Contains(res.Text, " · "+check+" · PASS · ") {
					t.Errorf("proof show lost %s:\n%s", check, res.Text)
				}
			}
			t.Logf("proof rows retained: %d/3", strings.Count(res.Text, " · PASS · "))
		})
	}
}
