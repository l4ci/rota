package worker

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

// gateCfg is the gate config of an in-memory project config, read through
// Env.Config like every worker verb.
func gateCfg(t *testing.T, raw string) GateConfig {
	t.Helper()
	return Env{Config: memConfig(t, raw)}.GateConfig("")
}

func verifyCfg(t *testing.T, cmds string) GateConfig {
	t.Helper()
	return gateCfg(t, `{"test":{"full":`+cmds+`}}`)
}

func TestVerifyReportsPassedFailedAndLog(t *testing.T) {
	root := t.TempDir()
	res, err := Env{}.Verify(context.Background(), verifyCfg(t, `["echo one"," ","false","echo two"]`), root, root)
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
	root := t.TempDir()
	tmp := t.TempDir()
	e := Env{
		Getenv: func(key string) string {
			if key == "TMPDIR" {
				return tmp
			}
			return ""
		},
		Shell: func(context.Context, string, string) (string, int) {
			files, err := os.ReadDir(tmp)
			if err != nil || len(files) != 1 {
				t.Errorf("during verify: temp files = %v, err = %v; want one log in injected TMPDIR", files, err)
			}
			return "", 0
		},
	}
	full := verifyCfg(t, `["true"]`)
	res, err := e.Verify(context.Background(), full, root, root)
	if err != nil || !res.OK() || res.LogPath != "" {
		t.Errorf("a pass leaves no log: %+v %v", res, err)
	}
	if files, err := os.ReadDir(tmp); err != nil || len(files) != 0 {
		t.Errorf("after verify: temp files = %v, err = %v; want none", files, err)
	}
	none := verifyCfg(t, `[]`)
	res, _ = e.Verify(context.Background(), none, root, root)
	if !res.NoCommands || none.HasVerifyCommands() || !full.HasVerifyCommands() {
		t.Errorf("empty list must report NoCommands: %+v", res)
	}
}

// The runner every caller shares: a cancelled verify returns even though a
// grandchild still holds the output pipe (a killed sh would otherwise hang Wait).
func TestVerifyCancelReturnsWhileGrandchildHoldsPipe(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	res, err := Env{}.Verify(ctx, verifyCfg(t, `["sleep 30; sleep 30"]`), root, root)
	if err != nil || res.OK() || time.Since(start) > 10*time.Second {
		t.Errorf("cancel must stop the verify: %+v %v after %v", res, err, time.Since(start))
	}
	os.Remove(res.LogPath)
}

// The gate, the train and wind-down all run commands through Env.Shell.
func TestVerifyRunsThroughTheShellSeam(t *testing.T) {
	root := t.TempDir()
	var seen []string
	e := Env{Shell: func(_ context.Context, dir, c string) (string, int) { seen = append(seen, dir+":"+c); return "", 0 }}
	if res, _ := e.Verify(context.Background(), verifyCfg(t, `["a","b"]`), root, "/work"); !res.OK() || len(seen) != 2 || seen[0] != "/work:a" {
		t.Errorf("Shell seam not used: %v %+v", seen, res)
	}
}

func TestTierCommandsReadsTestTier(t *testing.T) {
	gc := gateCfg(t, `{"test":{"fast":["echo f"],"full":[" a ","","b"]}}`)
	if got := gc.Tier("full"); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("full: %v", got)
	}
	if got := gc.Tier("fast"); !reflect.DeepEqual(got, []string{"echo f"}) {
		t.Errorf("fast: %v", got)
	}
	if got := gc.Tier("e2e"); len(got) != 0 {
		t.Errorf("e2e: %v", got)
	}
}

func TestVerifyCommandsFallsBackToLegacyKey(t *testing.T) {
	if got := gateCfg(t, `{"refactor":{"verifyCommands":["old"]}}`).Full; !reflect.DeepEqual(got, []string{"old"}) {
		t.Errorf("legacy: %v", got)
	}
	if got := gateCfg(t, `{"test":{"full":["new"]},"refactor":{"verifyCommands":["old"]}}`).Full; !reflect.DeepEqual(got, []string{"new"}) {
		t.Errorf("test.full wins: %v", got)
	}
}

func TestVerifyCommandsAtWarnsOnceForLegacyKey(t *testing.T) {
	const warning = "refactor.verifyCommands is deprecated"
	var buf strings.Builder
	gc := gateCfg(t, `{"refactor":{"verifyCommands":["echo old"]}}`)
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
	buf.Reset()
	gc = gateCfg(t, `{"test":{"full":["echo new"]},"refactor":{"verifyCommands":["echo old"]}}`)
	gc.WarnLegacy(&buf)
	got, out = gc.Tier("full"), buf.String()
	if !reflect.DeepEqual(got, []string{"echo new"}) || out != "" {
		t.Errorf("test.full set: commands %v, stderr %q", got, out)
	}
}

// Env.Verify writes the legacy-key warning to Env.Stderr, once however often
// the same loaded config verifies.
func TestVerifyWarnsThroughEnvStderr(t *testing.T) {
	root := t.TempDir()
	var buf strings.Builder
	e := Env{Stderr: &buf, Shell: func(context.Context, string, string) (string, int) { return "", 0 }}
	cfg := gateCfg(t, `{"refactor":{"verifyCommands":["echo old"]}}`)
	for i := 0; i < 2; i++ {
		if _, err := e.Verify(context.Background(), cfg, root, root); err != nil {
			t.Fatal(err)
		}
	}
	if n := strings.Count(buf.String(), "refactor.verifyCommands is deprecated"); n != 1 {
		t.Errorf("warning printed %d times, want once: %q", n, buf.String())
	}
}
