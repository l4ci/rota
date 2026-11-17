package knowledge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// One fixture drives every operation that reads a bullet's extent: a bullet
// with an indented continuation, a blank line and unrelated prose, once before
// another bullet and once at the end of its topic.
const unitFixture = "## A\n\n" +
	"- **One** first NEEDLE <!-- paths: [\"src/*.go\"] -->\n" +
	"  continued NEEDLE\n" +
	"\nProse after one: NEEDLE.\n\n" +
	"- **Two** second\n" +
	"  continued two\n" +
	"\nTail prose: NEEDLE.\n\n" +
	"## B\n\n"

func unitStore(t *testing.T) (Store, string) {
	t.Helper()
	s := lockedStore(t)
	km, _ := s.KnowledgePath("")
	if err := os.WriteFile(km, []byte(unitFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	return s, km
}

func readKM(t *testing.T, km string) string {
	t.Helper()
	b, err := os.ReadFile(km)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestBulletUnitAgreesAcrossOperations(t *testing.T) {
	t.Run("replace keeps prose", func(t *testing.T) {
		s, km := unitStore(t)
		if _, err := s.Replace("", "A", "first NEEDLE", "first CHANGED"); err != nil {
			t.Fatal(err)
		}
		want := strings.Replace(unitFixture, "first NEEDLE", "first CHANGED", 1)
		if got := readKM(t, km); got != want {
			t.Errorf("got %q want %q", got, want)
		}
	})
	t.Run("replace sees continuation but not prose", func(t *testing.T) {
		s, km := unitStore(t)
		// "Prose after one: NEEDLE" is outside both bullets, so the
		// continuation NEEDLE alone is a single-bullet match.
		if _, err := s.Replace("", "A", "continued NEEDLE", "continued CHANGED"); err != nil {
			t.Fatal(err)
		}
		if got := readKM(t, km); !strings.Contains(got, "continued CHANGED") || !strings.Contains(got, "Prose after one: NEEDLE.") || !strings.Contains(got, "Tail prose: NEEDLE.") {
			t.Errorf("got %q", got)
		}
	})
	t.Run("replace refuses text only in prose", func(t *testing.T) {
		s, km := unitStore(t)
		if _, err := s.Replace("", "A", "Tail prose", "X"); err == nil {
			t.Fatal("replaced text outside any bullet")
		}
		if got := readKM(t, km); got != unitFixture {
			t.Errorf("file changed: %q", got)
		}
	})
	t.Run("move last bullet leaves tail prose", func(t *testing.T) {
		s, km := unitStore(t)
		if _, err := s.RenameTopic("", "A", "B", "Two"); err != nil {
			t.Fatal(err)
		}
		got := readKM(t, km)
		wantA := "## A\n\n- **One** first NEEDLE <!-- paths: [\"src/*.go\"] -->\n  continued NEEDLE\n\nProse after one: NEEDLE.\n\nTail prose: NEEDLE.\n\n"
		if !strings.HasPrefix(got, wantA) || !strings.HasSuffix(got, "## B\n\n- **Two** second\n  continued two\n") {
			t.Errorf("got %q", got)
		}
	})
	t.Run("move first bullet leaves prose", func(t *testing.T) {
		s, km := unitStore(t)
		if _, err := s.RenameTopic("", "A", "B", "One"); err != nil {
			t.Fatal(err)
		}
		got := readKM(t, km)
		if !strings.Contains(got, "## A\n\nProse after one: NEEDLE.\n\n- **Two**") || !strings.Contains(got, "## B\n\n- **One** first NEEDLE") {
			t.Errorf("got %q", got)
		}
	})
	t.Run("remove keeps prose", func(t *testing.T) {
		s, km := unitStore(t)
		if _, err := s.Remove("", "A", "Two"); err != nil {
			t.Fatal(err)
		}
		if got := readKM(t, km); !strings.Contains(got, "Tail prose: NEEDLE.") || strings.Contains(got, "continued two") {
			t.Errorf("got %q", got)
		}
	})
	t.Run("recall returns the same unit", func(t *testing.T) {
		s, km := unitStore(t)
		_ = km
		entries, err := s.PathEntries(filepath.Join(s.Root, "src/a.go"))
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 || !strings.Contains(entries[0].Text, "continued NEEDLE") || strings.Contains(entries[0].Text, "Prose after one") {
			t.Errorf("entries %+v", entries)
		}
	})
}

// A padded title and a dated bullet read the same here as in extknowledge:
// both go through bulletfmt.Parse.
func TestParseBulletTrimsTitlePadding(t *testing.T) {
	title, body, stamp, ok := parseBullet("- ** Foo ** — the body <!-- 2026-01-02 sha:abc1234 -->")
	if !ok || title != "Foo" || body != "the body" || stamp != " <!-- 2026-01-02 sha:abc1234 -->" {
		t.Fatalf("got %q %q %q ok=%v", title, body, stamp, ok)
	}
	for _, l := range []string{"- **T** — b", "- **T** b <!-- 2026-01-02 -->", "- **T** — b <!-- note -->"} {
		if _, _, _, ok := parseBullet(l); ok {
			t.Errorf("%q must not be a dated bullet", l)
		}
	}
}

// An empty-body dated bullet (knowledge add does not refuse an empty body)
// still parses, and amend appends ahead of the stamp, not after it.
func TestParseBulletEmptyBody(t *testing.T) {
	for _, l := range []string{"- **T** —  <!-- 2026-01-02 -->", "- **T** — <!-- 2026-01-02 -->"} {
		title, body, _, ok := parseBullet(l)
		if !ok || title != "T" || body != "" {
			t.Errorf("%q: got %q %q ok=%v", l, title, body, ok)
		}
		if got := appendToBullet(l, "more"); got != "- **T** — more <!-- 2026-01-02 -->" && got != "- **T** — more  <!-- 2026-01-02 -->" {
			t.Errorf("appendToBullet(%q) = %q", l, got)
		}
	}
}
