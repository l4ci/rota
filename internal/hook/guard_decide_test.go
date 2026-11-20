package hook

import (
	"errors"
	"strings"
	"testing"
)

func fakeGit(dir string, args ...string) (string, error) {
	switch args[0] {
	case "branch":
		if strings.HasPrefix(dir, gWork) {
			return gBranch, nil
		}
		return "main", nil
	case "rev-parse":
		if args[1] == "--show-toplevel" {
			if strings.HasPrefix(dir, gWork) {
				return gWork, nil
			}
			return dir, nil
		}
		return "origin/" + gBranch, nil
	case "worktree":
		return "worktree /repo\nworktree " + gWork, nil
	}
	return "", errors.New("unexpected git call")
}

func TestDecideGuard(t *testing.T) {
	payload := func(tool, cmd, cwd string) []byte {
		s := `{"tool_name":"` + tool + `","tool_input":{"command":"` + cmd + `"},"cwd":"` + cwd + `"}`
		return []byte(s)
	}
	tests := []struct {
		name    string
		payload []byte
		cwd     string
		deny    string // substring of the expected reason; "" means the call passes
	}{
		{"non-bash tool passes", payload("Edit", "git push --force origin main", gWork), gWork, ""},
		{"empty command passes", payload("Bash", "", gWork), gWork, ""},
		{"force push denied", payload("Bash", "git push --force origin main", gWork), gWork, "force"},
		{"merge denied", payload("Bash", "gh pr merge 1", gWork), gWork, "merge"},
		{"plain command passes", payload("Bash", "go test ./...", gWork), gWork, ""},
		{"own branch push passes", payload("Bash", "git push origin HEAD", gWork), gWork, ""},
		{"missing cwd uses default", payload("Bash", "git reset --hard", ""), gWork, ""},
		{"missing cwd, default outside worktree", payload("Bash", "git reset --hard", ""), "/repo", "outside this worktree"},
		{"payload cwd beats default", payload("Bash", "git reset --hard", "/repo"), gWork, "outside this worktree"},
		{"workspace.current_dir beats default", []byte(`{"tool_name":"Bash","tool_input":{"command":"git reset --hard"},"workspace":{"current_dir":"/repo"}}`), gWork, "outside this worktree"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := DecideGuard(GuardRequest{Payload: mustPayload(t, tc.payload), Slot: "dana", DefaultCwd: tc.cwd, Git: fakeGit})
			if (tc.deny == "" && got != "") || !strings.Contains(got, tc.deny) {
				t.Fatalf("reason = %q, want substring %q", got, tc.deny)
			}
		})
	}
}

func TestSlotWorktree(t *testing.T) {
	git := func(string, ...string) (string, error) { return "", errors.New("no git") }
	if got := slotWorktree(gWork+"/internal", "dana", git); got != gWork {
		t.Errorf("ancestor: got %q", got)
	}
	if got := slotWorktree("/elsewhere", "dana", git); got != "" {
		t.Errorf("git failure: got %q", got)
	}
	if got := slotWorktree("/elsewhere", "dana", fakeGit); got != gWork {
		t.Errorf("worktree list: got %q", got)
	}
}

func TestDecideGuardProtectsConfiguredBase(t *testing.T) {
	p := mustPayload(t, []byte(`{"tool_name":"Bash","tool_input":{"command":"git push origin HEAD:develop"},"cwd":"`+gWork+`"}`))
	req := GuardRequest{Payload: p, Slot: "dana", DefaultCwd: gWork, Git: fakeGit, Base: "develop"}
	if got := DecideGuard(req); got == "" {
		t.Fatal("push to the configured base passed through DecideGuard")
	}
	req.Base = ""
	if got := DecideGuard(req); got != "" {
		t.Fatalf("without Base, develop is an ordinary branch: %q", got)
	}
}

// upstreamGit wraps fakeGit with the two ways git reports a branch's upstream:
// `rev-parse @{upstream}` (exit 128 when none is configured) and
// `for-each-ref %(upstream:short)` (empty output, exit 0 when none).
func upstreamGit(up string, readErr error) GitFunc {
	return func(dir string, args ...string) (string, error) {
		switch {
		case args[0] == "symbolic-ref":
			return "refs/heads/" + gBranch, nil
		case args[0] == "for-each-ref":
			return up, readErr
		case args[0] == "rev-parse" && args[1] == "--abbrev-ref":
			if readErr != nil {
				return "", readErr
			}
			if up == "" {
				return "", errors.New("fatal: no upstream configured")
			}
			return up, nil
		}
		return fakeGit(dir, args...)
	}
}

func TestDecideGuardForcePushUpstreamRead(t *testing.T) {
	p := mustPayload(t, []byte(`{"tool_name":"Bash","tool_input":{"command":"git push --force-with-lease"},"cwd":"`+gWork+`"}`))
	run := func(git GitFunc) string {
		return DecideGuard(GuardRequest{Payload: p, Slot: "dana", DefaultCwd: gWork, Git: git})
	}
	if got := run(upstreamGit("", errors.New("fatal: not a git repository"))); !strings.Contains(got, "upstream") {
		t.Errorf("unreadable upstream must deny, got %q", got)
	}
	if got := run(upstreamGit("", nil)); got != "" {
		t.Errorf("no configured upstream must pass, got %q", got)
	}
	if got := run(upstreamGit("origin/"+gBranch, nil)); got != "" {
		t.Errorf("own upstream must pass, got %q", got)
	}
}
