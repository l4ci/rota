package fsio

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/golden"
	"github.com/l4ci/rota/internal/jsonx"
)

func counter(v any) int {
	o, ok := v.(*jsonx.Object)
	if !ok {
		return 0
	}
	n, _ := o.Get("n")
	num, ok := n.(json.Number)
	if !ok {
		return 0
	}
	i, _ := strconv.Atoi(string(num))
	return i
}

func bump(v any) (any, error) {
	o, ok := v.(*jsonx.Object)
	if !ok {
		o = jsonx.NewObject()
	}
	o.Set("n", counter(o)+1)
	return o, nil
}

// Concurrent writers each do a read-modify-write under the lock. Any lost
// update leaves the counter short, so the final value proves exclusion.
func TestConcurrentWritersLoseNoUpdates(t *testing.T) {
	// Locked polls with no fairness, so under the gate's CPU contention a writer
	// can outwait the 10 s LockTimeout. Exclusion is judged by the counter below,
	// not by how long writers queue, so give the queue a generous budget.
	const budget = 2 * time.Minute
	path := filepath.Join(t.TempDir(), "state.json")
	const writers, rounds = 8, 25
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range rounds {
				if err := updateJSON(path, nil, budget, bump); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if got := counter(LoadJSON(path, nil)); got != writers*rounds {
		t.Fatalf("counter = %d, want %d", got, writers*rounds)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("tmp file left behind")
	}
}

func TestLockTimeout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	held := make(chan struct{})
	release := make(chan struct{})
	go Locked(path, time.Second, func() error { close(held); <-release; return nil })
	<-held
	err := Locked(path, 120*time.Millisecond, func() error { return nil })
	close(release)
	if !errors.Is(err, ErrLockTimeout) {
		t.Fatalf("err = %v, want lock timeout", err)
	}
}

// A crashed writer leaves an empty .lock file behind; flock state lives in
// the kernel, so the next acquirer must proceed, not hang or fail.
func TestLockedIgnoresLeftoverLockFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path+".lock", nil, 0o644); err != nil {
		t.Fatal(err)
	}
	ran := false
	if err := Locked(path, 200*time.Millisecond, func() error { ran = true; return nil }); err != nil || !ran {
		t.Fatalf("leftover lockfile blocked Locked: ran=%v err=%v", ran, err)
	}
	if err := UpdateJSON(path, nil, bump); err != nil {
		t.Fatalf("UpdateJSON with leftover lockfile: %v", err)
	}
	if got := counter(LoadJSON(path, nil)); got != 1 {
		t.Fatalf("counter = %d, want 1", got)
	}
}

// The golden is what Python's dump_json_atomic wrote for the same document.
func TestWriteJSONAtomicMatchesPython(t *testing.T) {
	goPath := filepath.Join(t.TempDir(), "go.json")
	v, _ := jsonx.Decode([]byte(`{"b": [1, {"x": "é"}], "a": {}}`))
	if err := WriteJSONAtomic(goPath, v); err != nil {
		t.Fatal(err)
	}
	g, _ := os.ReadFile(goPath)
	golden.Check(t, map[string]any{}, string(g))
}

func TestLoadJSONDefaults(t *testing.T) {
	dir := t.TempDir()
	if LoadJSON(filepath.Join(dir, "missing.json"), "d") != "d" {
		t.Fatal("missing file must return default")
	}
	bad := filepath.Join(dir, "bad.json")
	os.WriteFile(bad, []byte("{not json"), 0o644)
	if LoadJSON(bad, "d") != "d" {
		t.Fatal("corrupt file must return default")
	}
}

func TestDirSyncUnsupportedIsNotAFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	orig := dirSync
	t.Cleanup(func() { dirSync = orig })
	for _, errno := range []syscall.Errno{syscall.EINVAL, syscall.ENOTSUP} {
		dirSync = func(*os.File) error { return &os.PathError{Op: "sync", Path: "dir", Err: errno} }
		if err := WriteFileAtomic(path, []byte("x")); err != nil {
			t.Errorf("%v from dir fsync must be ignored, got %v", errno, err)
		}
	}
	dirSync = func(*os.File) error { return &os.PathError{Op: "sync", Path: "dir", Err: syscall.EIO} }
	if err := WriteFileAtomic(path, []byte("y")); !errors.Is(err, syscall.EIO) {
		t.Errorf("EIO from dir fsync must be reported, got %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "y" {
		t.Errorf("the rename happened before the dir fsync; file = %q", got)
	}
}

func TestReadTextNormalizesNewlines(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f.md")
	os.WriteFile(p, []byte("a\r\nb\rc\nd\r\r\ne"), 0o666)
	got, err := ReadText(p)
	if err != nil || got != "a\nb\nc\nd\n\ne" {
		t.Errorf("got %q, %v", got, err)
	}
	if _, err := ReadText(filepath.Join(t.TempDir(), "none")); !os.IsNotExist(err) {
		t.Errorf("missing file: %v", err)
	}
}

func TestUpdateJSONStrictRefusesUnparseableFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(p, []byte(`{"slots": [trunc`), 0o644); err != nil {
		t.Fatal(err)
	}
	err := UpdateJSONStrict(p, jsonx.NewObject(), func(v any) (any, error) { return v, nil })
	if !errors.Is(err, ErrUnreadable) {
		t.Fatalf("err = %v, want ErrUnreadable", err)
	}
	if b, _ := os.ReadFile(p); string(b) != `{"slots": [trunc` {
		t.Errorf("the unreadable file was overwritten: %q", b)
	}
}

func TestUpdateJSONStrictStartsFromDefWhenMissing(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.json")
	err := UpdateJSONStrict(p, jsonx.NewObject(), func(v any) (any, error) {
		v.(*jsonx.Object).Set("k", "v")
		return v, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); !strings.Contains(string(b), `"k": "v"`) {
		t.Errorf("file = %q", b)
	}
}

func TestWriteJSONVerifiedReportsAWriteThatDidNotLand(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.json")
	orig := dirSync
	defer func() { dirSync = orig }()
	// the directory sync runs after the rename and before the re-read: a
	// writer that clobbers the file there makes the verified write fail.
	dirSync = func(d *os.File) error { return os.WriteFile(p, []byte("{}\n"), 0o644) }
	o := jsonx.NewObject()
	o.Set("k", "v")
	err := writeJSONVerified(p, o)
	if !errors.Is(err, ErrNotLanded) {
		t.Fatalf("err = %v, want ErrNotLanded", err)
	}
}
