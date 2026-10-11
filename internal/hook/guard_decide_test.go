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
		deny    bool
	}{
		{"bad json passes", []byte("{"), gWork, false},
		{"non-bash tool passes", payload("Edit", "git push --force origin main", gWork), gWork, false},
		{"empty command passes", payload("Bash", "", gWork), gWork, false},
		{"force push denied", payload("Bash", "git push --force origin main", gWork), gWork, true},
		{"merge denied", payload("Bash", "gh pr merge 1", gWork), gWork, true},
		{"plain command passes", payload("Bash", "go test ./...", gWork), gWork, false},
		{"own branch push passes", payload("Bash", "git push origin HEAD", gWork), gWork, false},
		{"missing cwd uses default", payload("Bash", "git push --force origin main", ""), gWork, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := DecideGuard(GuardRequest{Payload: tc.payload, Slot: "dana", DefaultCwd: tc.cwd, Git: fakeGit})
			if (got != "") != tc.deny {
				t.Fatalf("reason = %q, deny want %v", got, tc.deny)
			}
		})
	}
}

func TestSlotWorktree(t *testing.T) {
	git := func(string, ...string) (string, error) { return "", errors.New("no git") }
	if got := SlotWorktree(gWork+"/internal", "dana", git); got != gWork {
		t.Errorf("ancestor: got %q", got)
	}
	if got := SlotWorktree("/elsewhere", "dana", git); got != "" {
		t.Errorf("git failure: got %q", got)
	}
	if got := SlotWorktree("/elsewhere", "dana", fakeGit); got != gWork {
		t.Errorf("worktree list: got %q", got)
	}
}
