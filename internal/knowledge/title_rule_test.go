package knowledge

import (
	"errors"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/bulletfmt"
)

// RenameTopic bullet mode and Remove resolve a title through one rule: case
// and surrounding space ignored, bulletfmt's title grammar (no "*"), a twin
// refused as ambiguous. The same typed title must reach the same verdict in
// both verbs.
func TestBulletVerbsResolveTitlesAlike(t *testing.T) {
	const star = "## A\n\n- **Star*Title** x\n- **Plain** y\n\n## B\n\n"
	cases := []struct {
		name, in, title string
		err             error
		moved           string // title left in A after a hit; "" when it must be refused
	}{
		{name: "mixed case hits", in: "## A\n\n- **MixedCase** x\n\n## B\n\n", title: "mixedCASE"},
		{name: "case-only twins refused", in: "## A\n\n- **Two** a\n- **TWO** b\n\n## B\n\n", title: "two", err: ErrMultiMatch},
		{name: "star title is not a title", in: star, title: "Star*Title", err: ErrNotFound},
		{name: "plain title beside a star line", in: star, title: "plain"},
	}
	verbs := map[string]func(Store, string) error{
		"rename": func(s Store, title string) error { _, err := s.RenameTopic("", "A", "B", title); return err },
		"remove": func(s Store, title string) error { _, err := s.Remove("", "A", title); return err },
	}
	for _, c := range cases {
		for name, run := range verbs {
			t.Run(c.name+"/"+name, func(t *testing.T) {
				s := lockedStore(t)
				km, _ := s.KnowledgePath("")
				if err := os.WriteFile(km, []byte(c.in), 0o644); err != nil {
					t.Fatal(err)
				}
				err := run(s, c.title)
				if !errors.Is(err, c.err) {
					t.Fatalf("err = %v, want %v", err, c.err)
				}
				got, _ := os.ReadFile(km)
				if c.err != nil && string(got) != c.in {
					t.Errorf("refused call changed the file: %q", got)
				}
				if c.err == nil && string(got) == c.in {
					t.Errorf("hit left the file unchanged")
				}
			})
		}
	}
}

// A case-variant title moves the bullet and its tier record: the sidecar is
// keyed by the stored title, not the typed one.
func TestRenameBulletCaseVariantKeepsTierRecord(t *testing.T) {
	s := lockedStore(t)
	km, _ := s.KnowledgePath("")
	if err := os.WriteFile(km, []byte("## A\n\n- **MixedCase** x\n\n## B\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TierSet("", "A", "MixedCase", Deprecated); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RenameTopic("", "A", "B", "mixedCASE"); err != nil {
		t.Fatal(err)
	}
	entries, err := s.TierList("", "")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Topic+"/"+e.Title+"="+e.Tier)
	}
	if len(got) != 1 || got[0] != "B/MixedCase="+Deprecated {
		t.Fatalf("tier records = %v, want [B/MixedCase=%s]", got, Deprecated)
	}
}

// Add, hit, tier backfill and rm read the same bullets: an indented bold line
// continues the bullet above it and names none of its own.
func TestIndentedBoldLineIsNoBulletForAnyVerb(t *testing.T) {
	const in = "## A\n\n- **Foo** — x <!-- 2026-01-01 -->\n  - **Note** continuation\n"
	s := lockedStore(t)
	km, _ := s.KnowledgePath("")
	if err := os.WriteFile(km, []byte(in), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Hit("", "A", "Note"); !errors.Is(err, ErrNotFound) {
		t.Errorf("hit err = %v, want ErrNotFound", err)
	}
	if _, err := s.Remove("", "A", "Note"); !errors.Is(err, ErrNotFound) {
		t.Errorf("rm err = %v, want ErrNotFound", err)
	}
	if got := tierTitles(t, s); got != "A/Foo" {
		t.Errorf("backfilled = %q, want A/Foo", got)
	}
	res, err := s.Add("", "A", "Note", "body", "2026-01-02", "")
	if err != nil || !res.Changed {
		t.Errorf("add = %+v, %v; want a new bullet", res, err)
	}
}

// Surrounding spaces and case do not make a second title: add writes the
// trimmed one, and a repeat tracks the title the file stores.
func TestAddTrimsAndMatchesTheStoredTitle(t *testing.T) {
	s := lockedStore(t)
	km, _ := s.KnowledgePath("")
	if err := os.WriteFile(km, []byte("## A\n\n- **Foo** — x <!-- 2026-01-01 -->\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{" Foo ", "foo"} {
		res, err := s.Add("", "A", title, "body", "2026-01-02", "")
		if err != nil || res.Changed {
			t.Errorf("add %q = %+v, %v; want unchanged", title, res, err)
		}
	}
	if _, err := s.Add("", "A", "  Bar\t", "body", "2026-01-02", ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(km); !strings.Contains(string(got), "- **Bar** — body") {
		t.Errorf("file = %q, want the trimmed title", got)
	}
	if got := tierTitles(t, s); got != "A/Bar A/Foo" {
		t.Errorf("tier records = %q, want A/Bar A/Foo", got)
	}
	if _, err := s.Add("", "A", "   ", "body", "2026-01-02", ""); !errors.Is(err, bulletfmt.ErrInvalid) {
		t.Errorf("blank title err = %v, want ErrInvalid", err)
	}
}

func tierTitles(t *testing.T, s Store) string {
	t.Helper()
	entries, err := s.TierList("", "")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Topic+"/"+e.Title)
	}
	sort.Strings(got)
	return strings.Join(got, " ")
}
