package cli

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/stamp"
	"github.com/l4ci/rota/internal/worker"
)

type statusRelayHost struct {
	*cliHost
	pane, sent, native string
	captures           []string
}

func TestWorkerPollRejectsSupersededCompletionInTextAndJSON(t *testing.T) {
	for _, replaceSession := range []bool{false, true} {
		for _, jsonOutput := range []bool{false, true} {
			name := map[bool]string{false: "new relay", true: "new session"}[replaceSession] + "/" + map[bool]string{false: "text", true: "json"}[jsonOutput]
			t.Run(name, func(t *testing.T) {
				root := workerProject(t, `{}`)
				deps := testDeps()
				echo := "❯ --- ORCHESTRATOR (round 48) ---\n  [relay 1]\n\n● working\n"
				h := &statusRelayHost{cliHost: &cliHost{inSession: true}, native: "idle", captures: []string{
					echo, strings.Repeat("● work\n", 80) + "ROTA-DONE w1 https://github.com/o/r/pull/7\n",
				}}
				useHost(deps, h)
				if code, out, errOut := rotaInWith(t, deps, root, "worker", "pool", "init", "--slots", "1", "--base", "main"); code != 0 {
					t.Fatalf("pool: exit %d: %s %s", code, out, errOut)
				}
				at := stamp.Format(deps.Now())
				if _, err := worker.UpdateSlot(root, "w1", func(s *worker.Slot) {
					s.Dispatch("old:session", "2346", "", at)
					s.SetRelayPending(1, at)
				}); err != nil {
					t.Fatal(err)
				}
				baseEnv := deps.WorkerEnv
				deps.WorkerEnv = func() worker.Env {
					e := baseEnv()
					e.Sleep = func(time.Duration) {
						if _, err := worker.UpdateSlot(root, "w1", func(s *worker.Slot) {
							pending := 2
							if replaceSession {
								s.Dispatch("new:session", "2346", "", at)
								pending = 1
							}
							s.SetRelayPending(pending, at)
						}); err != nil {
							t.Fatal(err)
						}
					}
					return e
				}
				args := []string{"worker", "poll", "w1", "--settle", "0"}
				if jsonOutput {
					args = append([]string{"--json"}, args...)
				}
				code, out, errOut := rotaInWith(t, deps, root, args...)
				if code != 0 {
					t.Fatalf("poll: exit %d: %s %s", code, out, errOut)
				}
				if jsonOutput {
					rows := data(t, out)["slots"].([]any)
					if len(rows) != 1 || rows[0].(map[string]any)["name"] != "w1" || rows[0].(map[string]any)["state"] != "busy" {
						t.Fatalf("superseded completion in JSON: %s", out)
					}
				} else if !strings.HasPrefix(out, "w1\tbusy\t") {
					t.Fatalf("superseded completion in text: %q", out)
				}
				if strings.Contains(out, "https://github.com/o/r/pull/7") {
					t.Fatalf("superseded PR evidence was returned: %s", out)
				}
				if s := worker.LoadRegistryTolerant(root).Slot("w1"); s.State() != "busy" || s.RelayPending() == 0 || s.RelayMarkerSeen() || s.PR() != "" {
					t.Fatalf("superseded completion changed current slot: %v", s.Raw())
				}
			})
		}
	}
}

func (h *statusRelayHost) Capabilities() host.Capabilities { return host.CapabilitiesOf(h) }
func (h *statusRelayHost) Capture(context.Context, string, string, int) string {
	if len(h.captures) > 0 {
		h.pane, h.captures = h.captures[0], h.captures[1:]
	}
	return h.pane
}
func (h *statusRelayHost) Status(context.Context, string, string) string { return h.native }
func (h *statusRelayHost) Send(ctx context.Context, slot, handle, file string) error {
	b, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	h.sent = string(b)
	return h.cliHost.Send(ctx, slot, handle, file)
}
func (h *statusRelayHost) Snapshot(context.Context) ([]host.Agent, error) {
	return []host.Agent{{Tab: "rota:dana", Name: "dana", Cwd: h.spawnOpts.Cwd, Status: h.native}}, nil
}

