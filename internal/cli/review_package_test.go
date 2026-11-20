package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
)

// reviewMergedBranch builds feat/m: one own commit (the --since point), a
// commit on main that is then merged into the branch, then one fix commit.
// It returns the repo and the since sha.
func reviewMergedBranch(t *testing.T) (repo, since string) {
	t.Helper()
	repo = newRepo(t, t.TempDir(), "repo", "main")
	gittest.Run(t, repo, "checkout", "-q", "-b", "feat/m")
	write(t, filepath.Join(repo, "own.txt"), "own")
	gittest.Run(t, repo, "add", "own.txt")
	gittest.Run(t, repo, "commit", "-q", "-m", "own work")
	since = strings.TrimSpace(gittest.Run(t, repo, "rev-parse", "HEAD"))
	gittest.Run(t, repo, "checkout", "-q", "main")
	write(t, filepath.Join(repo, "basefile.txt"), "from base")
	gittest.Run(t, repo, "add", "basefile.txt")
	gittest.Run(t, repo, "commit", "-q", "-m", "base moves on")
	gittest.Run(t, repo, "checkout", "-q", "feat/m")
	gittest.Run(t, repo, "merge", "-q", "--no-ff", "-m", "merge main", "main")
	write(t, filepath.Join(repo, "fix.txt"), "the fix")
	gittest.Run(t, repo, "add", "fix.txt")
	gittest.Run(t, repo, "commit", "-q", "-m", "fix review finding")
	return repo, since
}

// AC-1: a base merge inside since..head stays out of the package.
func TestReviewPackageSinceExcludesBaseMerge(t *testing.T) {
	repo, since := reviewMergedBranch(t)
	o := trRun(t, repo, "", "review", "package", "feat/m", "--since", since, "--json")
	if o.code != 0 {
		t.Fatalf("package: exit %d %s", o.code, o.stderr)
	}
	data := envelope(t, o.stdout)["data"].(map[string]any)
	body, err := os.ReadFile(data["path"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "fix.txt") {
		t.Errorf("package lacks the fix commit:\n%s", body)
	}
	if strings.Contains(string(body), "basefile.txt") {
		t.Errorf("package carries base content merged in:\n%s", body)
	}
	if n := data["files"].(float64); n != 1 {
		t.Errorf("files=%v, want 1", n)
	}
}

// AC-3: without --since the merge-base diff is unchanged.
func TestReviewPackageNoSinceKeepsMergeBaseDiff(t *testing.T) {
	repo, _ := reviewMergedBranch(t)
	o := trRun(t, repo, "", "review", "package", "feat/m", "--json")
	if o.code != 0 {
		t.Fatalf("package: exit %d %s", o.code, o.stderr)
	}
	body, err := os.ReadFile(envelope(t, o.stdout)["data"].(map[string]any)["path"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "own.txt") || !strings.Contains(string(body), "fix.txt") || strings.Contains(string(body), "basefile.txt") {
		t.Errorf("merge-base diff changed:\n%s", body)
	}
}

// reviewBehindRemote pushes feat/m to a bare origin, then adds a local commit,
// so the local head is one commit ahead of origin/feat/m. It returns the repo,
// the local head and the origin tip.
func reviewBehindRemote(t *testing.T) (repo, local, tip string) {
	t.Helper()
	repo, _ = reviewMergedBranch(t)
	bare := filepath.Join(t.TempDir(), "origin.git")
	gittest.Run(t, repo, "init", "-q", "--bare", bare)
	gittest.Run(t, repo, "remote", "add", "origin", bare)
	gittest.Run(t, repo, "push", "-q", "origin", "feat/m")
	tip = strings.TrimSpace(gittest.Run(t, repo, "rev-parse", "feat/m"))
	write(t, filepath.Join(repo, "unpushed.txt"), "local only")
	gittest.Run(t, repo, "add", "unpushed.txt")
	gittest.Run(t, repo, "commit", "-q", "-m", "unpushed fix")
	local = strings.TrimSpace(gittest.Run(t, repo, "rev-parse", "feat/m"))
	return repo, local, tip
}

// AC-2, AC-4: a local head that differs from origin/<branch> is refused before
// any package or workspace is built, naming both shas.
func TestReviewRefusesLocalHeadBehindOrigin(t *testing.T) {
	repo, local, tip := reviewBehindRemote(t)
	for name, args := range map[string][]string{
		"package": {"review", "package", "feat/m"},
		"prepare": {"review", "prepare", "feat/m"},
	} {
		t.Run(name, func(t *testing.T) {
			o := trRun(t, repo, "", args...)
			if o.code == 0 {
				t.Fatalf("want a refusal, got success: %s", o.stdout)
			}
			for _, want := range []string{local[:7], tip[:7], "push", "fetch"} {
				if !strings.Contains(o.stderr, want) {
					t.Errorf("refusal lacks %q: %s", want, o.stderr)
				}
			}
		})
	}
	// Once the branch is pushed, both heads agree and the verbs run.
	gittest.Run(t, repo, "push", "-q", "origin", "feat/m")
	if o := trRun(t, repo, "", "review", "package", "feat/m"); o.code != 0 {
		t.Fatalf("pushed head: exit %d %s", o.code, o.stderr)
	}
}

// A branch with no remote counterpart is unchanged.
func TestReviewPackageNoRemoteUnchanged(t *testing.T) {
	repo, _ := reviewMergedBranch(t)
	if o := trRun(t, repo, "", "review", "package", "feat/m"); o.code != 0 {
		t.Fatalf("no remote: exit %d %s", o.code, o.stderr)
	}
}

// A merge of a side branch (not the base) keeps its content in the package.
func TestReviewPackageSinceKeepsSideBranchMerge(t *testing.T) {
	repo, since := reviewMergedBranch(t)
	gittest.Run(t, repo, "checkout", "-q", "-b", "side", since)
	write(t, filepath.Join(repo, "side.txt"), "side work")
	gittest.Run(t, repo, "add", "side.txt")
	gittest.Run(t, repo, "commit", "-q", "-m", "side work")
	gittest.Run(t, repo, "checkout", "-q", "feat/m")
	gittest.Run(t, repo, "merge", "-q", "--no-ff", "-m", "merge side", "side")
	o := trRun(t, repo, "", "review", "package", "feat/m", "--since", since, "--json")
	if o.code != 0 {
		t.Fatalf("package: exit %d %s", o.code, o.stderr)
	}
	body, err := os.ReadFile(envelope(t, o.stdout)["data"].(map[string]any)["path"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "side.txt") {
		t.Errorf("side-branch merge hidden from the package:\n%s", body)
	}
}
