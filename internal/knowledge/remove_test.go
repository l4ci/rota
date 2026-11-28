package knowledge

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestRemove(t *testing.T) {
	const tier = `{"version":1,"entries":{"A::One":{"tier":"provisional","hits":1,"lastSeen":"2026-01-01"},"A::Two":{"tier":"provisional","hits":1,"lastSeen":"2026-01-01"}}}`
	cases := []struct {
		name, in, title string
		want            string
		err             error
		tierGone        bool
		tierTitle       string
		names           []string
	}{
		{name: "no final newline", in: "## A\n\n- **One** a\n- **Two** b", title: "Two", want: "## A\n\n- **One** a\n", tierGone: true},
		{name: "prose after last bullet stays", in: "## A\n\n- **One** a\n- **Two** b\n  wrapped\n\nSome prose.\n\n## B\n", title: "Two", want: "## A\n\n- **One** a\n\nSome prose.\n\n## B\n", tierGone: true},
		{name: "duplicate title refused", in: "## A\n\n- **One** a\n- **One** b\n", title: "One", err: ErrMultiMatch},
		{name: "title matches case-insensitively", in: "## A\n\n- **One** a\n- **Two** b\n", title: "tWo", want: "## A\n\n- **One** a\n", tierGone: true, tierTitle: "Two"},
		{name: "case-only twins refused as ambiguous", in: "## A\n\n- **Two** a\n- **TWO** b\n", title: "two", err: ErrMultiMatch, names: []string{"Two", "TWO"}},
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
			for _, n := range c.names {
				if !strings.Contains(err.Error(), "'"+n+"'") {
					t.Errorf("error %q does not name %q", err, n)
				}
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
			if c.tierTitle != "" {
				if _, ok, _ := e.Get("A", c.tierTitle); ok {
					t.Errorf("tier entry for %q survived", c.tierTitle)
				}
			} else if _, ok, _ := e.Get("A", c.title); ok == (c.tierGone && c.err == nil) && c.title != "Nope" && len(c.names) == 0 {
				t.Errorf("tier entry for %q present=%v", c.title, ok)
			}
			if c.err != nil {
				if _, ok, _ := e.Get("A", "One"); !ok {
					t.Error("refused rm dropped a tier entry")
				}
			}
		})
	}
}

// A replace that strips a bold title drops its tier record and buffered hits,
// so a later bullet of that title starts provisional.
func TestReplaceRemovingTheTitleDropsTheTierRecord(t *testing.T) {
	s := lockedStore(t)
	writeBullets(t, s, "## A\n\n- **Gone** — body\n")
	if _, err := s.TierSet("", "A", "Gone", Deprecated); err != nil {
		t.Fatal(err)
	}
	// Buffered: it must not recreate the record on a later fold.
	if _, err := s.Hit("", "A", "Gone"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Replace("", "A", "**Gone**", "Gone"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FoldHits(""); err != nil {
		t.Fatal(err)
	}
	sc, err := LoadSidecar(mustTier(t, s))
	if err != nil {
		t.Fatal(err)
	}
	if e, ok, _ := sc.Get("A", "Gone"); ok {
		t.Fatalf("tier record survived: %+v", e)
	}
	if _, err := s.Add("", "A", "Gone", "again", "2026-01-01", ""); err != nil {
		t.Fatal(err)
	}
	e, _, err := s.TierGet("", "A", "Gone")
	if err != nil || e.Tier != Provisional || e.Hits != 0 {
		t.Fatalf("later bullet = %+v, %v; want provisional with no hits", e, err)
	}
}

// A twin already on disk still holds the title, so the record stays.
func TestReplaceKeepsTheTierRecordATwinStillHolds(t *testing.T) {
	s := lockedStore(t)
	writeBullets(t, s, "## A\n\n- **One** — a\n- **One** — b\n")
	km, _ := s.KnowledgePath("")
	sc, _ := s.TierPath("")
	if err := os.WriteFile(sc, []byte(`{"version":1,"entries":{"A::One":{"tier":"confirmed","hits":1,"lastSeen":"2026-01-01"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Replace("", "A", "**One** — a", "plain"); err != nil {
		t.Fatalf("%v:\n%s", err, mustRead(t, km))
	}
	got, err := LoadSidecar(sc)
	if err != nil {
		t.Fatal(err)
	}
	if e, ok, _ := got.Get("A", "One"); !ok || e.Tier != Confirmed {
		t.Errorf("twin lost its record: %+v ok=%v", e, ok)
	}
}