func TestRoundStatusRelayCompletionAndNextAssignment(t *testing.T) {
	f := newScopeFx(t, "warn", "")
	if _, err := worker.UpdateSlot(f.root, "ben", func(s *worker.Slot) {
		s.Unbind()
		s.SetBranch("park/ben")
	}); err != nil {
		t.Fatal(err)
	}
	h := &statusRelayHost{cliHost: &cliHost{inSession: true}, native: "idle"}
	f.deps.Host = func(host.Kind) host.Host { return h }
	useHost(f.deps, h)
	origin := filepath.Join(t.TempDir(), "origin.git")
	gittest.Run(t, f.root, "init", "--bare", "-q", origin)
	gittest.Run(t, f.root, "remote", "add", "origin", origin)
	gittest.Run(t, f.root, "push", "-q", "origin", "feat/x")
	run := func(args ...string) string {
		t.Helper()
		code, out, errOut := rotaInWith(t, f.deps, f.root, args...)
		if code != 0 {
			t.Fatalf("%v: exit %d: %s %s", args, code, out, errOut)
		}
		return out
	}
	assign := func(id string) {
		t.Helper()
		run("round", "assign", id, "--agent", "dana", "--holder-pid", "4242", "--accept-overlap")
	}
	assign("2")
	s := worker.LoadRegistryTolerant(f.root).Slot("dana")
	write(t, filepath.Join(s.Worktree(), "work.txt"), "work")
	gittest.Run(t, s.Worktree(), "add", "work.txt")
	gittest.Run(t, s.Worktree(), "commit", "-qm", "work")
	gittest.Run(t, s.Worktree(), "push", "-q", "origin", s.Branch())
	head := gittest.Run(t, s.Worktree(), "rev-parse", "HEAD")
	const done = "ROTA-DONE dana https://github.com/o/r/pull/7\n"
	h.pane = done
	run("worker", "poll", "dana", "--settle", "0")
	brief := filepath.Join(t.TempDir(), "reply.md")
	write(t, brief, "Clarify the PR body.\n")
	relay := func() { run("worker", "dispatch", "dana", "--relay", "--body-file", brief) }
	pollReply := func() {
		h.captures = []string{h.sent + "\n● working\n", strings.Repeat("● work\n", 80) + done}
		run("worker", "poll", "dana", "--settle", "0")
	}
	checkStatus := func(wantDone bool) {
		t.Helper()
		before := worker.LoadRegistryTolerant(f.root).Slot("dana")
		out := run("--json", "round", "status")
		found := false
		for _, row := range data(t, out)["slots"].([]any) {
			r := row.(map[string]any)
			if r["name"] == "dana" {
				found = true
				if r["hostState"] != "idle" || (r["state"] == "done") != wantDone {
					t.Fatalf("completion must be separate from native idle: %v, want done=%v", r, wantDone)
				}
			}
		}
		if !found {
			t.Fatal("status omitted dana")
		}
		text := run("round", "status")
		if !strings.HasPrefix(text, "dana\t") && !strings.Contains(text, "\ndana\t") {
			t.Fatal("text status omitted dana")
		}
		for _, line := range strings.Split(text, "\n") {
			if strings.HasPrefix(line, "dana\t") && strings.Contains(line, "\tstate done") != wantDone {
				t.Fatalf("text completion mismatch: %q, want done=%v", line, wantDone)
			}
		}
		after := worker.LoadRegistryTolerant(f.root).Slot("dana")
		if !reflect.DeepEqual(before.Raw(), after.Raw()) {
			t.Fatalf("status changed the worker slot: before=%v after=%v", before.Raw(), after.Raw())
		}
	}
	relay()
	pollReply()
	if got := gittest.Run(t, s.Worktree(), "rev-parse", "HEAD"); got != head {
		t.Fatalf("text-only relay moved head: %s -> %s", head, got)
	}
	checkStatus(true)
	// A newer relay reopens the same slot even though its host is still idle
	// and its pane still carries exactly the earlier completion.
	relay()
	run("worker", "poll", "dana", "--settle", "0")
	checkStatus(false)
	if code, out, errOut := rotaInWith(t, f.deps, f.root, "round", "assign", "1", "--agent", "dana", "--holder-pid", "4242", "--accept-overlap"); code != 4 {
		t.Fatalf("new relay must hold assignment: exit %d: %s %s", code, out, errOut)
	}
	pollReply()
	checkStatus(true)
	assign("1")
	checkStatus(false)
}
