package host

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/shlex"
)

// The executable boundary exercises argv and BOTH shells involved in Orca
// launch. The fixture runs no real Orca, agent, or process-killing command.
func TestOrcaFakeExecutableLifecycle(t *testing.T) {
	dir := t.TempDir()
	cwd := filepath.Join(t.TempDir(), "worktree with space 'quote'")
	if err := os.Mkdir(cwd, 0700); err != nil {
		t.Fatal(err)
	}
	metadata := filepath.Join(t.TempDir(), "git metadata")
	if err := os.Mkdir(metadata, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".git"), []byte("gitdir: "+metadata+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	envFile := filepath.Join(dir, "worker-env.json")
	briefFile := filepath.Join(dir, "submitted.txt")
	fake := `#!/usr/bin/env python3
import json, os, subprocess, sys
from pathlib import Path
a=sys.argv[1:]
def arg(k): return a[a.index(k)+1]
def emit(v): print(json.dumps(v))
state=Path(os.environ['ORCA_TEST_STATE'])
if a[0]=='status': emit({'running':True})
elif a[1]=='create':
 t={'id':'native-1','title':arg('--title'),'cwd':arg('--worktree')[5:]}
 state.write_text(json.dumps(t))
 subprocess.run(arg('--command'), shell=True, cwd=t['cwd'], check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
 emit({'terminal':t})
elif a[1]=='read': emit({'screen':'Ask Codex to do anything\n› '})
elif a[1]=='send':
 Path(os.environ['ORCA_TEST_BRIEF']).write_bytes(arg('--text').encode())
 emit({'receipt':{'turn_started':True}})
elif a[1]=='list': emit({'terminals':[json.loads(state.read_text())] if state.exists() else []})
elif a[1]=='close': state.unlink(); emit({})
elif a[1]=='current': emit({'terminal':json.loads(state.read_text())})
else: sys.exit(1)
`
	if err := os.WriteFile(filepath.Join(dir, "orca"), []byte(fake), 0700); err != nil {
		t.Fatal(err)
	}
	worker := filepath.Join(dir, "worker.py")
	pathJSON, _ := json.Marshal(envFile)
	script := "import os,json\nfrom pathlib import Path\nPath(" + string(pathJSON) + ").write_text(json.dumps({k:os.environ.get(k) for k in ['CODEX_HOME','ROTA_SLOT']}))\n"
	if err := os.WriteFile(worker, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("ORCA_TEST_STATE", filepath.Join(dir, "state.json"))
	t.Setenv("ORCA_TEST_BRIEF", briefFile)
	deps := Deps{Getenv: func(string) string { return "" }, Sleep: func(time.Duration) {}, Alive: func(int) bool { _, err := os.Stat(filepath.Join(dir, "state.json")); return err == nil }, Tree: func(p int) []int { return []int{p} }, KillWait: 1}
	h := New(Orca, deps)
	if e := h.Require(); e != nil {
		t.Fatal(e)
	}
	account := "/tmp/account with 'quotes' $HOME `literal` ö"
	handle, e := h.Spawn(context.Background(), SpawnOpts{Slot: "ben", Session: "round", Cwd: cwd, Launch: "python3 " + shlex.QuoteAlways(worker), Env: []string{"CODEX_HOME=" + account, "ROTA_SLOT=ben"}, BootTimeout: 2})
	if e != nil {
		t.Fatal(e)
	}
	meta, e := decodeOrca(handle)
	if e != nil || !strings.HasPrefix(meta.PIDFile, metadata+string(os.PathSeparator)) {
		t.Fatalf("proof outside worktree git metadata: %+v %v", meta, e)
	}
	b, e := os.ReadFile(envFile)
	if e != nil {
		t.Fatal(e)
	}
	var got map[string]string
	if e = json.Unmarshal(b, &got); e != nil {
		t.Fatal(e)
	}
	if got["CODEX_HOME"] != account || got["ROTA_SLOT"] != "ben" {
		t.Fatalf("shell changed env: %#v", got)
	}
	lines := make([]string, 65)
	for i := range lines {
		if i%13 != 0 {
			lines[i] = "line `quoted` $HOME 'ö' " + strings.Repeat("body ", 20)
		}
	}
	text := strings.Join(lines, "\n") + "\n"
	lines[1] += strings.Repeat("x", 8192-len(text))
	text = strings.Join(lines, "\n") + "\n"
	if len(text) != 8192 || strings.Count(text, "\n") != 65 {
		t.Fatalf("bad brief fixture: bytes=%d lines=%d", len(text), strings.Count(text, "\n"))
	}
	t.Logf("brief fixture: %d bytes, %d lines", len(text), strings.Count(text, "\n"))
	input := filepath.Join(dir, "brief")
	os.WriteFile(input, []byte(text), 0600)
	if e := New(Orca, deps).Send(context.Background(), "ben", handle, input); e != nil {
		t.Fatal(e)
	}
	b, e = os.ReadFile(briefFile)
	if e != nil || string(b) != text {
		t.Fatalf("brief bytes %q %v", b, e)
	}
	if e := New(Orca, deps).Kill(context.Background(), "ben", handle); e != nil {
		t.Fatal(e)
	}
	if e := New(Orca, deps).Kill(context.Background(), "ben", handle); e != nil {
		t.Fatalf("retry: %v", e)
	}
}
