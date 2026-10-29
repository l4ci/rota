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
		wantA := "## A\n\n- **One** first NEEDLE <!-- paths: [\"src/*.go\"] -->\n  continued NEEDLE\n\nProse after one: NEEDLE.\n\n\nTail prose: NEEDLE.\n\n"
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
		if !strings.Contains(got, "## A\n\n\nProse after one: NEEDLE.\n\n- **Two**") || !strings.Contains(got, "## B\n\n- **One** first NEEDLE") {
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
