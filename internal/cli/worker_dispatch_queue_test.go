package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/host"
)

// Exercise the dispatch exit mapping through the real herdr adapter, with only
// the external herdr process faked. A queued preview omits the signed tail.
func TestWorkerDispatchQueuedCodexRelay(t *testing.T) {
	for _, status := range []string{"working", "blocked"} {
		for _, c := range []struct {
			name, preview string
			want          int
		}{
			{"queued", "• Queued follow-up messages\n  ↳ --- ORCHESTRATOR (round 37) ---\n    [relay 1]\n", 0},
			{"gutter", "┃ [relay 1]\n", 0},
			{"earlier", "• Queued follow-up messages\n    [relay 10]\n", 6},
			{"absent", "", 6},
			{"quoted", "The previous response mentioned [relay 1] in prose.\n", 6},
		} {
			t.Run(status+"/"+c.name, func(t *testing.T) {
				deps := testDeps()
				root := workerProject(t, `{"round":{"dispatch":"herdr"}}`)
				if code, out, errOut := rotaInWith(t, deps, root, "worker", "pool", "init", "--slots", "1", "--base", "main"); code != 0 {
					t.Fatalf("pool init: %d %s %s", code, out, errOut)
				}
				brief := filepath.Join(t.TempDir(), "brief.md")
				if err := os.WriteFile(brief, []byte("Continue the task.\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				// Start a Codex slot and its signing key through normal dispatch.
				useCodex(deps, &cliHost{herdr: true, inSession: true}, "codex-cli 0.163.0", true)
				if code, out, errOut := rotaInWith(t, deps, root, "worker", "dispatch", "w1", "--task", "T1", "--kind", "codex", "--round", "37", "--body-file", brief); code != 0 {
					t.Fatalf("initial dispatch: %d %s %s", code, out, errOut)
				}
				var sent string
				agent := fmt.Sprintf(`{"result":{"agent":{"agent":"codex","agent_status":%q}}}`, status)
				h := host.New("herdr", host.Deps{
					LookPath: func(n string) (string, error) { return "/fake/" + n, nil },
					Getenv: func(k string) string {
						return map[string]string{"HERDR_ENV": "1", "HERDR_WORKSPACE_ID": "w9"}[k]
					},
					Sleep: func(time.Duration) {},
					Run: func(_ context.Context, name string, args []string) (host.Result, error) {
						if name == "herdr" && len(args) >= 3 && args[0] == "agent" {
							switch args[1] {
							case "get":
								return host.Result{Stdout: agent}, nil
							case "read":
								preview := ""
								if sent != "" {
									preview = c.preview
								}
								return host.Result{Stdout: "OpenAI Codex\n" + preview + "› Ask Codex to do anything\n"}, nil
							case "prompt":
								sent = args[3]
								return host.Result{Stdout: agent}, nil
							}
						}
						t.Errorf("unexpected host command: %s %v", name, args)
						return host.Result{ExitCode: 99}, nil
					},
				})
				useHost(deps, h)
				code, out, errOut := rotaInWith(t, deps, root, "--json", "worker", "dispatch", "w1", "--relay", "--body-file", brief)
				if code != c.want {
					t.Fatalf("queued relay dispatch: exit %d, want %d: %s %s", code, c.want, out, errOut)
				}
				if !strings.Contains(sent, "\n[relay 1]\n") || !strings.Contains(sent, "--- ROTA-SIG ") {
					t.Fatalf("expected a signed, numbered relay, got %q", sent)
				}
				if code == 0 && data(t, out)["relay"] != true {
					t.Fatalf("dispatch did not report a relay: %s", out)
				}
			})
		}
	}
}
