package artifact

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/l4ci/rota/internal/exitcode"
)

func exitOf(err error) int {
	var e *exitcode.Error
	if errors.As(err, &e) {
		return e.Exit
	}
	return 0
}

func TestFileDocsVerbs(t *testing.T) {
	root := t.TempDir()
	d := FileDocs{Root: root, Dir: "plans", Kind: "plan"}
	if _, err := d.Read("M01-B07"); exitOf(err) != exitcode.ExitResolution ||
		err.Error() != "plan M01-B07 not found (.rota/plans/M01-B07.md)" {
		t.Errorf("read missing: %v", err)
	}
	if _, err := d.Replace("M01-B07", "x"); exitOf(err) != exitcode.ExitResolution {
		t.Errorf("replace missing: %v", err)
	}
	if err := d.Remove("M01-B07"); exitOf(err) != exitcode.ExitResolution {
		t.Errorf("remove missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".rota", "plans")); err == nil {
		t.Error("a verb on a missing doc created the directory")
	}
	p := filepath.Join(root, ".rota", "plans", "M01-B07.md")
	if d.Path("M01-B07") != p {
		t.Errorf("path = %q, want %q", d.Path("M01-B07"), p)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := d.Read("M01-B07"); err != nil || got != "old\n" {
		t.Errorf("read: %q %v", got, err)
	}
	if changed, err := d.Replace("M01-B07", "new\n"); err != nil || !changed {
		t.Errorf("replace: %v %v", changed, err)
	}
	if changed, err := d.Replace("M01-B07", "new\n"); err != nil || changed {
		t.Errorf("identical replace: %v %v", changed, err)
	}
	if b, _ := os.ReadFile(p); string(b) != "new\n" {
		t.Errorf("file = %q", b)
	}
	if err := d.Remove("M01-B07"); err != nil {
		t.Errorf("remove: %v", err)
	}
	if _, err := os.Stat(p); err == nil {
		t.Error("file survived remove")
	}
}

type fakeSlot struct {
	text string
	ok   bool
}

func (s *fakeSlot) Get() (string, bool, error) { return s.text, s.ok, nil }
func (s *fakeSlot) Put(text string) (bool, error) {
	if s.ok && s.text == text {
		return false, nil
	}
	s.text, s.ok = text, true
	return true, nil
}
func (s *fakeSlot) Rm() (bool, error) { was := s.ok; s.text, s.ok = "", false; return was, nil }

func TestNoteDocsVerbs(t *testing.T) {
	slot := &fakeSlot{}
	d := NoteDocs{Kind: "design", Open: func(key string) (NoteSlot, string, error) { return slot, key, nil }}
	if _, err := d.Read("7"); exitOf(err) != exitcode.ExitResolution || err.Error() != "design note for 7 not found" {
		t.Errorf("read missing: %v", err)
	}
	if _, err := d.Replace("7", "x"); exitOf(err) != exitcode.ExitResolution {
		t.Errorf("replace missing: %v", err)
	}
	if slot.ok {
		t.Error("replace created a missing note")
	}
	if err := d.Remove("7"); exitOf(err) != exitcode.ExitResolution {
		t.Errorf("remove missing: %v", err)
	}
	slot.text, slot.ok = "old", true
	if got, err := d.Read("7"); err != nil || got != "old\n" {
		t.Errorf("read: %q %v", got, err)
	}
	if changed, err := d.Replace("7", "new"); err != nil || !changed {
		t.Errorf("replace: %v %v", changed, err)
	}
	if changed, err := d.Replace("7", "new"); err != nil || changed {
		t.Errorf("identical replace: %v %v", changed, err)
	}
	if err := d.Remove("7"); err != nil || slot.ok {
		t.Errorf("remove: %v ok=%v", err, slot.ok)
	}
}

func TestNoteDocsOpenError(t *testing.T) {
	boom := exitcode.Errf(exitcode.ExitResolution, "unknown item")
	d := NoteDocs{Kind: "plan", Open: func(string) (NoteSlot, string, error) { return nil, "", boom }}
	if _, err := d.Read("9"); err != boom {
		t.Errorf("read: %v", err)
	}
	if _, err := d.Replace("9", "x"); err != boom {
		t.Errorf("replace: %v", err)
	}
	if err := d.Remove("9"); err != boom {
		t.Errorf("remove: %v", err)
	}
}

func TestFileDocsUpdate(t *testing.T) {
	d := FileDocs{Root: t.TempDir(), Dir: "designs", Kind: "design"}
	more := func(old string) (string, error) { return old + "more\n", nil }
	if _, err := d.Update("B07", more); exitOf(err) != exitcode.ExitResolution ||
		err.Error() != "design B07 not found (.rota/designs/B07.md)" {
		t.Fatalf("update missing: %v", err)
	}
	p := d.Path("B07")
	if err := os.MkdirAll(filepath.Dir(p), 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("a\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if changed, err := d.Update("B07", more); err != nil || !changed {
		t.Fatalf("update: %v %v", changed, err)
	}
	if b, _ := os.ReadFile(p); string(b) != "a\nmore\n" { // newlines normalised on read
		t.Errorf("file = %q", b)
	}
	if changed, err := d.Update("B07", func(old string) (string, error) { return old, nil }); err != nil || changed {
		t.Errorf("identity: %v %v", changed, err)
	}
	boom := errors.New("boom")
	if _, err := d.Update("B07", func(string) (string, error) { return "x", boom }); err != boom {
		t.Errorf("fn error: %v", err)
	}
	if b, _ := os.ReadFile(p); string(b) != "a\nmore\n" {
		t.Errorf("fn error wrote: %q", b)
	}
}

func TestFileDocsCreate(t *testing.T) {
	d := FileDocs{Root: t.TempDir(), Dir: "plans", Kind: "plan"}
	key, err := d.Create("M01-slice", func() (string, string) { return "M01-S01", "one\n" })
	if err != nil || key != "M01-S01" {
		t.Fatalf("create: %q %v", key, err)
	}
	if b, _ := os.ReadFile(d.Path("M01-S01")); string(b) != "one\n" {
		t.Errorf("file = %q", b)
	}
	key, err = d.Create("M01-S01", func() (string, string) { return "M01-S01", "two\n" })
	if exitOf(err) != exitcode.ExitRefused || err.Error() != ".rota/plans/M01-S01.md already exists" || key != "M01-S01" {
		t.Errorf("recreate: %q %v", key, err)
	}
	if b, _ := os.ReadFile(d.Path("M01-S01")); string(b) != "one\n" {
		t.Errorf("recreate overwrote: %q", b)
	}
}
