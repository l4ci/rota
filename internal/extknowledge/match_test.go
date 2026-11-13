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
	res, err := c.MatchWith([]string{"rpc"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.N != 4 {
		t.Errorf("n = %d, text = %q", res.N, res.Text)
	}
	gi, ni := strings.Index(res.Text, "## Godot: Networking\n"), strings.Index(res.Text, "## Go\n")
	if gi < 0 || ni < 0 {
		t.Fatalf("sections missing: %q", res.Text)
	}
	if strings.Contains(res.Text, "sub-index") || strings.Contains(res.Text, "(networking.md)") {
		t.Errorf("sub-index line printed: %q", res.Text)
	}
	// Wrapped bullet matches as one bullet and prints both lines.
	if !strings.Contains(res.Text, "- **Wrapped RPC** — first line\n  mentions authority on the second") {
		t.Errorf("wrapped bullet: %q", res.Text)
	}
}

func TestMatchNeverPrintsWholeFiles(t *testing.T) {
	c := matchFixture(t)
	res, err := c.MatchWith([]string{"autoloads"}, nil, nil)
	if err != nil || res.N != 1 {
		t.Fatalf("n = %d err = %v", res.N, err)
	}
	if !strings.Contains(res.Text, "## Godot\n- **Autoload order**") {
		t.Errorf("text = %q", res.Text)
	}
	for _, other := range []string{"Signals", "RPC authority", "Vet"} {
		if strings.Contains(res.Text, other) {
			t.Errorf("printed %q: %q", other, res.Text)
		}
	}
	res, _ = c.MatchWith([]string{"RPC", "AUTHORITY"}, nil, nil)
	if res.N != 2 || !strings.Contains(res.Text, "RPC authority") || !strings.Contains(res.Text, "Wrapped RPC") || strings.Contains(res.Text, "Race flag") {
		t.Errorf("all words, case-insensitive: n=%d %q", res.N, res.Text)
	}
}

func TestMatchIgnoresStamp(t *testing.T) {
	c := matchFixture(t)
	for _, w := range []string{"source:game", "2026-03-01", "godot@4.6"} {
		res, err := c.MatchWith([]string{w}, nil, nil)
		if err != nil || res.N != 0 || res.Text != "" {
			t.Errorf("%q matched the stamp: n=%d %q err=%v", w, res.N, res.Text, err)
		}
	}
}

func TestMatchRestrictedToTopics(t *testing.T) {
	c := matchFixture(t)
	res, _ := c.MatchWith([]string{"rpc"}, []string{"Go"}, nil)
	if res.N != 1 || strings.Contains(res.Text, "Godot") {
		t.Errorf("Go only: n=%d %q", res.N, res.Text)
	}
	res, _ = c.MatchWith([]string{"rpc"}, []string{"Godot"}, nil)
	if res.N != 3 || !strings.Contains(res.Text, "## Godot: Networking") || strings.Contains(res.Text, "## Go\n") {
		t.Errorf("split parent includes subtopics: n=%d %q", res.N, res.Text)
	}
	res, _ = c.MatchWith([]string{"signals"}, []string{"Godot: Networking"}, nil)
	if res.N != 0 || res.Text != "" {
		t.Errorf("subtopic only: n=%d %q", res.N, res.Text)
	}
}

func TestMatchNoHit(t *testing.T) {
	c := matchFixture(t)
	res, err := c.MatchWith([]string{"zzzz"}, nil, nil)
	if err != nil || res.N != 0 || res.Text != "" {
		t.Errorf("n=%d %q err=%v", res.N, res.Text, err)
	}
	if _, err := c.MatchWith(nil, nil, nil); err == nil {
		t.Error("no words should be an error")
	}
}

func TestMatchPunctuationIsLiteral(t *testing.T) {
	c := matchFixture(t)
	for _, w := range []string{"RPC.call", "--force"} {
		res, _ := c.MatchWith([]string{w}, nil, nil)
		if res.N != 1 || !strings.Contains(res.Text, "**Flags**") {
			t.Errorf("%q: n=%d %q", w, res.N, res.Text)
		}
	}
	if res, _ := c.MatchWith([]string{"RPC.cal."}, nil, nil); res.N != 0 {
		t.Errorf("regexp-like word matched")
	}
}

func TestMatchShowsRecheckMarker(t *testing.T) {
	c := matchFixture(t)
	res, _ := c.MatchWith([]string{"autoloads"}, nil, map[string]string{"godot": "4.8"})
	if !strings.Contains(res.Text, "recheck)") {
		t.Errorf("no marker: %q", res.Text)
	}
}
