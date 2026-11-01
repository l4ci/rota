package host

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Paths handed to the host do not exist on disk. Only this stand-in translates
// them into an isolated backing directory; locks and atomic writes stay real.
type redirectedFS struct {
	OSFileSystem
	t                *testing.T
	logical, backing string
}

func (f redirectedFS) path(p string) string {
	f.t.Helper()
	rel, err := filepath.Rel(f.logical, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		f.t.Fatalf("file access outside stand-in: %q", p)
	}
	return filepath.Join(f.backing, rel)
}
func (f redirectedFS) unpath(p string) string {
	f.t.Helper()
	rel, err := filepath.Rel(f.backing, p)
	if err != nil {
		f.t.Fatal(err)
	}
	return filepath.Join(f.logical, rel)
}
func (f redirectedFS) ReadFile(p string) ([]byte, error) { return f.OSFileSystem.ReadFile(f.path(p)) }
func (f redirectedFS) WriteFile(p string, b []byte, mode os.FileMode) error {
	return f.OSFileSystem.WriteFile(f.path(p), b, mode)
}
func (f redirectedFS) Stat(p string) (os.FileInfo, error) { return f.OSFileSystem.Stat(f.path(p)) }
func (f redirectedFS) MkdirAll(p string, mode os.FileMode) error {
	return f.OSFileSystem.MkdirAll(f.path(p), mode)
}
func (f redirectedFS) CreateTemp(dir, pattern string) (string, error) {
	p, err := f.OSFileSystem.CreateTemp(f.path(dir), pattern)
	if err != nil {
		return "", err
	}
	return f.unpath(p), nil
}
func (f redirectedFS) Remove(p string) error { return f.OSFileSystem.Remove(f.path(p)) }
func (f redirectedFS) Glob(pattern string) ([]string, error) {
	paths, err := f.OSFileSystem.Glob(f.path(pattern))
	for i, p := range paths {
		paths[i] = f.unpath(p)
	}
	return paths, err
}
func (f redirectedFS) Locked(p string, wait time.Duration, fn func() error) error {
	return f.OSFileSystem.Locked(f.path(p), wait, fn)
}
func (f redirectedFS) WriteFileAtomic(p string, b []byte) error {
	return f.OSFileSystem.WriteFileAtomic(f.path(p), b)
}

func TestOrcaLifecycleWithInjectedFileSystem(t *testing.T) {
	for _, metadata := range []string{"directory", "relative gitdir", "absolute gitdir"} {
		t.Run(metadata, func(t *testing.T) {
			files := redirectedFS{t: t, logical: filepath.Join(t.TempDir(), "virtual"), backing: t.TempDir()}
			f := newOrcaFakeAt(filepath.Join(files.logical, "worker with 'quote'"))
			f.fs = files
			if err := files.MkdirAll(f.cwd, 0700); err != nil {
				t.Fatal(err)
			}
			gitdir := filepath.Join(f.cwd, ".git")
			if metadata == "directory" {
				if err := files.MkdirAll(gitdir, 0700); err != nil {
					t.Fatal(err)
				}
			} else {
				gitdir = filepath.Join(files.logical, "metadata")
				target := gitdir
				if metadata == "relative gitdir" {
					target = "../metadata"
				}
				if err := files.WriteFile(filepath.Join(f.cwd, ".git"), []byte("gitdir: "+target+"\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			f.pidDir = filepath.Join(gitdir, "rota-orca")
			// Exercise the actual adapter across instances, including discovery,
			// banner-free relays and a child surviving the terminal close.
			f.screens = []string{"user@host $", "model: loading\n› Ask Codex to do anything", orcaEmptyReady}
			h := New(Orca, f.deps())
			handle, err := h.Spawn(context.Background(), SpawnOpts{Session: "r", Slot: "ben", Cwd: f.cwd, Launch: "codex", Env: []string{"CODEX_HOME=/tmp/account with 'quote'", "ROTA_SLOT=ben"}, BootTimeout: 10})
			if err != nil {
				t.Fatalf("spawn with injected filesystem: %v", err)
			}
			if h.Kind() != Orca || handle == "" || f.reads != 3 {
				t.Fatalf("kind=%q handle=%q reads=%d", h.Kind(), handle, f.reads)
			}
			if !strings.Contains(f.command, "CODEX_HOME=") || !strings.Contains(f.command, "sh -c ") || !strings.Contains(f.command, "exec codex") {
				t.Fatalf("unsafe launch: %s", f.command)
			}
			pids, err := files.Glob(filepath.Join(gitdir, "rota-orca", "agent-*.pid"))
			if err != nil || len(pids) != 1 {
				t.Fatalf("PID files=%v error=%v", pids, err)
			}
			pid := pids[0]
			for suffix, want := range map[string]string{"": "123\n", ".json": handle, ".ready": "ready", ".tree": "[123,456]"} {
				b, err := files.ReadFile(pid + suffix)
				if err != nil || string(b) != want {
					t.Fatalf("proof %s = %q, %v; want %q", suffix, b, err, want)
				}
			}
			agents, err := New(Orca, f.deps()).Capabilities().Snapshotter.Snapshot(context.Background())
			if err != nil || len(agents) != 1 || agents[0].Handle() != handle {
				t.Fatalf("snapshot=%+v error=%v", agents, err)
			}
			text := "line one\n\n`cmd` $HOME 'quotes' ö\n"
			brief := filepath.Join(files.logical, "brief")
			if err := files.WriteFile(brief, []byte(text), 0600); err != nil {
				t.Fatal(err)
			}
			f.screens = []string{"Finished previous task.\n› "}
			if err := New(Orca, f.deps()).Send(context.Background(), "ben", handle, brief); err != nil {
				t.Fatal(err)
			}
			if f.sent != text || f.sends != 1 {
				t.Fatalf("sent %q %d times", f.sent, f.sends)
			}
			f.alive[456] = true
			if err := New(Orca, f.deps()).Kill(context.Background(), "ben", handle); err == nil || !strings.Contains(err.Error(), "456") {
				t.Fatalf("surviving child: %v", err)
			}
			if b, err := files.ReadFile(pid + ".json"); err != nil || string(b) != handle {
				t.Fatalf("failed close lost identity: %q %v", b, err)
			}
			if err := New(Orca, f.deps()).Kill(context.Background(), "ben", handle); err == nil {
				t.Fatal("retry lost orphan proof")
			}
			f.alive[456] = false
			if err := New(Orca, f.deps()).Kill(context.Background(), "ben", handle); err != nil {
				t.Fatal(err)
			}
			if _, err := files.Stat(pid + ".json"); !os.IsNotExist(err) {
				t.Fatalf("identity not removed: %v", err)
			}
			if err := New(Orca, f.deps()).Kill(context.Background(), "ben", handle); err != nil {
				t.Fatalf("idempotent close: %v", err)
			}
			if _, err := os.Stat(files.logical); !os.IsNotExist(err) {
				t.Fatalf("host bypassed injected filesystem: %v", err)
			}
		})
	}
}
