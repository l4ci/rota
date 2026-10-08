package tracker

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"
)

// The tracker output echoed into a parse error is cut to clipLen and quoted
// with %q; the cut must not land inside a multi-byte rune.
func TestUnparseableOutputKeepsRunesWhole(t *testing.T) {
	bad := strings.Repeat("a", clipLen-1) + "é"
	s := &scripted{answer: func(string, []string) (string, string, int) { return bad, "", 0 }}
	_, err := newAdapter(t, "gitlab", s).PRFiles(context.Background(), 12)
	if err == nil || !strings.Contains(err.Error(), "unparseable tracker output") {
		t.Fatalf("error %v; want a parse failure", err)
	}
	if msg := err.Error(); !utf8.ValidString(msg) || strings.Contains(msg, `\x`) {
		t.Fatalf("clip split a rune: %q", msg)
	}
}
