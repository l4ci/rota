package knowledge

import (
	"errors"
	"os"
	"testing"
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
