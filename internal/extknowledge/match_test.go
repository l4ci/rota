package extknowledge

import (
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
)

// matchFixture is a split Godot topic (one README bullet, one subtopic bullet,
// one wrapped subtopic bullet) beside a flat Go topic.
func matchFixture(t *testing.T) Clone {
	t.Helper()
	f := splitFixture(t, "")
	gittest.Write(t, f.a, "topics/godot/networking.md", splitNet+
		"- **Wrapped RPC** — first line\n  mentions authority on the second <!-- 2026-04-02 source:game seen:godot@4.6 -->\n"+
		"- **Flags** — Pass --force and RPC.call literally. <!-- 2026-04-03 source:game -->\n")
	gittest.Write(t, f.a, "topics/go.md", "# Go\n\n- **Race flag** — Run with -race. The rpc layer needs it. <!-- 2026-01-01 source:app -->\n- **Vet** — Always vet. <!-- 2026-01-02 source:app -->\n")
	return f.clone()
}

func TestMatchAcrossTopics(t *testing.T) {
	c := matchFixture(t)
	text, n, err := c.Match([]string{"rpc"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Errorf("n = %d, text = %q", n, text)
	}
	gi, ni := strings.Index(text, "## Godot: Networking\n"), strings.Index(text, "## Go\n")
	if gi < 0 || ni < 0 {
		t.Fatalf("sections missing: %q", text)
	}
	if strings.Contains(text, "sub-index") || strings.Contains(text, "(networking.md)") {
		t.Errorf("sub-index line printed: %q", text)
	}
	// Wrapped bullet matches as one bullet and prints both lines.
	if !strings.Contains(text, "- **Wrapped RPC** — first line\n  mentions authority on the second") {
		t.Errorf("wrapped bullet: %q", text)
	}
}

func TestMatchNeverPrintsWholeFiles(t *testing.T) {
	c := matchFixture(t)
	text, n, err := c.Match([]string{"autoloads"}, nil, nil)
	if err != nil || n != 1 {
		t.Fatalf("n = %d err = %v", n, err)
	}
	if !strings.Contains(text, "## Godot\n- **Autoload order**") {
		t.Errorf("text = %q", text)
	}
	for _, other := range []string{"Signals", "RPC authority", "Vet"} {
		if strings.Contains(text, other) {
			t.Errorf("printed %q: %q", other, text)
		}
	}
	text, n, _ = c.Match([]string{"RPC", "AUTHORITY"}, nil, nil)
	if n != 2 || !strings.Contains(text, "RPC authority") || !strings.Contains(text, "Wrapped RPC") || strings.Contains(text, "Race flag") {
		t.Errorf("all words, case-insensitive: n=%d %q", n, text)
	}
}

func TestMatchIgnoresStamp(t *testing.T) {
	c := matchFixture(t)
	for _, w := range []string{"source:game", "2026-03-01", "godot@4.6"} {
		text, n, err := c.Match([]string{w}, nil, nil)
		if err != nil || n != 0 || text != "" {
			t.Errorf("%q matched the stamp: n=%d %q err=%v", w, n, text, err)
		}
	}
}

func TestMatchRestrictedToTopics(t *testing.T) {
	c := matchFixture(t)
	text, n, _ := c.Match([]string{"rpc"}, []string{"Go"}, nil)
	if n != 1 || strings.Contains(text, "Godot") {
		t.Errorf("Go only: n=%d %q", n, text)
	}
	text, n, _ = c.Match([]string{"rpc"}, []string{"Godot"}, nil)
	if n != 3 || !strings.Contains(text, "## Godot: Networking") || strings.Contains(text, "## Go\n") {
		t.Errorf("split parent includes subtopics: n=%d %q", n, text)
	}
	text, n, _ = c.Match([]string{"signals"}, []string{"Godot: Networking"}, nil)
	if n != 0 || text != "" {
		t.Errorf("subtopic only: n=%d %q", n, text)
	}
}

func TestMatchNoHit(t *testing.T) {
	c := matchFixture(t)
	text, n, err := c.Match([]string{"zzzz"}, nil, nil)
	if err != nil || n != 0 || text != "" {
		t.Errorf("n=%d %q err=%v", n, text, err)
	}
	if _, _, err := c.Match(nil, nil, nil); err == nil {
		t.Error("no words should be an error")
	}
}

func TestMatchPunctuationIsLiteral(t *testing.T) {
	c := matchFixture(t)
	for _, w := range []string{"RPC.call", "--force"} {
		text, n, _ := c.Match([]string{w}, nil, nil)
		if n != 1 || !strings.Contains(text, "**Flags**") {
			t.Errorf("%q: n=%d %q", w, n, text)
		}
	}
	if _, n, _ := c.Match([]string{"RPC.cal."}, nil, nil); n != 0 {
		t.Errorf("regexp-like word matched")
	}
}

func TestMatchShowsRecheckMarker(t *testing.T) {
	c := matchFixture(t)
	text, _, _ := c.Match([]string{"autoloads"}, nil, map[string]string{"godot": "4.8"})
	if !strings.Contains(text, "recheck)") {
		t.Errorf("no marker: %q", text)
	}
}
