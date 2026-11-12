package fsio

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func decodeOK(got *[]string) func([]byte) error {
	return func(l []byte) error {
		if string(l) == "bad" || string(l) == `{"torn":` {
			return errors.New("rejected")
		}
		*got = append(*got, string(l))
		return nil
	}
}

func TestAppendLineModeAndDurability(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.jsonl")
	for _, l := range []string{"a", "b"} {
		if err := AppendLine(p, []byte(l), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(p)
	if string(b) != "a\nb\n" {
		t.Fatalf("file = %q", b)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %v", fi.Mode().Perm())
	}
}

func TestAppendLineConcurrentLinesStayWhole(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.jsonl")
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := AppendLine(p, []byte("0123456789"), 0o644); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	var got []string
	skipped, err := ReadLines(p, Strict, decodeOK(&got))
	if err != nil || skipped != 0 || len(got) != 20 {
		t.Fatalf("got %d lines skipped=%d err=%v", len(got), skipped, err)
	}
}

func TestReadLinesTornLastLineUnderBothPolicies(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.jsonl")
	if err := os.WriteFile(p, []byte("one\n\n  \ntwo\n{\"torn\":"), 0o644); err != nil {
		t.Fatal(err)
	}
	var got []string
	skipped, err := ReadLines(p, Lenient, decodeOK(&got))
	if err != nil || skipped != 1 || len(got) != 2 || got[0] != "one" || got[1] != "two" {
		t.Fatalf("lenient: got=%v skipped=%d err=%v", got, skipped, err)
	}
	got = nil
	_, err = ReadLines(p, Strict, decodeOK(&got))
	var le *LineError
	if !errors.As(err, &le) || le.Line != 5 || len(got) != 2 {
		t.Fatalf("strict: got=%v err=%v", got, err)
	}
	if err.Error() != "line 5: rejected" {
		t.Fatalf("message = %q", err)
	}
}

func TestReadLinesMissingFileKeepsNotExist(t *testing.T) {
	for _, pol := range []LinePolicy{Lenient, Strict} {
		_, err := ReadLines(filepath.Join(t.TempDir(), "none"), pol, func([]byte) error { return nil })
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("policy %v: err = %v", pol, err)
		}
	}
}

func TestRewriteLines(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.jsonl")
	if n, err := RewriteLines(p, func([]byte) bool { return false }); n != 0 || err != nil {
		t.Fatalf("missing: %d %v", n, err)
	}
	os.WriteFile(p, []byte("keep\ndrop\n\nkeep2\n"), 0o644)
	keep := func(l []byte) bool { return string(l) != "drop" }
	n, err := RewriteLines(p, keep)
	b, _ := os.ReadFile(p)
	if n != 1 || err != nil || string(b) != "keep\nkeep2\n" {
		t.Fatalf("n=%d err=%v file=%q", n, err, b)
	}
	// nothing dropped leaves the file untouched, blank lines included.
	os.WriteFile(p, []byte("keep\n\n"), 0o644)
	if n, _ := RewriteLines(p, keep); n != 0 {
		t.Fatal(n)
	}
	if b, _ := os.ReadFile(p); string(b) != "keep\n\n" {
		t.Fatalf("file = %q", b)
	}
}

func TestAppendTextKeepsBytesVerbatim(t *testing.T) {
	p := filepath.Join(t.TempDir(), "log")
	for _, s := range []string{"== a\nout", "== b\n"} {
		if err := AppendText(p, s, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if b, _ := os.ReadFile(p); string(b) != "== a\nout== b\n" {
		t.Fatalf("file = %q", b)
	}
}

func TestAppendLineUniqueConcurrentAddsOnce(t *testing.T) {
	p := filepath.Join(t.TempDir(), "info", "exclude")
	var wg sync.WaitGroup
	var mu sync.Mutex
	adds := 0
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := AppendLineUnique(p, "/node_modules", 0o644)
			if err != nil {
				t.Error(err)
			}
			if ok {
				mu.Lock()
				adds++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if b, _ := os.ReadFile(p); string(b) != "/node_modules\n" || adds != 1 {
		t.Fatalf("file = %q, adds = %d", b, adds)
	}
}

func TestAppendLineUniqueFramesMissingNewline(t *testing.T) {
	p := filepath.Join(t.TempDir(), "exclude")
	os.WriteFile(p, []byte("# c\n/a"), 0o644)
	if _, err := AppendLineUnique(p, "/b", 0o644); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "# c\n/a\n/b\n" {
		t.Fatalf("file = %q", b)
	}
}
