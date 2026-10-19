package worker

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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
	res, err := Env{}.Verify(context.Background(), LoadGateConfig(root), root, root)
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
	res, err := Env{}.Verify(context.Background(), LoadGateConfig(root), root, root)
	if err != nil || !res.OK() || res.LogPath != "" {
		t.Errorf("a pass leaves no log: %+v %v", res, err)
	}
	empty := verifyRoot(t, `[]`)
	res, _ = Env{}.Verify(context.Background(), LoadGateConfig(empty), empty, empty)
	if !res.NoCommands || LoadGateConfig(empty).HasVerifyCommands() || !LoadGateConfig(root).HasVerifyCommands() {
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
	res, err := Env{}.Verify(ctx, LoadGateConfig(root), root, root)
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
	if res, _ := e.Verify(context.Background(), LoadGateConfig(root), root, "/work"); !res.OK() || len(seen) != 2 || seen[0] != "/work:a" {
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
	if got := LoadGateConfig(root).Tier("full"); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("full: %v", got)
	}
	if got := LoadGateConfig(root).Tier("fast"); !reflect.DeepEqual(got, []string{"echo f"}) {
		t.Errorf("fast: %v", got)
	}
	if got := LoadGateConfig(root).Tier("e2e"); len(got) != 0 {
		t.Errorf("e2e: %v", got)
	}
}

func TestVerifyCommandsFallsBackToLegacyKey(t *testing.T) {
	root := tierRoot(t, `{"refactor":{"verifyCommands":["old"]}}`)
	if got := LoadGateConfig(root).Full; !reflect.DeepEqual(got, []string{"old"}) {
		t.Errorf("legacy: %v", got)
	}
	root = tierRoot(t, `{"test":{"full":["new"]},"refactor":{"verifyCommands":["old"]}}`)
	if got := LoadGateConfig(root).Full; !reflect.DeepEqual(got, []string{"new"}) {
		t.Errorf("test.full wins: %v", got)
	}
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
	var buf strings.Builder
	gc := LoadGateConfig(legacy)
	gc.WarnLegacy(&buf)
	got := gc.Tier("full")
	gc.WarnLegacy(&buf)
	out := buf.String()
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
	buf.Reset()
	gc = LoadGateConfig(legacy)
	gc.WarnLegacy(&buf)
	got, out = gc.Tier("full"), buf.String()
	if !reflect.DeepEqual(got, []string{"echo new"}) || out != "" {
		t.Errorf("test.full set: commands %v, stderr %q", got, out)
	}
}

// Env.Verify writes the legacy-key warning to Env.Stderr, once however often
// the same loaded config verifies.
func TestVerifyWarnsThroughEnvStderr(t *testing.T) {
	root := tierRoot(t, `{"refactor":{"verifyCommands":["echo old"]}}`)
	var buf strings.Builder
	e := Env{Stderr: &buf, Shell: func(context.Context, string, string) (string, int) { return "", 0 }}
	cfg := LoadGateConfig(root)
	for i := 0; i < 2; i++ {
		if _, err := e.Verify(context.Background(), cfg, root, root); err != nil {
			t.Fatal(err)
		}
	}
	if n := strings.Count(buf.String(), "refactor.verifyCommands is deprecated"); n != 1 {
		t.Errorf("warning printed %d times, want once: %q", n, buf.String())
	}
}
