package spike

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/l4ci/rota/internal/artifact"
)

func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
}

func repo(t *testing.T) string {
	t.Helper()
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	run(t, dir, "init", "-q", "-b", "main")
	run(t, dir, "-c", "user.email=a@b", "-c", "user.name=n", "commit", "-q", "--allow-empty", "-m", "i")
	os.MkdirAll(filepath.Join(dir, ".rota"), 0o777)
	return dir
}

func exitOf(err error) int {
	var ae *artifact.Error
	if errors.As(err, &ae) {
		return ae.Exit
	}
	return -1
}

var date = regexp.MustCompile(`\d{4}-\d{2}-\d{2}`)

const wantAdded = `---
name: sse
branch: spike/sse
status: open
created: DATE
---

# spike/sse

## Question

Can SSE work?

## What was tried

_(commands run, files touched on the spike branch — fill in as you go)_

## Findings

_(3–5 bullets — what you learned)_

## Decision

_(viable / not viable / depends-on-X)_

## Recommended approach

_(if viable, the shape of the real implementation — write only at /rota-spike done)_
`

// Spike files are shared with older plugin versions; pin them byte for byte.
func TestSpikeFileBytes(t *testing.T) {
	root := repo(t)
	if _, err := Add(root, root, "sse", "Can SSE work?", ""); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(file(root, "sse"))
	if got := date.ReplaceAllString(string(raw), "DATE"); got != wantAdded {
		t.Fatalf("after add:\n%s", got)
	}
	if changed, err := Finish(root, "sse"); err != nil || !changed {
		t.Fatalf("finish: %v %v", changed, err)
	}
	raw, _ = os.ReadFile(file(root, "sse"))
	want := strings.Replace(wantAdded, "status: open\ncreated: DATE\n", "status: done\nfinished: DATE\ncreated: DATE\n", 1)
	if got := date.ReplaceAllString(string(raw), "DATE"); got != want {
		t.Fatalf("after finish:\n%s\nwant:\n%s", got, want)
	}
}

func TestFinishRewritesExistingFinishedLine(t *testing.T) {
	root := repo(t)
	os.MkdirAll(filepath.Dir(file(root, "x")), 0o777)
	os.WriteFile(file(root, "x"), []byte("---\nname: x\nstatus: open\nfinished: 2000-01-01\n---\nbody\n"), 0o644)
	if _, err := Finish(root, "x"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(file(root, "x"))
	if strings.Contains(string(raw), "2000-01-01") || strings.Count(string(raw), "finished:") != 1 {
		t.Fatalf("got:\n%s", raw)
	}
}

func TestFinishWithoutStatusIs70(t *testing.T) {
	root := repo(t)
	os.MkdirAll(filepath.Dir(file(root, "x")), 0o777)
	os.WriteFile(file(root, "x"), []byte("---\nname: x\n---\nbody\n"), 0o644)
	if _, err := Finish(root, "x"); exitOf(err) != 70 {
		t.Fatalf("got %v", err)
	}
	if _, err := Finish(root, "missing"); exitOf(err) != 3 {
		t.Fatalf("missing: %v", err)
	}
	if _, err := Finish(root, "Bad"); exitOf(err) != 2 {
		t.Fatalf("bad name: %v", err)
	}
}

func TestListSkipsFilesWithoutFrontmatter(t *testing.T) {
	root := repo(t)
	dir := filepath.Dir(file(root, "x"))
	os.MkdirAll(dir, 0o777)
	os.WriteFile(filepath.Join(dir, "plain.md"), []byte("# no frontmatter\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.md"), []byte("---\nstatus: open\n---\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "a.md"), []byte("---\nname: a\nbranch: spike/a\nrepo: web\ncreated: 2026-01-01\n---\n"), 0o644)
	list, err := List(root, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Name != "a" || list[0].Repo != "web" || list[0].Status != "open" || list[0].BranchExists {
		t.Fatalf("list = %+v", list)
	}
	// name and branch fall back to the file stem.
	if list[1].Name != "b" || list[1].Branch != "spike/b" || list[1].Created != "" {
		t.Fatalf("fallbacks = %+v", list[1])
	}
}

func umbrella(t *testing.T) (root, sub string) {
	t.Helper()
	root, _ = filepath.EvalSymlinks(t.TempDir())
	sub = filepath.Join(root, "web")
	os.MkdirAll(sub, 0o777)
	run(t, sub, "init", "-q", "-b", "main")
	run(t, sub, "-c", "user.email=a@b", "-c", "user.name=n", "commit", "-q", "--allow-empty", "-m", "i")
	os.MkdirAll(filepath.Join(root, ".rota"), 0o777)
	os.WriteFile(filepath.Join(root, ".rota", "repos.json"), []byte(`{"repos": [{"name": "web", "path": "web"}]}`), 0o644)
	return
}

func TestAddUmbrellaRepo(t *testing.T) {
	root, sub := umbrella(t)
	// From the umbrella root (not a git repo) with no repo: usage, naming the repos.
	_, err := Add(root, root, "sse", "q", "")
	var ae *artifact.Error
	if !errors.As(err, &ae) || ae.Exit != 2 || !strings.Contains(ae.Hint, "web") {
		t.Fatalf("no --repo: %v", err)
	}
	branch, err := Add(root, sub, "sse", "q", "web")
	if err != nil || branch != "spike/sse" {
		t.Fatalf("add: %q %v", branch, err)
	}
	raw, _ := os.ReadFile(file(root, "sse"))
	if !strings.Contains(string(raw), "branch: spike/sse\nrepo: web\nstatus: open\n") {
		t.Fatalf("repo line missing:\n%s", raw)
	}
	if _, err := os.Stat(filepath.Join(sub, ".rota")); err == nil {
		t.Error("spike file landed in the sub-repo")
	}
	list, _ := List(root, root)
	if len(list) != 1 || !list[0].BranchExists || list[0].Repo != "web" {
		t.Fatalf("list = %+v", list)
	}
}

func TestAddNotGit(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	os.MkdirAll(filepath.Join(dir, ".rota"), 0o777)
	if _, err := Add(dir, dir, "x", "q", ""); exitOf(err) != 5 {
		t.Fatalf("got %v", err)
	}
}

// Two concurrent adds of one name: exactly one succeeds, the other is refused.
func TestAddConcurrent(t *testing.T) {
	root := repo(t)
	var ok, refused atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := Add(root, root, "dup", "q", "")
			switch {
			case err == nil:
				ok.Add(1)
			case exitOf(err) == 4:
				refused.Add(1)
			default:
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if ok.Load() != 1 || refused.Load() != 7 {
		t.Fatalf("ok=%d refused=%d", ok.Load(), refused.Load())
	}
}

func TestCRLFSpikeFile(t *testing.T) {
	root := repo(t)
	os.MkdirAll(filepath.Dir(file(root, "x")), 0o777)
	os.WriteFile(file(root, "x"), []byte("---\r\nname: x\r\nstatus: open\r\n---\r\nbody\r\n"), 0o644)
	list, _ := List(root, root)
	if len(list) != 1 || list[0].Status != "open" {
		t.Fatalf("list = %+v", list)
	}
	if changed, err := Finish(root, "x"); err != nil || !changed {
		t.Fatalf("finish: %v %v", changed, err)
	}
	raw, _ := os.ReadFile(file(root, "x"))
	if !strings.HasPrefix(string(raw), "---\nname: x\nstatus: done\nfinished: ") || strings.Contains(string(raw), "\r") {
		t.Fatalf("got %q", raw)
	}
}
