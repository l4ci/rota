package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
)

// branchRepo is a repo whose base is main, with a bare origin holding main.
func branchRepo(t *testing.T) (dir, origin string) {
	t.Helper()
	dir = gitRepo(t)
	origin, _ = filepath.EvalSymlinks(t.TempDir())
	gittest.Run(t, origin, "init", "-q", "--bare")
	gittest.Quiet(t, origin)
	gittest.Run(t, dir, "branch", "main")
	gittest.Run(t, dir, "remote", "add", "origin", origin)
	gittest.Run(t, dir, "push", "-q", "origin", "main")
	rotaIn(t, dir, "milestone", "add", "--title", "First", "--summary", "S")
	return dir, origin
}

func remoteHas(t *testing.T, origin, branch string) bool {
	t.Helper()
	return strings.TrimSpace(gittest.Run(t, origin, "branch", "--list", branch)) != ""
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
	if got, want := strings.TrimSpace(gittest.Run(t, origin, "rev-parse", "milestone/M01")), strings.TrimSpace(gittest.Run(t, origin, "rev-parse", "main")); got != want {
		t.Errorf("branch at %s, base at %s", got, want)
	}
}

func TestMilestoneActivateReusesExistingBranch(t *testing.T) {
	dir, origin := branchRepo(t)
	gittest.Run(t, dir, "commit", "-q", "--allow-empty", "-m", "work")
	gittest.Run(t, dir, "push", "-q", "origin", "HEAD:refs/heads/milestone/M01")
	want := strings.TrimSpace(gittest.Run(t, origin, "rev-parse", "milestone/M01"))
	if code, out, errOut := rotaIn(t, dir, "milestone", "status", "M01", "--to", "active"); code != 0 {
		t.Fatalf("activate: %d %s %s", code, out, errOut)
	}
	if got := strings.TrimSpace(gittest.Run(t, origin, "rev-parse", "milestone/M01")); got != want {
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
		t.Errorf("branches on origin: %s", gittest.Run(t, origin, "branch", "--list"))
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
	if out := gittest.Run(t, origin, "branch", "--list", "*milestone/M01"); strings.TrimSpace(out) != "" {
		t.Errorf("branches left: %s", out)
	}
}

func TestMilestonePlannedFromActive(t *testing.T) {
	dir, origin := branchRepo(t)
	rotaIn(t, dir, "milestone", "status", "M01", "--to", "active")
	gittest.Run(t, dir, "commit", "-q", "--allow-empty", "-m", "milestone work")
	gittest.Run(t, dir, "push", "-q", "origin", "HEAD:refs/heads/milestone/M01")
	if code, _, _ := rotaIn(t, dir, "milestone", "status", "M01", "--to", "planned"); code != 4 {
		t.Fatalf("planned with commits: exit %d, want 4", code)
	}
	if s := milestoneStatusOf(t, dir, "M01"); s != "active" || !remoteHas(t, origin, "milestone/M01") {
		t.Fatalf("refusal changed state: status %q", s)
	}
	gittest.Run(t, dir, "push", "-q", "-f", "origin", "main:refs/heads/milestone/M01")
	if code, out, errOut := rotaIn(t, dir, "milestone", "status", "M01", "--to", "planned"); code != 0 {
		t.Fatalf("planned without commits: %d %s %s", code, out, errOut)
	}
	if remoteHas(t, origin, "milestone/M01") {
		t.Error("milestone/M01 still on origin")
	}
}

// behindRepo leaves origin/main one commit ahead of the local main.
func behindRepo(t *testing.T) (dir, origin string) {
	t.Helper()
	dir, origin = branchRepo(t)
	gittest.Run(t, dir, "commit", "-q", "--allow-empty", "-m", "merged upstream")
	gittest.Run(t, dir, "push", "-q", "origin", "HEAD:refs/heads/main")
	gittest.Run(t, dir, "fetch", "-q", "origin")
	return dir, origin
}

func TestMilestoneActivateCutsFromOriginBase(t *testing.T) {
	dir, origin := behindRepo(t)
	if code, out, errOut := rotaIn(t, dir, "milestone", "status", "M01", "--to", "active"); code != 0 {
		t.Fatalf("activate: %d %s %s", code, out, errOut)
	}
	if got, want := strings.TrimSpace(gittest.Run(t, origin, "rev-parse", "milestone/M01")), strings.TrimSpace(gittest.Run(t, origin, "rev-parse", "main")); got != want {
		t.Errorf("branch at %s, origin base at %s", got, want)
	}
}

func TestMilestonePlannedMeasuresAgainstOriginBase(t *testing.T) {
	dir, origin := branchRepo(t)
	rotaIn(t, dir, "milestone", "status", "M01", "--to", "active")
	gittest.Run(t, dir, "commit", "-q", "--allow-empty", "-m", "milestone work")
	gittest.Run(t, dir, "push", "-q", "origin", "HEAD:refs/heads/milestone/M01")
	// The work reached origin/main, while the local main stays behind.
	gittest.Run(t, dir, "push", "-q", "origin", "HEAD:refs/heads/main")
	gittest.Run(t, dir, "fetch", "-q", "origin")
	if code, out, errOut := rotaIn(t, dir, "milestone", "status", "M01", "--to", "planned"); code != 0 {
		t.Fatalf("planned: %d %s %s", code, out, errOut)
	}
	if remoteHas(t, origin, "milestone/M01") {
		t.Error("milestone/M01 still on origin")
	}
}

func TestMilestonePlannedRerunAfterFailedDelete(t *testing.T) {
	dir, origin := branchRepo(t)
	rotaIn(t, dir, "milestone", "status", "M01", "--to", "active")
	flag := filepath.Join(origin, "failed-once")
	hook := "#!/bin/sh\nif [ ! -e " + flag + " ]; then touch " + flag + "; echo deletes closed >&2; exit 1; fi\n"
	os.WriteFile(filepath.Join(origin, "hooks", "pre-receive"), []byte(hook), 0o755)
	if code, _, _ := rotaIn(t, dir, "milestone", "status", "M01", "--to", "planned"); code == 0 {
		t.Fatal("planned succeeded despite a rejected delete")
	}
	if s := milestoneStatusOf(t, dir, "M01"); s != "active" {
		t.Fatalf("status %q after failed delete, want active", s)
	}
	if code, out, errOut := rotaIn(t, dir, "milestone", "status", "M01", "--to", "planned"); code != 0 {
		t.Fatalf("rerun: %d %s %s", code, out, errOut)
	}
	if remoteHas(t, origin, "milestone/M01") {
		t.Error("milestone/M01 still on origin after rerun")
	}
}
