package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestItemCompleteUnmetAcceptance(t *testing.T) {
	root := trackerProject(t, issuesConfig)
	fake := acceptFixture()
	fake.Issues[0].Body = "## Acceptance\n\n- [ ] AC-1: first\n- [x] AC-2: second\n"
	deps := withTracker(t, fake)
	addProof(t, deps, root, "PASS", "abc1234", "check")
	code, env, stderr := issueRunWith(t, deps, root, "item", "complete", "7", "--commit", "abc1234")
	t.Logf("completion exit=%d", code)
	if code != 4 || !strings.Contains(stderr, "AC-1") || !strings.Contains(stderr, "AC-2") {
		t.Fatalf("unmet completion: exit %d, data %v, stderr %s", code, env, stderr)
	}
	if fake.Issues[0].State != "open" {
		t.Fatal("refused item was closed")
	}
}

func TestFileCompleteUnmetAcceptance(t *testing.T) {
	root := trackerProject(t, "")
	body := "## Acceptance\n\n- [ ] AC-1: first\n- [ ] AC-2: second\n\n## Completed\n\nExisting close history.\n"
	if err := os.MkdirAll(filepath.Join(root, ".rota/bugs"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".rota/bugs/B01.md"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	code, out, stderr := rotaIn(t, root, "item", "complete", "B01", "--commit", "abc1234", "--no-proof", "--json")
	if code != 4 || !strings.Contains(stderr+out, "AC-1") || !strings.Contains(stderr+out, "AC-2") {
		t.Fatalf("file completion: exit %d: %s %s", code, out, stderr)
	}
	if strings.Contains(stderr+out, "plan pass") {
		t.Fatalf("file backend suggests unavailable plan pass: %s %s", out, stderr)
	}
	code, _, stderr = rotaIn(t, root, "item", "complete", "B01", "--commit", "abc1234", "--no-proof", "--unmet-reason", "defer second")
	if code != 0 {
		t.Fatalf("override: %d %s", code, stderr)
	}
	detail, err := os.ReadFile(filepath.Join(root, ".rota/bugs/B01.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"## Completed", "Existing close history.", "AC-1: first", "AC-2: second", "Reason: defer second"} {
		if !strings.Contains(string(detail), want) {
			t.Errorf("detail lacks %q: %s", want, detail)
		}
	}
}

func TestItemCompleteAcceptanceCoverage(t *testing.T) {
	for _, tc := range []struct {
		name, body, reason, override string
		mark, withdraw, edit         bool
		want                         int
	}{
		{name: "met", mark: true, want: 0},
		{name: "overridden", override: "ship the tested subset", want: 0},
		{name: "blank override", override: "  ", want: 4},
		{name: "changed mark", mark: true, edit: true, want: 4},
		{name: "withdrawn proof", mark: true, withdraw: true, want: 4},
		{name: "legacy", body: "## Acceptance\n- [ ] unnumbered\n", want: 0},
		{name: "dropped", reason: "dropped", want: 0},
		{name: "handed off", reason: "handed-off", want: 0},
		{name: "blocked", reason: "blocked", want: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := trackerProject(t, issuesConfig)
			fake := acceptFixture()
			fake.Issues[0].Body = "## Acceptance\n- [ ] AC-1: first\n"
			if tc.body != "" {
				fake.Issues[0].Body = tc.body
			}
			deps := withTracker(t, fake)
			addProof(t, deps, root, "PASS", "abc1234", "check")
			if tc.mark {
				if code, _, stderr := issueRunWith(t, deps, root, "plan", "pass", "#7", "AC-1", "--proof", "abc1234:check"); code != 0 {
					t.Fatalf("mark: %d %s", code, stderr)
				}
			}
			if tc.edit {
				fake.Issues[0].Body = "## Acceptance\n- [ ] AC-1: changed\n"
			}
			if tc.withdraw {
				addProof(t, deps, root, "FAIL", "abc1234", "check")
			}
			reason := tc.reason
			if reason == "" {
				reason = "done"
			}
			code, env, stderr := issueRunWith(t, deps, root, "item", "complete", "7", "--commit", "abc1234", "--reason", reason, "--unmet-reason", tc.override)
			if code != tc.want {
				t.Fatalf("exit %d want %d: %v %s", code, tc.want, env, stderr)
			}
			if tc.override != "" && tc.want == 0 {
				var close string
				for _, c := range fake.Issues[0].Comments {
					close += c.Body
				}
				for _, want := range []string{"AC-1: first", "Reason: ship the tested subset"} {
					if !strings.Contains(close, want) {
						t.Errorf("close lacks %q: %s", want, close)
					}
				}
			}
		})
	}
}
