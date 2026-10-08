package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// doneProject is a backlog project whose slot "ben" holds B07 on branch feat/x.
func doneProject(t *testing.T, fast string) string {
	dir := backlogProject(t)
	cfg := `{}`
	if fast != "" {
		cfg = `{"test":{"fast":["` + fast + `"]}}`
	}
	os.WriteFile(filepath.Join(dir, ".rota", "config.json"), []byte(cfg), 0o644)
	os.WriteFile(filepath.Join(dir, ".rota", "workers.json"),
		[]byte(`{"slots":[{"name":"ben","branch":"feat/x","task":"B07","state":"busy"}]}`), 0o644)
	return dir
}

func headOf(t *testing.T, dir string) string {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func slotState(t *testing.T, dir string) string {
	b, _ := os.ReadFile(filepath.Join(dir, ".rota", "workers.json"))
	if strings.Contains(string(b), `"state": "done"`) || strings.Contains(string(b), `"state":"done"`) {
		return "done"
	}
	return "busy"
}

func TestWorkerDone(t *testing.T) {
	t.Run("refuses without a row", func(t *testing.T) {
		dir := doneProject(t, "true")
		code, out, _ := rotaIn(t, dir, "worker", "done", "ben", "--json")
		d := data(t, out)
		if code != 4 || d["changed"] != false || d["missing"] == nil {
			t.Fatalf("exit %d: %s", code, out)
		}
		if !strings.Contains(out, "rota proof record") {
			t.Errorf("hint must name rota proof record: %s", out)
		}
		if slotState(t, dir) == "done" {
			t.Error("a refusal must not mark the slot done")
		}
	})
	t.Run("a FAIL row does not count", func(t *testing.T) {
		dir := doneProject(t, "true")
		rotaIn(t, dir, "proof", "add", "B07", "--check", "true", "--result", "FAIL", "--evidence", "x", "--sha", headOf(t, dir))
		if code, out, _ := rotaIn(t, dir, "worker", "done", "ben", "--json"); code != 4 {
			t.Fatalf("exit %d: %s", code, out)
		}
	})
	t.Run("a PASS row at an older sha does not count", func(t *testing.T) {
		dir := doneProject(t, "true")
		if code, out, _ := rotaIn(t, dir, "proof", "record", "B07", "--", "true"); code != 0 {
			t.Fatalf("record: %d %s", code, out)
		}
		c := exec.Command("git", "-c", "user.email=a@b", "-c", "user.name=n", "commit", "-q", "--allow-empty", "-m", "more")
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("%v %s", err, out)
		}
		if code, out, _ := rotaIn(t, dir, "worker", "done", "ben", "--json"); code != 4 {
			t.Fatalf("stale sha: exit %d: %s", code, out)
		}
	})
	t.Run("accepts a PASS row at HEAD", func(t *testing.T) {
		dir := doneProject(t, "true")
		if code, out, _ := rotaIn(t, dir, "proof", "record", "B07", "--", "true"); code != 0 {
			t.Fatalf("record: %d %s", code, out)
		}
		code, out, _ := rotaIn(t, dir, "worker", "done", "ben", "--json")
		if code != 0 || data(t, out)["changed"] != true {
			t.Fatalf("exit %d: %s", code, out)
		}
		if slotState(t, dir) != "done" {
			t.Error("slot not marked done")
		}
	})
	t.Run("accepts the whole-tier row", func(t *testing.T) {
		dir := doneProject(t, "true")
		rotaIn(t, dir, "proof", "add", "B07", "--check", "rota test run fast", "--result", "PASS", "--evidence", "x", "--sha", headOf(t, dir))
		if code, out, _ := rotaIn(t, dir, "worker", "done", "ben", "--json"); code != 0 {
			t.Fatalf("exit %d: %s", code, out)
		}
	})
	t.Run("accepts the whole-tier row recorded multi-arg", func(t *testing.T) {
		dir := doneProject(t, "true")
		rotaIn(t, dir, "proof", "add", "B07", "--check", "'rota' 'test' 'run' 'fast'", "--result", "PASS", "--evidence", "x", "--sha", headOf(t, dir))
		if code, out, _ := rotaIn(t, dir, "worker", "done", "ben", "--json"); code != 0 {
			t.Fatalf("exit %d: %s", code, out)
		}
	})
	t.Run("unset test.fast skips the proof half with a warning", func(t *testing.T) {
		dir := doneProject(t, "")
		code, out, errOut := rotaIn(t, dir, "worker", "done", "ben", "--json")
		if code != 0 || data(t, out)["proofSkipped"] != true || !strings.Contains(errOut+out, "test.fast is unset") {
			t.Fatalf("exit %d: %s %s", code, out, errOut)
		}
		if slotState(t, dir) != "done" {
			t.Error("slot not marked done")
		}
	})
	t.Run("unknown slot is 3", func(t *testing.T) {
		dir := doneProject(t, "true")
		if code, _, _ := rotaIn(t, dir, "worker", "done", "nope"); code != 3 {
			t.Errorf("exit %d, want 3", code)
		}
	})
}

