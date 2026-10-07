package worker

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func verifyRoot(t *testing.T, cmds string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".rota"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := `{"test":{"full":` + cmds + `}}`
	if err := os.WriteFile(filepath.Join(root, ".rota", "config.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestVerifyReportsPassedFailedAndLog(t *testing.T) {
	root := verifyRoot(t, `["echo one"," ","false","echo two"]`)
	res, err := Env{}.Verify(context.Background(), root, root)
	if err != nil {
		t.Fatal(err)
	}
	if res.NoCommands || res.OK() || len(res.Verified) != 2 || len(res.Failed) != 1 || res.Failed[0] != "false" {
		t.Errorf("verified/failed wrong: %+v", res)
	}
	if res.LogPath == "" {
		t.Fatal("a failed run keeps its log")
	}
	defer os.Remove(res.LogPath)
	if b, _ := os.ReadFile(res.LogPath); string(b) != res.Log || res.Log != "== echo one\none\n== false\n== echo two\ntwo\n" {
		t.Errorf("log = %q, on disk %q", res.Log, b)
	}
}

func TestVerifyPassRemovesLogAndNoCommandsIsFlagged(t *testing.T) {
	root := verifyRoot(t, `["true"]`)
	res, err := Env{}.Verify(context.Background(), root, root)
	if err != nil || !res.OK() || res.LogPath != "" {
		t.Errorf("a pass leaves no log: %+v %v", res, err)
	}
	empty := verifyRoot(t, `[]`)
	res, _ = Env{}.Verify(context.Background(), empty, empty)
	if !res.NoCommands || HasVerifyCommands(empty) || !HasVerifyCommands(root) {
		t.Errorf("empty list must report NoCommands: %+v", res)
	}
}

// The runner every caller shares: a cancelled verify returns even though a
// grandchild still holds the output pipe (a killed sh would otherwise hang Wait).
func TestVerifyCancelReturnsWhileGrandchildHoldsPipe(t *testing.T) {
	root := verifyRoot(t, `["sleep 30; sleep 30"]`)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	res, err := Env{}.Verify(ctx, root, root)
	if err != nil || res.OK() || time.Since(start) > 10*time.Second {
		t.Errorf("cancel must stop the verify: %+v %v after %v", res, err, time.Since(start))
	}
	os.Remove(res.LogPath)
}

// The gate, the train and wind-down all run commands through Env.Shell.
func TestVerifyRunsThroughTheShellSeam(t *testing.T) {
	root := verifyRoot(t, `["a","b"]`)
	var seen []string
	e := Env{Shell: func(_ context.Context, dir, c string) (string, int) { seen = append(seen, dir+":"+c); return "", 0 }}
	if res, _ := e.Verify(context.Background(), root, "/work"); !res.OK() || len(seen) != 2 || seen[0] != "/work:a" {
		t.Errorf("Shell seam not used: %v %+v", seen, res)
	}
}

func tierRoot(t *testing.T, cfg string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".rota"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".rota", "config.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestTierCommandsReadsTestTier(t *testing.T) {
	root := tierRoot(t, `{"test":{"fast":["echo f"],"full":[" a ","","b"]}}`)
	if got := TierCommands(root, "full"); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("full: %v", got)
	}
	if got := TierCommands(root, "fast"); !reflect.DeepEqual(got, []string{"echo f"}) {
		t.Errorf("fast: %v", got)
	}
	if got := TierCommands(root, "e2e"); len(got) != 0 {
		t.Errorf("e2e: %v", got)
	}
}

func TestVerifyCommandsFallsBackToLegacyKey(t *testing.T) {
	root := tierRoot(t, `{"refactor":{"verifyCommands":["old"]}}`)
	if got := verifyCommandsAt(root); !reflect.DeepEqual(got, []string{"old"}) {
		t.Errorf("legacy: %v", got)
	}
	root = tierRoot(t, `{"test":{"full":["new"]},"refactor":{"verifyCommands":["old"]}}`)
	if got := verifyCommandsAt(root); !reflect.DeepEqual(got, []string{"new"}) {
		t.Errorf("test.full wins: %v", got)
	}
}

// captureStderr returns what fn wrote to os.Stderr.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = old }()
	fn()
	w.Close()
	b, _ := io.ReadAll(r)
	return string(b)
}

func TestVerifyCommandsAtWarnsOnceForLegacyKey(t *testing.T) {
	const warning = "refactor.verifyCommands is deprecated"
	legacy := t.TempDir()
	if err := os.MkdirAll(filepath.Join(legacy, ".rota"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := `{"refactor":{"verifyCommands":["echo old"]}}`
	if err := os.WriteFile(filepath.Join(legacy, ".rota", "config.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	legacyWarn = sync.Once{}
	var got []string
	out := captureStderr(t, func() {
		verifyCommandsAt(legacy)
		got = verifyCommandsAt(legacy)
	})
	if !reflect.DeepEqual(got, []string{"echo old"}) {
		t.Errorf("legacy commands = %v", got)
	}
	if n := strings.Count(out, warning); n != 1 {
		t.Errorf("warning printed %d times, want once: %q", n, out)
	}

	// test.full wins and stays silent, even when the legacy key is present too.
	cfg = `{"test":{"full":["echo new"]},"refactor":{"verifyCommands":["echo old"]}}`
	if err := os.WriteFile(filepath.Join(legacy, ".rota", "config.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	legacyWarn = sync.Once{}
	out = captureStderr(t, func() { got = verifyCommandsAt(legacy) })
	if !reflect.DeepEqual(got, []string{"echo new"}) || out != "" {
		t.Errorf("test.full set: commands %v, stderr %q", got, out)
	}
}
