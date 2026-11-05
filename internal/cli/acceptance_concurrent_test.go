package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/tracker"
)

// Cross planPass, the issue-note store and the real GitHub adapter, including
// separate invocation caches. Hold AC-1's PATCH while AC-2 reads the old notes
// for proof validation, then let both continue. Without a fresh transaction,
// both replacements contain only their own mark and AC-3, whichever lands last.
func TestPlanPassConcurrentKeepsMarks(t *testing.T) {
	for _, linked := range []bool{false, true} {
		t.Run(fmt.Sprintf("linked=%t", linked), func(t *testing.T) {
			root := trackerProject(t, issuesConfig)
			other := root
			if linked {
				gittest.Init(t, root, "main")
				gittest.Run(t, root, "add", ".rota/config.json")
				gittest.Run(t, root, "commit", "-q", "-m", "fixture")
				other = filepath.Join(t.TempDir(), "linked")
				gittest.Run(t, root, "worktree", "add", "-q", "-b", "other", other)
			}
			var mu sync.Mutex
			note := "<!-- rota:acceptance -->\n- AC-3 · 2026-01-01 · abc1234 · check · third\n"
			patchStarted, secondRead := make(chan struct{}), make(chan struct{})
			var patchOnce, readOnce sync.Once
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			wait := func(ch <-chan struct{}) error {
				select {
				case <-ch:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			newCtx := func(dir string, second bool) *Ctx {
				reads := 0
				d := testDeps()
				d.Getwd = func() (string, error) { return dir, nil }
				d.Getenv = func(string) string { return "" }
				d.TrackerOptions = []tracker.Option{tracker.WithExec(func(_ context.Context, _, _ string, args []string, _ []byte) ([]byte, []byte, int, error) {
					joined := strings.Join(args, " ")
					switch {
					case len(args) > 1 && args[0] == "issue" && args[1] == "view":
						out, err := json.Marshal(map[string]any{"number": 7, "title": "Export", "state": "OPEN", "body": "## Acceptance\n- [ ] AC-1: first\n- [ ] AC-2: second\n- [ ] AC-3: third\n", "labels": []map[string]string{{"name": "type:feature"}}})
						return out, nil, 0, err
					case strings.Contains(joined, "-X PATCH"):
						body := ""
						for _, arg := range args {
							if strings.HasPrefix(arg, "body=") {
								body = strings.TrimPrefix(arg, "body=")
							}
						}
						if !second {
							patchOnce.Do(func() { close(patchStarted) })
							if err := wait(secondRead); err != nil {
								return nil, nil, 0, err
							}
						}
						mu.Lock()
						note = body
						mu.Unlock()
						return []byte(`{"id":2}`), nil, 0, nil
					case len(args) > 0 && args[0] == "api" && strings.Contains(joined, "/comments"):
						reads++
						mu.Lock()
						out, err := json.Marshal([]map[string]any{
							{"id": 1, "body": "<!-- rota:proof -->\n## Proof\n\n- 2026-01-01 · check · PASS · abc1234 · evidence\n"},
							{"id": 2, "body": note},
						})
						mu.Unlock()
						if second && reads == 2 { // proof.Show refreshes the status read
							readOnce.Do(func() {
								close(secondRead)
							})
						}
						return out, nil, 0, err
					default:
						return nil, nil, 0, fmt.Errorf("unexpected forge command: %v", args)
					}
				}, func(n string) (string, error) { return "/fake/" + n, nil })}
				return &Ctx{Deps: d, Stderr: io.Discard, ctx: ctx}
			}
			mark := func(c *Ctx, ref, ac string) error {
				fs := flag.NewFlagSet("pass", flag.ContinueOnError)
				run := planPass(fs)
				if err := fs.Parse([]string{"--proof", "abc1234:check"}); err != nil {
					return err
				}
				_, err := run(c, []string{ref, ac})
				return err
			}
			firstResult := make(chan error, 1)
			go func() {
				firstResult <- mark(newCtx(root, false), "#7", "AC-1")
			}()
			select {
			case <-patchStarted:
			case err := <-firstResult:
				t.Fatalf("first mark ended before PATCH: %v", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			secondErr := mark(newCtx(other, true), "F7", "AC-2")
			if firstErr := <-firstResult; firstErr != nil || secondErr != nil {
				t.Fatalf("marks: first=%v second=%v", firstErr, secondErr)
			}
			for _, ac := range []string{"AC-1", "AC-2", "AC-3"} {
				if !strings.Contains(note, "- "+ac+" · ") {
					t.Errorf("successful overlapping marks lost %s:\n%s", ac, note)
				}
			}
			t.Logf("acceptance marks retained: %d/3", strings.Count(note, " · check · "))

			// The same seam covers date stamping and replacement: re-marking
			// AC-2 changes only that mark and never appends a duplicate.
			replace := newCtx(other, true)
			replace.Deps.Today = func() time.Time { return time.Date(2031, 4, 5, 9, 0, 0, 0, time.UTC) }
			if err := mark(replace, "7", "AC-2"); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(note, "- AC-2 · 2031-04-05 · abc1234 · check · second") ||
				strings.Count(note, "- AC-2 · ") != 1 || strings.Count(note, " · check · ") != 3 {
				t.Fatalf("same-criterion replacement changed unrelated marks or date:\n%s", note)
			}
		})
	}
}
