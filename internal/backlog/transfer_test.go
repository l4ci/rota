package backlog

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/fsio"
)

const archivedNeighbor = "# Archive\n- ~~**[B09] [P2] neighbor.** keep~~ Done 2020-01-01 [`z`]\n"

func blockPath(t *testing.T, path string) {
	t.Helper()
	if err := os.Mkdir(path, 0755); err != nil {
		t.Fatal(err)
	}
}

func TestArchiveTransferFailures(t *testing.T) {
	for _, failure := range []string{"lock", "read", "destination write", "source write"} {
		t.Run(failure, func(t *testing.T) {
			f := fileBackend(t, completed)
			writeHV(t, f, "ARCHIVE.md", archivedNeighbor)
			path := f.archivePath()
			switch failure {
			case "lock":
				path += ".lock"
			case "read":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "destination write":
				path += ".tmp"
			case "source write":
				path = f.backlogPath() + ".tmp"
			}
			blockPath(t, path)
			if _, err := f.Archive(5, day(t, "2026-10-02")); err == nil {
				t.Fatal("expected injected failure")
			}
			if got := readFile(t, f, "BACKLOG.md"); got != completed {
				t.Errorf("failure removed source items: %q", got)
			}
			if failure == "source write" && strings.Count(readFile(t, f, "ARCHIVE.md"), "[B02]") != 1 {
				t.Fatal("destination was not persisted before source cleanup")
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if failure == "read" {
				writeHV(t, f, "ARCHIVE.md", archivedNeighbor)
			}
			// A fresh backend must recover using only the persisted documents.
			f = &File{Root: f.Root}
			if moved, err := f.Archive(5, day(t, "2026-10-02")); err != nil || moved != 1 {
				t.Fatalf("retry = %d, %v", moved, err)
			}
			archive := readFile(t, f, "ARCHIVE.md")
			for _, id := range []string{"B02", "B09"} {
				if n := strings.Count(archive, "["+id+"]"); n != 1 {
					t.Errorf("archive has %d copies of %s: %s", n, id, archive)
				}
			}
			if strings.Contains(readFile(t, f, "BACKLOG.md"), "[B02]") {
				t.Error("retry left source copy")
			}
		})
	}
}

func TestReopenTransferRetry(t *testing.T) {
	f := fileBackend(t, completed)
	if _, err := f.Archive(5, day(t, "2026-10-02")); err != nil {
		t.Fatal(err)
	}
	blockPath(t, f.archivePath()+".tmp")
	if _, err := f.Reopen("B02"); err == nil {
		t.Fatal("expected archive write failure")
	}
	if err := os.Remove(f.archivePath() + ".tmp"); err != nil {
		t.Fatal(err)
	}
	f = &File{Root: f.Root}
	if _, err := f.Reopen("B02"); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(readFile(t, f, "BACKLOG.md"), "[B02]"); n != 1 {
		t.Fatalf("reopen left %d backlog copies", n)
	}
	if strings.Contains(readFile(t, f, "ARCHIVE.md"), "[B02]") {
		t.Fatal("retry left archived copy of reopened item")
	}
}

func TestLifecycleWaitsForArchiveLock(t *testing.T) {
	for _, op := range []string{"archive", "reopen", "remove"} {
		t.Run(op, func(t *testing.T) {
			f := fileBackend(t, completed)
			writeHV(t, f, "ARCHIVE.md", archivedNeighbor)
			held, release, lockDone := make(chan struct{}), make(chan struct{}), make(chan error, 1)
			go func() {
				lockDone <- fsio.Locked(f.archivePath(), fsio.LockTimeout, func() error { close(held); <-release; return nil })
			}()
			<-held
			released := false
			defer func() {
				if !released {
					close(release)
				}
				if err := <-lockDone; err != nil {
					t.Error(err)
				}
			}()
			result := make(chan error, 1)
			go func() {
				var err error
				switch op {
				case "archive":
					_, err = f.Archive(5, day(t, "2026-10-02"))
				case "reopen":
					_, err = f.Reopen("B09")
				case "remove":
					_, err = f.Remove([]string{"B09"}, true, true)
				}
				result <- err
			}()
			// Wait until the operation owns BACKLOG, or returns without waiting for ARCHIVE.
			deadline := time.Now().Add(2 * time.Second)
			waiting := false
			for time.Now().Before(deadline) {
				select {
				case err := <-result:
					t.Fatalf("operation bypassed archive lock: %v", err)
				default:
				}
				err := fsio.Locked(f.backlogPath(), 0, func() error { return nil })
				if errors.Is(err, fsio.ErrLockTimeout) {
					waiting = true
					break
				}
				time.Sleep(time.Millisecond)
			}
			if !waiting {
				t.Fatal("operation never acquired backlog lock")
			}
			// Give the old implementation time to expose its early source rewrite.
			time.Sleep(75 * time.Millisecond)
			if got := readFile(t, f, "BACKLOG.md"); got != completed {
				t.Errorf("backlog changed before archive lock: %q", got)
			}
			if got := readFile(t, f, "ARCHIVE.md"); got != archivedNeighbor {
				t.Errorf("archive changed while its lock was held: %q", got)
			}
			// Simulate a concurrent ARCHIVE writer while holding its advisory lock.
			extra := "- ~~**[B10] [P2] concurrent.** keep~~ Done 2020-01-01 [`z`]\n"
			writeHV(t, f, "ARCHIVE.md", archivedNeighbor+extra)
			close(release)
			released = true
			if err := <-result; err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(readFile(t, f, "ARCHIVE.md"), extra) {
				t.Error("lost concurrent archive append")
			}
		})
	}
}

func TestInterruptedArchiveFollowup(t *testing.T) {
	for _, op := range []string{"reopen", "remove"} {
		t.Run(op, func(t *testing.T) {
			f := fileBackend(t, completed)
			writeHV(t, f, "ARCHIVE.md", archivedNeighbor)
			blockPath(t, f.backlogPath()+".tmp")
			if _, err := f.Archive(5, day(t, "2026-10-02")); err == nil {
				t.Fatal("expected source write failure")
			}
			if err := os.Remove(f.backlogPath() + ".tmp"); err != nil {
				t.Fatal(err)
			}
			switch op {
			case "reopen":
				if _, err := f.Reopen("B02"); err != nil {
					t.Fatal(err)
				}
			case "remove":
				if _, err := f.Remove([]string{"B02"}, true, true); err != nil {
					t.Fatal(err)
				}
			}
			if strings.Contains(readFile(t, f, "ARCHIVE.md"), "[B02]") {
				t.Fatal("left interrupted archive copy")
			}
			if !strings.Contains(readFile(t, f, "ARCHIVE.md"), "[B09]") {
				t.Fatal("lost unrelated archive entry")
			}
			if _, err := f.Archive(5, day(t, "2026-10-02")); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(readFile(t, f, "ARCHIVE.md"), "[B02]") {
				t.Fatal("archive retry resurrected entry")
			}
		})
	}
}

func TestLifecycleConcurrentOperations(t *testing.T) {
	f := fileBackend(t, completed)
	writeHV(t, f, "ARCHIVE.md", archivedNeighbor+"- ~~**[B10] [P2] remove.** x~~ Done 2020-01-01 [`z`]\n")
	start := make(chan struct{})
	results := make(chan error, 3)
	today := day(t, "2026-10-02")
	go func() { <-start; _, err := f.Archive(5, today); results <- err }()
	go func() { <-start; _, err := f.Reopen("B09"); results <- err }()
	go func() { <-start; _, err := f.Remove([]string{"B10"}, true, true); results <- err }()
	close(start)
	for range 3 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	archive, todo := readFile(t, f, "ARCHIVE.md"), readFile(t, f, "BACKLOG.md")
	if strings.Count(archive, "[B02]") != 1 || strings.Contains(todo, "[B02]") {
		t.Fatalf("archive transfer: %s\n%s", todo, archive)
	}
	if strings.Count(todo, "[B09]") != 1 || strings.Contains(archive, "[B09]") {
		t.Fatalf("reopen transfer: %s\n%s", todo, archive)
	}
	if strings.Contains(todo+archive, "[B10]") {
		t.Fatal("remove left a copy")
	}
	for _, id := range []string{"B01", "B03", "B04"} {
		if strings.Count(todo, "["+id+"]") != 1 {
			t.Errorf("lost unrelated %s", id)
		}
	}
}

func TestArchiveLockTimeoutPreservesSource(t *testing.T) {
	f := fileBackend(t, completed)
	if err := fsio.Locked(f.archivePath(), fsio.LockTimeout, func() error {
		_, err := f.Archive(5, day(t, "2026-10-02"))
		if !errors.Is(err, fsio.ErrLockTimeout) {
			t.Errorf("expected timeout, got %v", err)
		}
		if got := readFile(t, f, "BACKLOG.md"); got != completed {
			t.Error("timeout removed source")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if moved, err := f.Archive(5, day(t, "2026-10-02")); err != nil || moved != 1 {
		t.Fatalf("retry = %d, %v", moved, err)
	}
}

func TestLifecycleArchiveReadError(t *testing.T) {
	for _, op := range []string{"reopen", "remove"} {
		t.Run(op, func(t *testing.T) {
			f := fileBackend(t, completed)
			blockPath(t, f.archivePath())
			var err error
			if op == "reopen" {
				_, err = f.Reopen("B02")
			} else {
				_, err = f.Remove([]string{"B02"}, true, true)
			}
			if err == nil {
				t.Fatal("expected archive read failure")
			}
			if readFile(t, f, "BACKLOG.md") != completed {
				t.Fatal("read failure changed backlog")
			}
		})
	}
}
