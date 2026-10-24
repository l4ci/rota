package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #1827 AC-2: a relay kind posts a comment whose first rota marker carries the
// round, so the other machine's gate can read it.
func TestItemCommentRelay(t *testing.T) {
	root := trackerProject(t, issuesConfig)
	fake := issueFixture()
	deps := withTracker(t, fake)
	body := bodyFile(t, "use the shared cache for the lookup\n")

	code, env, stderr := rotaRunWith(t, deps, "--json", "-C", root, "item", "comment", "add", "7", "--kind", "relay", "--body-file", body)
	if code != ExitOK {
		t.Fatalf("exit %d: %s %v", code, stderr, env)
	}
	if got := issueGet(issueData(t, env), "kind"); got != "relay" {
		t.Errorf("kind = %v", got)
	}
	if code, _, _ := rotaRunWith(t, deps, "--json", "-C", root, "item", "comment", "add", "7", "--kind", "relay", "--round", "3", "--body-file", body); code != ExitOK {
		t.Fatalf("--round: exit %d", code)
	}
	if code, _, _ := rotaRunWith(t, deps, "--json", "-C", root, "item", "comment", "add", "7", "--kind", "question", "--round", "3", "--body-file", body); code != ExitUsage {
		t.Errorf("--round on another kind: exit %d", code)
	}
	cs := fake.Issues[0].Comments
	if len(cs) != 2 {
		t.Fatalf("comments = %v", cs)
	}
	if !strings.Contains(cs[0].Body, "\n<!-- rota:relay round=1 -->\nuse the shared cache for the lookup") {
		t.Errorf("first = %q", cs[0].Body)
	}
	if !strings.Contains(cs[1].Body, "<!-- rota:relay round=3 -->\n") {
		t.Errorf("second = %q", cs[1].Body)
	}
}

// On a support machine a relay dispatch also lands on the slot's issue; on main
// it stays in the local log.
func TestDispatchRelayPostsComment(t *testing.T) {
	for _, role := range []string{"support", "main"} {
		t.Run(role, func(t *testing.T) {
			dir := workerProject(t, `{"issues":{"source":"github"},"round":{"support":"mac"}}`)
			writeLocalConfig(t, dir, `{"round":{"role":"`+role+`"}}`+"\n")
			fake := issueFixture()
			deps := withTracker(t, fake)
			rotaInWith(t, deps, dir, "worker", "pool", "init", "--slots", "1", "--base", "main")
			useHost(deps, &cliHost{inSession: true})
			brief := filepath.Join(t.TempDir(), "b.md")
			os.WriteFile(brief, []byte("use the shared cache for the lookup\n"), 0o644)

			if code, out, _ := rotaInWith(t, deps, dir, "worker", "dispatch", "w1", "--body-file", brief, "--task", "7", "--round", "2", "--json"); code != 0 {
				t.Fatalf("task dispatch: %d %s", code, out)
			}
			if n := len(fake.Issues[0].Comments); n != 0 {
				t.Fatalf("a task dispatch posted %d comments", n)
			}
			if code, out, _ := rotaInWith(t, deps, dir, "worker", "dispatch", "w1", "--body-file", brief, "--relay", "--round", "2", "--json"); code != 0 {
				t.Fatalf("relay: %d %s", code, out)
			}
			cs := fake.Issues[0].Comments
			if role == "main" {
				if len(cs) != 0 {
					t.Errorf("main posted %v", cs)
				}
				return
			}
			if len(cs) != 1 || !strings.Contains(cs[0].Body, "<!-- rota:relay round=2 -->\n") || !strings.Contains(cs[0].Body, "use the shared cache for the lookup") {
				t.Errorf("comments = %v", cs)
			}
		})
	}
}
