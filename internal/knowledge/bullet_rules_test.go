package knowledge

import (
	"errors"
	"os"
	"testing"
)

// The bullet-selection rules Replace, Amend and Remove share with the external
// store (bulletfmt owns them). internal/extknowledge/bullet_rules_test.go runs
// the same cases through its own store: a case added here goes there too.

func rulesStore(t *testing.T, content string) (Store, string) {
	t.Helper()
	s := lockedStore(t)
	km, _ := s.KnowledgePath("")
	if err := os.WriteFile(km, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return s, km
}

func TestReplaceNeverSearchesTheStamp(t *testing.T) {
	const in = "## A\n\n- **One** body <!-- 2026-01-01 sha:abc1234 -->\n"
	for _, old := range []string{"2026-01-01", "sha:abc1234", "body <!--"} {
		s, km := rulesStore(t, in)
		if _, err := s.Replace("", "A", old, "x"); !errors.Is(err, ErrNotFound) {
			t.Errorf("old %q: err = %v, want ErrNotFound", old, err)
		}
		if got := readKM(t, km); got != in {
			t.Errorf("old %q: file changed: %q", old, got)
		}
	}
	s, km := rulesStore(t, in)
	if _, err := s.Replace("", "A", "body", "text"); err != nil {
		t.Fatal(err)
	}
	if got, want := readKM(t, km), "## A\n\n- **One** text <!-- 2026-01-01 sha:abc1234 -->\n"; got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestAmendTreatsAnIndentedDashLineAsContinuation(t *testing.T) {
	const in = "## A\n\n- **One** — body <!-- 2026-01-01 -->\n  - nested note\n"
	s, km := rulesStore(t, in)
	if _, changed, err := s.Amend("", false, "A", "nested note", "more"); err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	// The text lands on the bullet the nested line belongs to.
	if got, want := readKM(t, km), "## A\n\n- **One** — body more <!-- 2026-01-01 -->\n  - nested note\n"; got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestAmendRefusesTwoBulletsInOneFile(t *testing.T) {
	const in = "## A\n\n- **One** has X <!-- 2026-01-01 -->\n- **Two** has X <!-- 2026-01-01 -->\n"
	s, km := rulesStore(t, in)
	if _, _, err := s.Amend("", false, "A", "X", "more"); !errors.Is(err, ErrMultiMatch) {
		t.Fatalf("err = %v, want ErrMultiMatch", err)
	}
	if got := readKM(t, km); got != in {
		t.Errorf("file changed: %q", got)
	}
}

func TestAmendNeverSearchesTheStamp(t *testing.T) {
	const in = "## A\n\n- **One** body <!-- 2026-01-01 -->\n"
	s, km := rulesStore(t, in)
	if _, _, err := s.Amend("", false, "A", "2026-01-01", "more"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if got := readKM(t, km); got != in {
		t.Errorf("file changed: %q", got)
	}
}

func TestRemoveCollapsesBlankLines(t *testing.T) {
	cases := []struct{ name, in, title, want string }{
		{"between blanks", "## A\n\n- **One** a\n\n- **Two** b\n\n## B\n", "One", "## A\n\n- **Two** b\n\n## B\n"},
		{"last of topic", "## A\n\n- **Two** b\n\n## B\n", "Two", "## A\n\n## B\n"},
		{"wrapped bullet goes whole", "## A\n\n- **One** a\n  wrapped\n\n- **Two** b\n", "One", "## A\n\n- **Two** b\n"},
		{"next line is not blank", "## A\n\n- **One** a\n- **Two** b\n", "One", "## A\n\n- **Two** b\n"},
	}
	for _, c := range cases {
		s, km := rulesStore(t, c.in)
		if _, err := s.Remove("", "A", c.title); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := readKM(t, km); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func TestHitKeysEverythingByTheStoredTitle(t *testing.T) {
	s, _ := rulesStore(t, "## Arch\n\n- **Foo** — body <!-- 2026-01-01 -->\n")
	if _, err := s.AddContradiction("Arch", "foo", "wrong"); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= s.PromoteThreshold(); i++ {
		r, err := s.Hit(Umbrella, "Arch", "foo")
		if err != nil {
			t.Fatal(err)
		}
		if r.Entry.Title != "Foo" {
			t.Errorf("hit title = %q, want the stored Foo", r.Entry.Title)
		}
		// The queue entry was typed "foo": it still blocks the stored bullet.
		if want := i == s.PromoteThreshold(); r.PromotionBlocked != want {
			t.Errorf("hit %d: contradiction typed in another case did not block promotion: %+v", i, r)
		}
	}
	sc, err := s.effective(Umbrella)
	if err != nil {
		t.Fatal(err)
	}
	if e, ok, _ := sc.Get("Arch", "Foo"); !ok || e.Hits != s.PromoteThreshold() {
		t.Errorf("Foo tier row = %+v ok=%v, want one row holding every hit", e, ok)
	}
	if _, ok, _ := sc.Get("Arch", "foo"); ok {
		t.Error("a second tier row was keyed by the typed title")
	}
	if has, err := s.HasContradiction("Arch", "FOO"); err != nil || !has {
		t.Errorf("HasContradiction in another case = %v, %v", has, err)
	}
}

func TestHitRefusesCaseTwins(t *testing.T) {
	s, _ := rulesStore(t, "## Arch\n\n- **Foo** — a <!-- 2026-01-01 -->\n- **FOO** — b <!-- 2026-01-01 -->\n")
	_, err := s.Hit(Umbrella, "Arch", "foo")
	if !errors.Is(err, ErrMultiMatch) {
		t.Fatalf("err = %v, want ErrMultiMatch", err)
	}
	if hp, _ := s.HitsPath(Umbrella); fileExists(hp) {
		t.Error("a refused hit was buffered")
	}
}

// A multi-line bullet crosses the one extent rule (bulletfmt) through Replace
// and Remove: both take the wrapped line and leave the prose after it.
func TestMultiLineBulletExtentForReplaceAndRemove(t *testing.T) {
	const in = "## A\n\n- **One** a\n  wrapped tail\n\nprose\n- **Two** b\n"
	s, km := rulesStore(t, in)
	if _, err := s.Replace("", "A", "tail", "end"); err != nil {
		t.Fatal(err)
	}
	if got, want := readKM(t, km), "## A\n\n- **One** a\n  wrapped end\n\nprose\n- **Two** b\n"; got != want {
		t.Errorf("Replace: got %q want %q", got, want)
	}
	if _, err := s.Remove("", "A", "One"); err != nil {
		t.Fatal(err)
	}
	if got, want := readKM(t, km), "## A\n\nprose\n- **Two** b\n"; got != want {
		t.Errorf("Remove: got %q want %q", got, want)
	}
}
