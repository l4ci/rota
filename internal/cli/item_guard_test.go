package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func createGuardProject(t *testing.T) string {
	t.Helper()
	root := trackerProject(t, "")
	bl := filepath.Join(root, ".rota", "BACKLOG.md")
	if err := os.WriteFile(bl, []byte("# TODO\n\n## Bugs\n\n## Tasks\n\n## Completed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func guardBatch(t *testing.T, recs string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "batch.json")
	if err := os.WriteFile(p, []byte(recs), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// filed counts the backlog lines whose title holds sub.
func filed(t *testing.T, root, sub string) int {
	t.Helper()
	b, _ := os.ReadFile(filepath.Join(root, ".rota", "BACKLOG.md"))
	return strings.Count(strings.ToLower(string(b)), strings.ToLower(sub))
}

// AC-1 and AC-3, single create.
func TestItemCreateRefusesRecentDuplicate(t *testing.T) {
	root := createGuardProject(t)
	first := []string{"--json", "-C", root, "item", "create", "--kind", "tasks", "--title", "Alpha seam extraction"}
	if code, _, e := rotaRun(t, first...); code != 0 {
		t.Fatalf("first: exit %d: %s", code, e)
	}
	// Same title in other case and spacing is the same title.
	again := []string{"--json", "-C", root, "item", "create", "--kind", "tasks", "--title", "alpha  seam extraction"}
	code, out, e := rotaRun(t, again...)
	if code != ExitRefused {
		t.Fatalf("repeat: exit %d, want %d: %s", code, ExitRefused, e)
	}
	if !strings.Contains(fmt.Sprint(out)+e, "T0") && !strings.Contains(fmt.Sprint(out)+e, "T1") {
		t.Errorf("refusal does not name the existing item: %v %s", out, e)
	}
	if n := filed(t, root, "alpha seam"); n != 1 {
		t.Errorf("repeat filed something: %d", n)
	}
	if code, _, e := rotaRun(t, append(again, "--force")...); code != 0 {
		t.Fatalf("--force: exit %d: %s", code, e)
	}
	if n := filed(t, root, "alpha seam"); n != 2 {
		t.Errorf("--force did not file: %d", n)
	}
}

// A title filed outside the window is fine again.
func TestItemCreateDuplicateWindowExpires(t *testing.T) {
	root := createGuardProject(t)
	args := []string{"--json", "-C", root, "item", "create", "--kind", "tasks", "--title", "Alpha seam extraction"}
	d := testDeps()
	d.Now = func() time.Time { return time.Date(2031, 1, 1, 10, 0, 0, 0, time.UTC) }
	if code, _, e := rotaRunWith(t, d, args...); code != 0 {
		t.Fatalf("first: exit %d: %s", code, e)
	}
	d.Now = func() time.Time { return time.Date(2031, 1, 1, 10, 11, 0, 0, time.UTC) }
	if code, _, e := rotaRunWith(t, d, args...); code != 0 {
		t.Fatalf("after window: exit %d: %s", code, e)
	}
}

// AC-1 and AC-3, batch: one recent duplicate refuses the whole batch.
func TestItemCreateBatchRefusesRecentDuplicate(t *testing.T) {
	root := createGuardProject(t)
	b1 := guardBatch(t, `[{"kind":"tasks","title":"Alpha seam extraction"}]`)
	if code, _, e := rotaRun(t, "--json", "-C", root, "item", "create", "--batch-file", b1); code != 0 {
		t.Fatalf("first: exit %d: %s", code, e)
	}
	b2 := guardBatch(t, `[{"kind":"tasks","title":"Beta crash on empty"},{"kind":"tasks","title":"Alpha seam extraction"}]`)
	code, out, e := rotaRun(t, "--json", "-C", root, "item", "create", "--batch-file", b2)
	if code != ExitRefused {
		t.Fatalf("exit %d, want %d: %s", code, ExitRefused, e)
	}
	if !strings.Contains(fmt.Sprint(out)+e, "Alpha seam extraction") {
		t.Errorf("refusal does not name the duplicate: %v %s", out, e)
	}
	if n := filed(t, root, "beta crash") + filed(t, root, "alpha seam"); n != 1 {
		t.Errorf("batch filed part of itself: %d", n)
	}
	if code, _, e := rotaRun(t, "--json", "-C", root, "item", "create", "--batch-file", b2, "--force"); code != 0 {
		t.Fatalf("--force: exit %d: %s", code, e)
	}
	if n := filed(t, root, "beta crash") + filed(t, root, "alpha seam"); n != 3 {
		t.Errorf("--force batch: %d", n)
	}
}

// AC-2 and AC-3: over the cap is refused before anything is filed.
func TestItemCreateBatchCap(t *testing.T) {
	root := createGuardProject(t)
	var recs []string
	for i := 0; i < createBatchCap+1; i++ {
		recs = append(recs, fmt.Sprintf(`{"kind":"tasks","title":"Gamma helper number %d"}`, i))
	}
	b := guardBatch(t, "["+strings.Join(recs, ",")+"]")
	code, _, e := rotaRun(t, "--json", "-C", root, "item", "create", "--batch-file", b)
	if code != ExitRefused {
		t.Fatalf("exit %d, want %d: %s", code, ExitRefused, e)
	}
	if n := filed(t, root, "gamma helper"); n != 0 {
		t.Errorf("over-cap batch filed %d", n)
	}
	if code, _, e := rotaRun(t, "--json", "-C", root, "item", "create", "--batch-file", b, "--force"); code != 0 {
		t.Fatalf("--force: exit %d: %s", code, e)
	}
	if n := filed(t, root, "gamma helper"); n != createBatchCap+1 {
		t.Errorf("--force filed %d", n)
	}
}
