package extknowledge

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
)

const seedGodot = "# Godot\n\n" +
	"- **Tween leak** — Kill tweens on exit. <!-- 2026-09-01 source:game seen:godot@4.6 -->\n" +
	"- **Both tools** — Needs both. <!-- 2026-09-02 source:game seen:godot@4.6,swift@6.1 -->\n" +
	"- **Current** — Fine on 4.8. <!-- 2026-09-03 source:game seen:godot@4.8 -->\n" +
	"- **Legacy** — No version. <!-- 2026-08-02 source:kluft+Upracked -->\n"

// withGodot adds a Godot topic to the working clone (Query reads the files).
func withGodot(t *testing.T, f *fixture) Clone {
	t.Helper()
	gittest.Write(t, f.a, IndexFile, seedIndex+"- [Godot](topics/godot.md) — engine quirks\n")
	gittest.Write(t, f.a, "topics/godot.md", seedGodot)
	return f.clone()
}

func TestAddWritesSeenStamp(t *testing.T) {
	f := newFixture(t, "")
	c := f.clone()
	seen, err := ParseSeen("godot@4.6 swift@6")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Add(context.Background(), AddIn{Topic: "Go", Title: "Seen one", Body: "b.", Date: "2026-10-25", Source: "rota", Seen: seen}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(f.a, "topics/go.md"))
	if !strings.Contains(string(raw), "- **Seen one** — b. <!-- 2026-10-25 source:rota seen:godot@4.6,swift@6 -->") {
		t.Fatalf("topic file = %q", raw)
	}
}

func TestParseSeen(t *testing.T) {
	got, err := ParseSeen(" godot@4.6, three.js@r186 ")
	if err != nil || len(got) != 2 || got[0] != (ToolVersion{"godot", "4.6"}) || got[1] != (ToolVersion{"three.js", "r186"}) {
		t.Fatalf("got %v, %v", got, err)
	}
	if got.String() != "godot@4.6,three.js@r186" {
		t.Errorf("String = %q", got.String())
	}
	for _, bad := range []string{"godot@", "@4.6", "godot", "go dot@4", "a@1,a@2", "x@1 -->", "Godot@4.6", "godot@4.6 Swift@6"} {
		if _, err := ParseSeen(bad); err == nil {
			t.Errorf("ParseSeen(%q) accepted", bad)
		}
	}
}

func TestParseBulletsFinalCommentIsStamp(t *testing.T) {
	file := "# T\n\n- **A** — body with seen:godot@1 and <!-- x --> inside <!-- 2026-01-02 source:p+q seen:godot@4.6 -->\n- **B** — plain\n"
	bs := parseBullets(file)
	if len(bs) != 2 {
		t.Fatalf("bullets = %+v", bs)
	}
	a := bs[0]
	if a.Title != "A" || a.Date != "2026-01-02" || a.Source != "p+q" || len(a.Seen) != 1 || a.Seen[0].Version != "4.6" || a.Line != 2 {
		t.Errorf("A = %+v", a)
	}
	if !strings.HasSuffix(a.Body, "inside") {
		t.Errorf("body = %q", a.Body)
	}
	if bs[1].Stamp != "" || bs[1].Seen != nil {
		t.Errorf("B = %+v", bs[1])
	}
}

func TestQueryFlagsNewerProjectVersion(t *testing.T) {
	f := newFixture(t, "")
	c := withGodot(t, f)
	text, _, recheck, err := c.Query([]string{"godot"}, map[string]string{"godot": "4.8"})
	if err != nil {
		t.Fatal(err)
	}
	want := "- **Tween leak** — Kill tweens on exit. (seen on godot 4.6, project on 4.8: recheck) <!-- 2026-09-01 source:game seen:godot@4.6 -->\n"
	if !strings.Contains(text, want) {
		t.Fatalf("text = %q", text)
	}
	if strings.Contains(text, "Current — Fine on 4.8. (seen") {
		t.Errorf("4.8 bullet flagged: %q", text)
	}
	found := false
	for _, r := range recheck {
		if r.Title == "Tween leak" && r == (Recheck{Topic: "Godot", Title: "Tween leak", Tool: "godot", Seen: "4.6", Project: "4.8"}) {
			found = true
		}
	}
	if !found {
		t.Errorf("recheck = %+v", recheck)
	}
}

func TestQueryWithoutSeenIsUnchanged(t *testing.T) {
	f := newFixture(t, "")
	c := withGodot(t, f)
	const golden = "## Godot\n" +
		"- **Tween leak** — Kill tweens on exit. <!-- 2026-09-01 source:game seen:godot@4.6 -->\n" +
		"- **Both tools** — Needs both. <!-- 2026-09-02 source:game seen:godot@4.6,swift@6.1 -->\n" +
		"- **Current** — Fine on 4.8. <!-- 2026-09-03 source:game seen:godot@4.8 -->\n" +
		"- **Legacy** — No version. <!-- 2026-08-02 source:kluft+Upracked -->\n"
	for _, project := range []map[string]string{nil, {}, {"godot": "4.6"}, {"godot": "4.6.9"}} {
		text, _, recheck, err := c.Query([]string{"godot"}, project)
		if err != nil || text != golden || len(recheck) != 0 {
			t.Errorf("project %v: err=%v recheck=%v text=%q", project, err, recheck, text)
		}
	}
	// A legacy bullet is untouched even when the project is far ahead.
	text, _, _, _ := c.Query([]string{"godot"}, map[string]string{"godot": "9.0"})
	if !strings.Contains(text, "- **Legacy** — No version. <!-- 2026-08-02 source:kluft+Upracked -->\n") {
		t.Errorf("legacy bullet changed: %q", text)
	}
}

func TestQueryMarkerNamesEveryOutgrownTool(t *testing.T) {
	f := newFixture(t, "")
	c := withGodot(t, f)
	text, _, recheck, err := c.Query([]string{"godot"}, map[string]string{"godot": "4.8", "swift": "6.2"})
	if err != nil {
		t.Fatal(err)
	}
	want := "- **Both tools** — Needs both. (seen on godot 4.6, project on 4.8; seen on swift 6.1, project on 6.2: recheck) <!-- 2026-09-02"
	if !strings.Contains(text, want) {
		t.Fatalf("text = %q", text)
	}
	n := 0
	for _, r := range recheck {
		if r.Title == "Both tools" {
			n++
		}
	}
	if n != 2 {
		t.Errorf("recheck rows for the bullet = %d, want 2 (%+v)", n, recheck)
	}
}
