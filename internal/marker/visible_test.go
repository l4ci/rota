package marker

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestVisible(t *testing.T) {
	for _, c := range []struct{ name, body, want string }{
		{"rota marker dropped", "<!-- rota:comment note -->\nhello  \n\n\n\nworld", "hello\n\nworld"},
		{"legacy hv marker dropped", "<!-- hv:comment note -->\nhello", "hello"},
		{"marker only names itself", "<!-- rota:claim s1 -->", "(rota claim s1)"},
		{"legacy marker only names itself", "<!-- hv:claim s1 -->", "(rota claim s1)"},
		{"no marker is trimmed", "  plain  \n", "plain"},
	} {
		if got := Visible(c.body); got != c.want {
			t.Errorf("%s: Visible(%q) = %q, want %q", c.name, c.body, got, c.want)
		}
	}
}

func TestVisibleCaps(t *testing.T) {
	got := Visible("<!-- rota:comment note -->\n" + strings.Repeat("x", VisibleMax+10))
	if n := utf8.RuneCountInString(got); n != VisibleMax+3 || !strings.HasSuffix(got, "\n\n…") {
		t.Fatalf("got %d runes, tail %q", n, got[len(got)-6:])
	}
}
