package worker

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func verifyRoot(t *testing.T, cmds string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".rota"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := `{"refactor":{"verifyCommands":` + cmds + `}}`
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
