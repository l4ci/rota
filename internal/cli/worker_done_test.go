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
