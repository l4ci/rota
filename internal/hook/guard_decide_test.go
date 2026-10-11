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
		{"bad json passes", []byte("{"), gWork, ""},
		{"non-bash tool passes", payload("Edit", "git push --force origin main", gWork), gWork, ""},
		{"empty command passes", payload("Bash", "", gWork), gWork, ""},
		{"force push denied", payload("Bash", "git push --force origin main", gWork), gWork, "force"},
		{"merge denied", payload("Bash", "gh pr merge 1", gWork), gWork, "merge"},
		{"plain command passes", payload("Bash", "go test ./...", gWork), gWork, ""},
		{"own branch push passes", payload("Bash", "git push origin HEAD", gWork), gWork, ""},
		{"missing cwd uses default", payload("Bash", "git reset --hard", ""), gWork, ""},
		{"missing cwd, default outside worktree", payload("Bash", "git reset --hard", ""), "/repo", "outside this worktree"},
		{"payload cwd beats default", payload("Bash", "git reset --hard", "/repo"), gWork, "outside this worktree"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := DecideGuard(GuardRequest{Payload: tc.payload, Slot: "dana", DefaultCwd: tc.cwd, Git: fakeGit})
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
