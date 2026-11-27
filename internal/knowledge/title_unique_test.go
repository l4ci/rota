package knowledge

import (
	"errors"
	"os"
	"testing"
)

// Replace and bullet-mode rename refuse a title the target topic already
// holds, in any case, and leave the knowledge file and tier sidecar as they were.
func TestRetitleAndMoveRefuseATakenTitle(t *testing.T) {
	const md = "## A\n\n- **One** — a\n- **Two** — b\n- plain c\n\n## B\n\n- **ONE** — d\n"
	cases := map[string]func(Store) error{
		"replace":            func(s Store) error { _, err := s.Replace("", "A", "**One**", "**Two**"); return err },
		"replace other case": func(s Store) error { _, err := s.Replace("", "A", "**One**", "**tWO**"); return err },
		"replace untitled":   func(s Store) error { _, err := s.Replace("", "A", "plain", "**Two**"); return err },
		"move":               func(s Store) error { _, err := s.RenameTopic("", "A", "B", "one"); return err },
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			s := lockedStore(t)
			writeBullets(t, s, md)
			for _, b := range [][3]string{{"A", "One", Confirmed}, {"A", "Two", Deprecated}, {"B", "ONE", Deprecated}} {
				if _, err := s.TierSet("", b[0], b[1], b[2]); err != nil {
					t.Fatal(err)
				}
			}
			// A buffered hit: a refusal must not fold it into the sidecar.
			if _, err := s.Hit("", "A", "One"); err != nil {
				t.Fatal(err)
			}
			km, _ := s.KnowledgePath("")
			sc, _ := s.TierPath("")
			before, tiers := mustRead(t, km), mustRead(t, sc)
			if err := run(s); !errors.Is(err, ErrExists) {
				t.Fatalf("err = %v, want ErrExists", err)
			}
			if got := mustRead(t, km); got != before {
				t.Errorf("knowledge file changed:\n%s", got)
			}
			if got, err := os.ReadFile(sc); err != nil || string(got) != tiers {
				t.Errorf("tier sidecar changed (%v):\n%s", err, got)
			}
		})
	}
}

// A case change in place is not a second title, even beside twins already on disk.
func TestReplaceAllowsCaseChangeInPlace(t *testing.T) {
	for _, md := range []string{"## A\n\n- **One** — a\n", "## A\n\n- **One** — a\n- **ONE** — b\n"} {
		s := lockedStore(t)
		writeBullets(t, s, md)
		res, err := s.Replace("", "A", "**One**", "**one**")
		if err != nil || !res.Changed {
			t.Errorf("%q: replace = %+v, %v; want a change", md, res, err)
		}
	}
}
