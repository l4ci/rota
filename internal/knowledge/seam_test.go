package knowledge

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/bulletfmt"
)

// Every term line the reader lists must survive a glossary write: the writer
// and the reader share one term-line rule.
func TestGlossaryWriteKeepsEveryReaderVisibleTerm(t *testing.T) {
	shapes := map[string]string{
		"canonical": "- **Canon** — the canonical shape\n  - **Aliases:** _none_\n  <!-- 2026-01-01 -->\n",
		"colon":     "- **Colon**: a colon definition\n",
		"hyphen":    "- **Hyphen** - a hyphen definition\n",
		"bare":      "- **Bare** a definition with no separator\n",
		"name only": "- **Solo**\n",
	}
	for name, line := range shapes {
		t.Run(name, func(t *testing.T) {
			s := lockedStore(t)
			km, _ := s.KnowledgePath(Umbrella)
			if err := os.WriteFile(km, []byte("# K\n\n## Glossary\n\n"+line), 0o644); err != nil {
				t.Fatal(err)
			}
			before := glossaryBlocks(mustRead(t, km))
			if len(before) != 1 {
				t.Fatalf("reader sees %d blocks, want 1", len(before))
			}
			if _, _, err := s.GlossaryWrite(Umbrella, Term{Name: "Other", Definition: "d"}, false, false); err != nil {
				return // refusing the write is the other allowed outcome
			}
			after := glossaryBlocks(mustRead(t, km))
			var names []string
			for _, b := range after {
				names = append(names, b.name)
			}
			if !strings.Contains(strings.Join(names, ","), before[0].name) {
				t.Fatalf("term %q lost by write; reader now sees %v\n%s", before[0].name, names, mustRead(t, km))
			}
			// The definition text survives too, not just the name.
			out, _, err := s.GlossaryRead(Umbrella, []string{before[0].name})
			if err != nil {
				t.Fatal(err)
			}
			for _, w := range strings.Fields(strings.TrimLeft(strings.SplitN(line, "**", 3)[2], ":—- ")) {
				if !strings.Contains(out, w) && !strings.HasPrefix(w, "<!--") && w != "-->" && !strings.Contains(w, "2026") {
					t.Errorf("definition word %q lost: %s", w, out)
				}
			}
		})
	}
}

func mustRead(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func hitFixture(t *testing.T) (Store, string) {
	t.Helper()
	s := lockedStore(t)
	km, _ := s.KnowledgePath(Umbrella)
	if err := os.WriteFile(km, []byte("# K\n\n## Arch\n\n- **bullet** — body <!-- 2026-01-01 -->\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return s, km
}

// Hit refuses a title that names no bullet and leaves no buffer row or sidecar
// entry; a known title still counts.
func TestHitRefusesUnknownBulletAndCountsKnownOne(t *testing.T) {
	s, _ := hitFixture(t)
	hp, _ := s.HitsPath(Umbrella)
	tp, _ := s.TierPath(Umbrella)
	for _, c := range []struct{ topic, title string }{{"Arch", "typo"}, {"Nope", "bullet"}} {
		_, err := s.Hit(Umbrella, c.topic, c.title)
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("hit %+v: err = %v, want ErrNotFound", c, err)
		}
		for _, p := range []string{hp, tp} {
			if _, serr := os.Stat(p); !os.IsNotExist(serr) {
				t.Fatalf("refused hit left %s (%v)", p, serr)
			}
		}
	}
	r, err := s.Hit(Umbrella, "Arch", "BULLET")
	if err != nil || r.Entry.Hits != 1 {
		t.Fatalf("known hit = %+v err=%v", r, err)
	}
}

// Both Add seams refuse the same invalid title and malformed date with the
// same error class (bulletfmt.ErrInvalid), and write nothing.
func TestAddRefusesInvalidTitleAndDate(t *testing.T) {
	s, km := hitFixture(t)
	want := mustRead(t, km)
	for _, c := range []struct{ name, title, date string }{
		{"star", "a*b", ""},
		{"newline", "a\nb", ""},
		{"stamp closer", "a --> b", ""},
		{"bad date", "ok", "2026-13-45"},
		{"short date", "ok", "26-1-1"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := s.Add(Umbrella, "Arch", c.title, "body", c.date, "")
			if !errors.Is(err, bulletfmt.ErrInvalid) {
				t.Fatalf("err = %v, want bulletfmt.ErrInvalid", err)
			}
			if got := mustRead(t, km); got != want {
				t.Fatalf("refused add wrote:\n%s", got)
			}
			if _, serr := os.Stat(filepath.Join(filepath.Dir(km), "knowledge-tier.json")); !os.IsNotExist(serr) {
				t.Fatalf("refused add touched the sidecar: %v", serr)
			}
		})
	}
}
