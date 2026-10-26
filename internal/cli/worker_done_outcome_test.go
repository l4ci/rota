package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSummary(t *testing.T, dir, body string) string {
	t.Helper()
	// Outside the repo: done refuses a dirty worktree.
	p := filepath.Join(t.TempDir(), "summary.txt")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func slotJSON(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, ".rota", "workers.json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

const threeLines = "Wired the flag.\nThe gate ignores it.\nGate wiring is left.\n"

func TestWorkerDoneOutcome(t *testing.T) {
	t.Run("partial records the outcome and the three lines", func(t *testing.T) {
		dir := doneProject(t, "")
		sum := writeSummary(t, dir, threeLines)
		code, out, _ := rotaIn(t, dir, "worker", "done", "ben", "--outcome", "partial", "--summary-file", sum, "--json")
		if code != 0 {
			t.Fatalf("exit %d: %s", code, out)
		}
		d := data(t, out)
		if d["outcome"] != "partial" {
			t.Errorf("data outcome = %v: %s", d["outcome"], out)
		}
		if block, _ := d["prBlock"].(string); !strings.Contains(block, "partial") || !strings.Contains(block, "Gate wiring is left.") {
			t.Errorf("prBlock = %q", block)
		}
		raw := slotJSON(t, dir)
		for _, want := range []string{`"outcome"`, "partial", "Wired the flag.", "Gate wiring is left."} {
			if !strings.Contains(raw, want) {
				t.Errorf("registry lacks %q: %s", want, raw)
			}
		}
	})
	t.Run("partial and failed need a summary", func(t *testing.T) {
		for _, o := range []string{"partial", "failed"} {
			dir := doneProject(t, "")
			if code, out, _ := rotaIn(t, dir, "worker", "done", "ben", "--outcome", o, "--json"); code != 2 {
				t.Fatalf("%s without summary: exit %d: %s", o, code, out)
			}
			if slotState(t, dir) == "done" {
				t.Errorf("%s: a refusal must not mark the slot done", o)
			}
		}
	})
	t.Run("an unknown outcome is a usage error", func(t *testing.T) {
		dir := doneProject(t, "")
		sum := writeSummary(t, dir, threeLines)
		if code, out, _ := rotaIn(t, dir, "worker", "done", "ben", "--outcome", "mostly", "--summary-file", sum, "--json"); code != 2 {
			t.Fatalf("exit %d: %s", code, out)
		}
	})
	t.Run("no outcome stays a plain done", func(t *testing.T) {
		dir := doneProject(t, "")
		if code, out, _ := rotaIn(t, dir, "worker", "done", "ben", "--json"); code != 0 {
			t.Fatalf("exit %d: %s", code, out)
		}
		if strings.Contains(slotJSON(t, dir), `"outcome"`) {
			t.Error("a plain done records no outcome")
		}
	})
	t.Run("a later done outcome replaces a partial one", func(t *testing.T) {
		dir := doneProject(t, "")
		sum := writeSummary(t, dir, threeLines)
		rotaIn(t, dir, "worker", "done", "ben", "--outcome", "partial", "--summary-file", sum)
		if code, out, _ := rotaIn(t, dir, "worker", "done", "ben", "--outcome", "done", "--summary-file", sum, "--json"); code != 0 {
			t.Fatalf("exit %d: %s", code, out)
		}
		if raw := slotJSON(t, dir); strings.Contains(raw, `"partial"`) {
			t.Errorf("partial outcome survived a done one: %s", raw)
		}
	})
	t.Run("a stale attempt is still fenced", func(t *testing.T) {
		dir := doneProject(t, "")
		os.WriteFile(filepath.Join(dir, ".rota", "workers.json"),
			[]byte(`{"slots":[{"name":"ben","branch":"feat/x","task":"B07","state":"busy","attempt":"cur"}]}`), 0o644)
		sum := writeSummary(t, dir, threeLines)
		code, _, _ := rotaIn(t, dir, "worker", "done", "ben", "--attempt", "old", "--outcome", "partial", "--summary-file", sum)
		if code != 4 || strings.Contains(slotJSON(t, dir), `"outcome"`) {
			t.Fatalf("exit %d: a fenced worker records nothing", code)
		}
	})
	t.Run("a plain done does not answer a standing partial", func(t *testing.T) {
		dir := doneProject(t, "")
		sum := writeSummary(t, dir, threeLines)
		rotaIn(t, dir, "worker", "done", "ben", "--outcome", "partial", "--summary-file", sum)
		if code, out, _ := rotaIn(t, dir, "worker", "done", "ben", "--json"); code != 0 {
			t.Fatalf("exit %d: %s", code, out)
		}
		if raw := slotJSON(t, dir); !strings.Contains(raw, `"partial"`) {
			t.Errorf("a plain done lifted the partial outcome: %s", raw)
		}
	})
	t.Run("a summary line is short and holds no separator", func(t *testing.T) {
		dir := doneProject(t, "")
		for _, body := range []string{"a | b\nc\nd\n", strings.Repeat("x", 201) + "\nb\nc\n"} {
			sum := writeSummary(t, dir, body)
			if code, out, _ := rotaIn(t, dir, "worker", "done", "ben", "--outcome", "partial", "--summary-file", sum, "--json"); code != 2 {
				t.Fatalf("exit %d: %s", code, out)
			}
		}
	})
	t.Run("failed needs no test.fast proof", func(t *testing.T) {
		dir := doneProject(t, "true")
		sum := writeSummary(t, dir, threeLines)
		if code, out, _ := rotaIn(t, dir, "worker", "done", "ben", "--outcome", "failed", "--summary-file", sum, "--json"); code != 0 {
			t.Fatalf("exit %d: %s", code, out)
		}
		dir = doneProject(t, "true")
		if code, out, _ := rotaIn(t, dir, "worker", "done", "ben", "--outcome", "partial", "--summary-file", sum, "--json"); code != 4 {
			t.Fatalf("partial still needs proof: exit %d: %s", code, out)
		}
	})
}
