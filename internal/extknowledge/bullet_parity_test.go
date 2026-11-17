package extknowledge

import "testing"

// A title with padding inside the ** parses the same here as in the local
// store (internal/knowledge trims it): the shared splitter owns the rule.
func TestParseBulletsTrimsTitlePadding(t *testing.T) {
	bs := parseBullets("- ** Foo ** — the body <!-- 2026-01-02 source:x -->\n")
	if len(bs) != 1 {
		t.Fatalf("got %d bullets, want 1", len(bs))
	}
	b := bs[0]
	if b.Title != "Foo" || b.Body != "the body" || b.Date != "2026-01-02" || b.Source != "x" {
		t.Fatalf("got title=%q body=%q date=%q source=%q", b.Title, b.Body, b.Date, b.Source)
	}
}
