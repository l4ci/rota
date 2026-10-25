package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func branchGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", append([]string{"-c", "user.email=a@b", "-c", "user.name=n"}, args...)...)
	c.Dir = dir
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return string(out)
}

// branchRepo is a repo whose base is main, with a bare origin holding main.
func branchRepo(t *testing.T) (dir, origin string) {
	t.Helper()
	dir = gitRepo(t)
	origin, _ = filepath.EvalSymlinks(t.TempDir())
	branchGit(t, origin, "init", "-q", "--bare")
	branchGit(t, dir, "branch", "main")
	branchGit(t, dir, "remote", "add", "origin", origin)
	branchGit(t, dir, "push", "-q", "origin", "main")
	rotaIn(t, dir, "milestone", "add", "--title", "First", "--summary", "S")
	return dir, origin
}

func remoteHas(t *testing.T, origin, branch string) bool {
	t.Helper()
	return strings.TrimSpace(branchGit(t, origin, "branch", "--list", branch)) != ""
}

func milestoneStatusOf(t *testing.T, dir, id string) string {
	t.Helper()
	_, out, _ := rotaIn(t, dir, "milestone", "show", id)
	for _, l := range strings.Split(out, "\n") {
		if s, ok := strings.CutPrefix(l, "status: "); ok {
			return s
		}
	}
	return ""
}

func TestMilestoneActivateCutsBranchFromBase(t *testing.T) {
	dir, origin := branchRepo(t)
	if code, out, errOut := rotaIn(t, dir, "milestone", "status", "M01", "--to", "active"); code != 0 {
		t.Fatalf("activate: %d %s %s", code, out, errOut)
	}
	if !remoteHas(t, origin, "milestone/M01") {
		t.Fatal("milestone/M01 not on origin")
	}
	if got, want := strings.TrimSpace(branchGit(t, origin, "rev-parse", "milestone/M01")), strings.TrimSpace(branchGit(t, origin, "rev-parse", "main")); got != want {
		t.Errorf("branch at %s, base at %s", got, want)
	}
}

func TestMilestoneActivateReusesExistingBranch(t *testing.T) {
	dir, origin := branchRepo(t)
	branchGit(t, dir, "commit", "-q", "--allow-empty", "-m", "work")
	branchGit(t, dir, "push", "-q", "origin", "HEAD:refs/heads/milestone/M01")
	want := strings.TrimSpace(branchGit(t, origin, "rev-parse", "milestone/M01"))
	if code, out, errOut := rotaIn(t, dir, "milestone", "status", "M01", "--to", "active"); code != 0 {
		t.Fatalf("activate: %d %s %s", code, out, errOut)
	}
	if got := strings.TrimSpace(branchGit(t, origin, "rev-parse", "milestone/M01")); got != want {
		t.Errorf("branch moved from %s to %s", want, got)
	}
}

func TestMilestoneActivatePushFailureKeepsStatus(t *testing.T) {
	dir, origin := branchRepo(t)
	hook := filepath.Join(origin, "hooks", "pre-receive")
	os.WriteFile(hook, []byte("#!/bin/sh\necho protected >&2\nexit 1\n"), 0o755)
	code, _, errOut := rotaIn(t, dir, "milestone", "status", "M01", "--to", "active")
	if code == 0 {
		t.Fatal("activate succeeded despite a rejected push")
	}
	if !strings.Contains(errOut, "milestone/M01") {
		t.Errorf("error does not name the branch: %q", errOut)
	}
	if s := milestoneStatusOf(t, dir, "M01"); s != "planned" {
		t.Errorf("status %q, want planned", s)
	}
}

func TestMilestoneShippedDeletesBranch(t *testing.T) {
	dir, origin := branchRepo(t)
	rotaIn(t, dir, "milestone", "status", "M01", "--to", "active")
	if code, out, errOut := rotaIn(t, dir, "milestone", "status", "M01", "--to", "shipped", "--force"); code != 0 {
		t.Fatalf("ship: %d %s %s", code, out, errOut)
	}
	if remoteHas(t, origin, "milestone/M01") {
		t.Error("milestone/M01 still on origin")
	}
}

func TestMilestoneArchivedKeepRenamesAndUntagsItems(t *testing.T) {
	dir, origin := branchRepo(t)
	os.WriteFile(filepath.Join(dir, ".rota/BACKLOG.md"), []byte("# TODO\n\n## Bugs\n\n## Features\n\n## Tasks\n\n## Completed\n"), 0o644)
	rotaIn(t, dir, "item", "create", "--kind", "bugs", "--title", "Open one", "--tag", "P2", "--milestone", "M01")
	rotaIn(t, dir, "milestone", "status", "M01", "--to", "active")
	if code, _, _ := rotaIn(t, dir, "milestone", "status", "M01", "--to", "archived"); code != 2 {
		t.Fatalf("archive without --branch: exit %d, want 2", code)
	}
	if s := milestoneStatusOf(t, dir, "M01"); s != "active" {
		t.Fatalf("status %q after refused archive, want active", s)
	}
	if code, out, errOut := rotaIn(t, dir, "milestone", "status", "M01", "--to", "archived", "--branch", "keep"); code != 0 {
		t.Fatalf("archive keep: %d %s %s", code, out, errOut)
	}
	if remoteHas(t, origin, "milestone/M01") || !remoteHas(t, origin, "archive/milestone/M01") {
		t.Errorf("branches on origin: %s", branchGit(t, origin, "branch", "--list"))
	}
	if _, out, _ := rotaIn(t, dir, "backlog", "ids", "--milestone", "M01"); strings.TrimSpace(out) != "" {
		t.Errorf("items still tagged M01: %q", out)
	}
}

func TestMilestoneArchivedDeleteRemovesBranch(t *testing.T) {
	dir, origin := branchRepo(t)
	rotaIn(t, dir, "milestone", "status", "M01", "--to", "active")
	if code, out, errOut := rotaIn(t, dir, "milestone", "status", "M01", "--to", "archived", "--branch", "delete"); code != 0 {
		t.Fatalf("archive delete: %d %s %s", code, out, errOut)
	}
	if out := branchGit(t, origin, "branch", "--list", "*milestone/M01"); strings.TrimSpace(out) != "" {
		t.Errorf("branches left: %s", out)
	}
}

func TestMilestonePlannedFromActive(t *testing.T) {
	dir, origin := branchRepo(t)
	rotaIn(t, dir, "milestone", "status", "M01", "--to", "active")
	branchGit(t, dir, "commit", "-q", "--allow-empty", "-m", "milestone work")
	branchGit(t, dir, "push", "-q", "origin", "HEAD:refs/heads/milestone/M01")
	if code, _, _ := rotaIn(t, dir, "milestone", "status", "M01", "--to", "planned"); code != 4 {
		t.Fatalf("planned with commits: exit %d, want 4", code)
	}
	if s := milestoneStatusOf(t, dir, "M01"); s != "active" || !remoteHas(t, origin, "milestone/M01") {
		t.Fatalf("refusal changed state: status %q", s)
	}
	branchGit(t, dir, "push", "-q", "-f", "origin", "main:refs/heads/milestone/M01")
	if code, out, errOut := rotaIn(t, dir, "milestone", "status", "M01", "--to", "planned"); code != 0 {
		t.Fatalf("planned without commits: %d %s %s", code, out, errOut)
	}
	if remoteHas(t, origin, "milestone/M01") {
		t.Error("milestone/M01 still on origin")
	}
}
