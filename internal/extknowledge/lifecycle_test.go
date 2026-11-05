package extknowledge

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
)

// A separate process uses the public Clone interface against the same checkout,
// with no shared Go memory or state directory. It reports rather than fails on
// contention so the parent can verify the checkout and retry after release.
func TestCloneMutationSubprocess(t *testing.T) {
	dir := os.Getenv("ROTA_EXTKNOWLEDGE_TEST_CLONE")
	if dir == "" {
		t.Skip("subprocess helper")
	}
	res, err := (Clone{Dir: dir}).Add(context.Background(), AddIn{Topic: "Go", Title: "Inner", Body: "Keep this learning."})
	out := struct {
		Changed bool
		Pushed  bool
		Error   string
	}{Changed: res.Changed, Pushed: res.Pushed}
	if err != nil {
		out.Error = err.Error()
	}
	if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
		t.Fatal(err)
	}
}

func subprocessAdd(t *testing.T, dir string) (bool, bool, string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestCloneMutationSubprocess$")
	cmd.Env = append(os.Environ(), "ROTA_EXTKNOWLEDGE_TEST_CLONE="+dir)
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("child writer: %v", err)
	}
	var out struct {
		Changed bool
		Pushed  bool
		Error   string
	}
	if err := json.NewDecoder(strings.NewReader(string(raw))).Decode(&out); err != nil {
		t.Fatalf("child output %q: %v", raw, err)
	}
	return out.Changed, out.Pushed, out.Error
}

func TestCloneMutationOverlapPreservesLearnings(t *testing.T) {
	f := newFixture(t, "")
	// A path alias must still coordinate with the same checkout.
	alias := filepath.Join(filepath.Dir(f.a), "alias")
	if err := os.Symlink(f.a, alias); err != nil {
		t.Fatal(err)
	}
	paused := false
	f.hook = func(args []string) {
		if strings.Join(args, " ") != "rev-parse --abbrev-ref HEAD" || paused {
			return
		}
		paused = true // the outer Add has planned but has not written yet
		before := gittest.Run(t, f.a, "rev-parse", "HEAD")
		changed, _, err := subprocessAdd(t, alias)
		if err == "" || changed {
			t.Errorf("contending writer must refuse before mutation: changed=%v err=%q", changed, err)
		}
		if after := gittest.Run(t, f.a, "rev-parse", "HEAD"); after != before {
			t.Error("contention refusal changed HEAD")
		}
		if raw, _ := os.ReadFile(filepath.Join(f.a, "topics/go.md")); string(raw) != seedGo {
			t.Errorf("contention refusal changed topic: %s", raw)
		}
		if status := gittest.Run(t, f.a, "status", "--porcelain"); status != "" {
			t.Errorf("contention refusal dirtied checkout: %s", status)
		}
	}
	res, err := f.clone().Add(context.Background(), AddIn{Topic: "Go", Title: "Outer", Body: "Keep this too."})
	if err != nil || !res.Changed || !res.Pushed || !paused {
		t.Fatalf("outer writer: %+v, %v, paused=%v", res, err, paused)
	}
	changed, pushed, msg := subprocessAdd(t, alias)
	if msg != "" || !changed || !pushed {
		t.Fatalf("retry after release: changed=%v pushed=%v err=%q", changed, pushed, msg)
	}
	for _, dir := range []string{f.a, f.b} {
		if dir == f.b {
			gittest.Run(t, dir, "pull", "-q", "--ff-only")
		}
		raw, err := os.ReadFile(filepath.Join(dir, "topics/go.md"))
		if err != nil {
			t.Fatal(err)
		}
		for _, title := range []string{"Table tests", "Outer", "Inner"} {
			if !strings.Contains(string(raw), "**"+title+"**") {
				t.Errorf("%s lost successful learning %q: %s", dir, title, raw)
			}
		}
	}
}
