package knowledge_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/extknowledge"
	"github.com/l4ci/rota/internal/knowledge"
)

type hitStore struct {
	hit                func(string) (string, int, error)
	read               func() (string, int, string, error)
	log                func() string
	notFound, multiple error
	wantLine           string
}

// Exercise the append/read contract at both public store seams. The log
// adapters deliberately retain their distinct formats (external adds project).
func TestHitAppendReadContract(t *testing.T) {
	for _, store := range []string{"knowledge", "extknowledge"} {
		for _, tc := range []struct {
			name, bullets, typed string
			refusal              string
		}{
			{"stored spelling", "- **Foo** — body <!-- 2026-01-01 -->\n", " FOO ", ""},
			{"padded title", "- ** Foo ** — body <!-- 2026-01-01 -->\n", "foo", ""},
			{"unknown", "- **Foo** — body <!-- 2026-01-01 -->\n", "typo", "missing"},
			{"case twins", "- **Foo** — a <!-- 2026-01-01 -->\n- **FOO** — b <!-- 2026-01-01 -->\n", "foo", "multiple"},
		} {
			t.Run(store+"/"+tc.name, func(t *testing.T) {
				s := newHitStore(t, store, tc.bullets)
				if tc.refusal != "" {
					want := s.notFound
					if tc.refusal == "multiple" {
						want = s.multiple
					}
					if _, _, err := s.hit(tc.typed); !errors.Is(err, want) {
						t.Fatalf("Hit(%q) error = %v, want %v", tc.typed, err, want)
					}
					if _, _, err := s.hit(tc.typed); !strings.Contains(err.Error(), "Arch") {
						t.Fatalf("Hit(%q) error %q does not name the topic", tc.typed, err)
					}
					if _, err := os.Stat(s.log()); !os.IsNotExist(err) {
						t.Fatalf("refused hit left a log: %v", err)
					}
					return
				}
				for i := 1; i <= 2; i++ {
					title, n, err := s.hit(tc.typed)
					if err != nil || title != "Foo" || n != i {
						t.Fatalf("Hit(%q) = %q, %d, %v; want Foo, %d", tc.typed, title, n, err, i)
					}
					// Read through a fresh adapter value, not the Hit result.
					title, n, date, err := s.read()
					if err != nil || title != "Foo" || n != i || date != "2026-11-18" {
						t.Fatalf("read = %q, %d, %q, %v", title, n, date, err)
					}
					raw, err := os.ReadFile(s.log())
					if err != nil || string(raw) != strings.Repeat(s.wantLine+"\n", i) {
						t.Fatalf("log = %q, %v; want %d unchanged-format rows", raw, err, i)
					}
				}
			})
		}
	}
}

func newHitStore(t *testing.T, kind, bullets string) hitStore {
	t.Helper()
	root := t.TempDir()
	clock := func() time.Time { return time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC) }
	write := func(rel, text string) {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const line = `{"topic":"Arch","title":"Foo","date":"2026-11-18"}`
	if kind == "knowledge" {
		write(".rota/KNOWLEDGE.md", "## Arch\n\n"+bullets)
		s := knowledge.Store{Root: root, Now: clock}
		log, err := s.HitsPath(knowledge.Umbrella)
		if err != nil {
			t.Fatal(err)
		}
		return hitStore{
			hit: func(title string) (string, int, error) {
				r, err := s.Hit(knowledge.Umbrella, "Arch", title)
				return r.Entry.Title, r.Entry.Hits, err
			},
			read: func() (string, int, string, error) {
				e, _, err := (knowledge.Store{Root: root}).TierGet(knowledge.Umbrella, "Arch", "Foo")
				return e.Title, e.Hits, e.LastSeen, err
			},
			log: func() string { return log }, notFound: knowledge.ErrNotFound, multiple: knowledge.ErrMultiMatch, wantLine: line,
		}
	}
	write("clone/README.md", "- [Arch](topics/arch.md) — architecture\n")
	write("clone/topics/arch.md", "# Arch\n\n"+bullets)
	clone := func() extknowledge.Clone {
		return extknowledge.Clone{Dir: filepath.Join(root, "clone"), Env: extknowledge.Env{StateDir: filepath.Join(root, "state"), Now: clock}}
	}
	// The file name is private; discover only this fixture's state log.
	log := func() string {
		paths, _ := filepath.Glob(filepath.Join(root, "state", "external-knowledge", "*.hits.jsonl"))
		if len(paths) == 0 {
			return filepath.Join(root, "state", "no-log")
		}
		if len(paths) != 1 {
			t.Fatalf("hit logs = %v", paths)
		}
		return paths[0]
	}
	return hitStore{
		hit: func(title string) (string, int, error) { return clone().Hit("Arch", title, "project") },
		read: func() (string, int, string, error) {
			rows, _, err := clone().StatsWithHits()
			if err != nil || len(rows) != 1 || len(rows[0].Items) != 1 {
				return "", 0, "", fmt.Errorf("stats = %+v, %v", rows, err)
			}
			b := rows[0].Items[0]
			return b.Title, b.Hits, b.LastHit, nil
		},
		log: log, notFound: extknowledge.ErrNotFound, multiple: extknowledge.ErrMultiMatch,
		wantLine: strings.TrimSuffix(line, "}") + `,"project":"project"}`,
	}
}
