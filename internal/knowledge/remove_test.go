package knowledge

import (
	"errors"
	"os"
	"testing"
)

func TestRemove(t *testing.T) {
	const tier = `{"version":1,"entries":{"A::One":{"tier":"provisional","hits":1,"lastSeen":"2026-01-01"},"A::Two":{"tier":"provisional","hits":1,"lastSeen":"2026-01-01"}}}`
	cases := []struct {
		name, in, title string
		want            string
		err             error
		tierGone        bool
	}{
		{name: "no final newline", in: "## A\n\n- **One** a\n- **Two** b", title: "Two", want: "## A\n\n- **One** a\n", tierGone: true},
		{name: "prose after last bullet stays", in: "## A\n\n- **One** a\n- **Two** b\n  wrapped\n\nSome prose.\n\n## B\n", title: "Two", want: "## A\n\n- **One** a\n\nSome prose.\n\n## B\n", tierGone: true},
		{name: "duplicate title refused", in: "## A\n\n- **One** a\n- **One** b\n", title: "One", err: ErrMultiMatch},
		{name: "only bullet collapses blanks", in: "## A\n\n- **One** a\n\n## B\n", title: "One", want: "## A\n\n## B\n", tierGone: true},
		{name: "missing bullet", in: "## A\n\n- **One** a\n", title: "Nope", err: ErrNotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := lockedStore(t)
			km, _ := s.KnowledgePath("")
			sc, _ := s.TierPath("")
			for p, text := range map[string]string{km: c.in, sc: tier} {
				if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			_, err := s.Remove("", "A", c.title)
			if !errors.Is(err, c.err) {
				t.Fatalf("err = %v, want %v", err, c.err)
			}
			got, _ := os.ReadFile(km)
			want := c.want
			if c.err != nil {
				want = c.in
			}
			if string(got) != want {
				t.Errorf("KNOWLEDGE.md = %q, want %q", got, want)
			}
			e, _ := LoadSidecar(sc)
			if _, ok := e.Get("A", c.title); ok == (c.tierGone && c.err == nil) && c.title != "Nope" {
				t.Errorf("tier entry for %q present=%v", c.title, ok)
			}
			if c.err != nil {
				if _, ok := e.Get("A", "One"); !ok {
					t.Error("refused rm dropped a tier entry")
				}
			}
		})
	}
}