// TestWorkerDoneBaselinesReviewCursor: the contract runs `rota worker done`
// before the PR is opened, so the poll never sees the edge; the verb itself
// must start the review cursor or every earlier comment counts as review input.
func TestWorkerDoneBaselinesReviewCursor(t *testing.T) {
	dir := doneProject(t, "")
	if code, out, _ := rotaIn(t, dir, "worker", "done", "ben", "--json"); code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	b, _ := os.ReadFile(filepath.Join(dir, ".rota", "workers.json"))
	if !strings.Contains(string(b), "reviewSeen") {
		t.Fatalf("review cursor not baselined: %s", b)
	}
}

// TestWorkerDoneFromWorktree: the registry lives in the main checkout, and
// {files} is the slot branch's diff, from whichever directory the verb runs.
func TestWorkerDoneFromWorktree(t *testing.T) {
	main := backlogProject(t)
	os.WriteFile(filepath.Join(main, ".rota", "config.json"), []byte(`{"test":{"fast":["true {files}"]}}`), 0o644)
	// Proof detail files are untracked noise; ignore them so {files} is stable.
	os.WriteFile(filepath.Join(main, ".gitignore"), []byte(".rota/*\n!.rota/config.json\n!.rota/BACKLOG.md\n"), 0o644)
	gitT(t, main, "add", ".gitignore", ".rota/config.json", ".rota/BACKLOG.md")
	gitT(t, main, "-c", "user.email=a@b", "-c", "user.name=n", "commit", "-q", "-m", "rota")
	gitT(t, main, "branch", "base0")
	wt := filepath.Join(main, ".worktrees", "ben")
	gitT(t, main, "worktree", "add", "-q", "-b", "ben/x", wt, "base0")
	os.WriteFile(filepath.Join(wt, "a.go"), []byte("package a\n"), 0o644)
	gitT(t, wt, "add", "a.go")
	gitT(t, wt, "-c", "user.email=a@b", "-c", "user.name=n", "commit", "-q", "-m", "work")
	os.WriteFile(filepath.Join(main, ".rota", "workers.json"),
		[]byte(`{"slots":[{"name":"ben","branch":"ben/x","task":"B07","state":"busy","worktree":"`+wt+`"}]}`), 0o644)

	if code, out, errOut := rotaIn(t, wt, "proof", "record", "B07", "--base", "base0", "--", "true {files}"); code != 0 {
		t.Fatalf("record: %d %s %s", code, out, errOut)
	}
	// The same row, as the main checkout's proof store sees it.
	sha := headOf(t, wt)
	if code, out, _ := rotaIn(t, main, "proof", "add", "B07", "--check", "true 'a.go'", "--result", "PASS", "--evidence", "x", "--sha", sha); code != 0 {
		t.Fatalf("add: %d %s", code, out)
	}

	t.Run("from the slot worktree", func(t *testing.T) {
		code, out, errOut := rotaIn(t, wt, "worker", "done", "ben", "--base", "base0", "--json")
		if code != 0 || data(t, out)["changed"] != true {
			t.Fatalf("exit %d: %s %s", code, out, errOut)
		}
	})
	// Reset the slot, then dirty the main checkout with an unrelated file.
	os.WriteFile(filepath.Join(main, ".rota", "workers.json"),
		[]byte(`{"slots":[{"name":"ben","branch":"ben/x","task":"B07","state":"busy","worktree":"`+wt+`"}]}`), 0o644)
	os.WriteFile(filepath.Join(main, "unrelated.txt"), []byte("x"), 0o644)
	t.Run("from the main checkout with a dirty file", func(t *testing.T) {
		code, out, errOut := rotaIn(t, main, "worker", "done", "ben", "--base", "base0", "--json")
		if code != 0 || data(t, out)["changed"] != true {
			t.Fatalf("exit %d: %s %s", code, out, errOut)
		}
	})
}
