package hook

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReadNoteReadsOnceAndKnowsItsMarker(t *testing.T) {
	p := filepath.Join(t.TempDir(), "main.md")
	if err := os.WriteFile(p, []byte(HandoffMarker+"\n\n## Round 7\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	n, err := ReadNote(p)
	if err != nil || !n.Exists || !n.Marked() || n.Path != p {
		t.Fatalf("got %+v %v", n, err)
	}
	if n.Body != HandoffMarker+"\n\n## Round 7\nbody\n" {
		t.Fatalf("body %q", n.Body)
	}
	if !n.Fresh(time.Minute, time.Now()) {
		t.Fatal("a just-written note is fresh")
	}
}

func TestReadNoteMissingOrUnmarked(t *testing.T) {
	d := t.TempDir()
	if n, err := ReadNote(filepath.Join(d, "none.md")); err != nil || n.Exists || n.Marked() {
		t.Fatalf("missing: %+v %v", n, err)
	}
	p := filepath.Join(d, "x.md")
	os.WriteFile(p, []byte("# pause\n"), 0o644)
	if n, err := ReadNote(p); err != nil || !n.Exists || n.Marked() {
		t.Fatalf("unmarked: %+v %v", n, err)
	}
	if n, err := ReadNote(d); err == nil || n.Exists {
		t.Fatalf("a directory is unreadable, not absent: %+v %v", n, err)
	}
}

func TestNoteConsumeArchivesTheBodyAlreadyRead(t *testing.T) {
	p := filepath.Join(t.TempDir(), "main.md")
	os.WriteFile(p, []byte(HandoffMarker+"\nbody"), 0o644)
	os.WriteFile(p+".consumed", []byte("older"), 0o644)
	n, _ := ReadNote(p)
	body, err := n.Consume()
	if err != nil || body != HandoffMarker+"\nbody" {
		t.Fatalf("got %q %v", body, err)
	}
	if _, err := os.Stat(p); err == nil {
		t.Fatal("note still in place")
	}
	if b, _ := os.ReadFile(p + ".consumed"); string(b) != HandoffMarker+"\nbody" {
		t.Fatalf("archive %q", b)
	}
	if _, err := n.Consume(); err == nil {
		t.Fatal("second consume must fail")
	}
}
