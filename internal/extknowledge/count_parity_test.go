package extknowledge

import (
	"os"
	"testing"
)

// The external store counts bullets with the same fixture the local store's
// test reads, so the two cannot disagree on nested or wrapped lines.
func TestBulletsCountSharedFixture(t *testing.T) {
	b, err := os.ReadFile("../bulletfmt/testdata/count.md")
	if err != nil {
		t.Fatal(err)
	}
	if got := len(Topic{}.bullets(string(b))); got != 3 {
		t.Fatalf("bullets = %d, want 3", got)
	}
}
