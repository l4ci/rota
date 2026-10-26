package extknowledge

import (
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
)

func TestStatsCountsBulletsAndBytes(t *testing.T) {
	f := newFixture(t, "")
	gittest.Write(t, f.a, "README.md", seedIndex+"- [Godot](topics/godot.md) — engine\n- [Ghost](topics/ghost.md) — not written\n")
	godot := "# Godot\n\n- **A** — one. <!-- 2026-01-01 -->\n- **B** — two.\n"
	gittest.Write(t, f.a, "topics/godot.md", godot)
	got, err := f.clone().Stats()
	if err != nil {
		t.Fatal(err)
	}
	want := []TopicStat{
		{Name: "Go", File: "topics/go.md", Bullets: 1, Bytes: len(seedGo)},
		{Name: "Godot", File: "topics/godot.md", Bullets: 2, Bytes: len(godot)},
	}
	if len(got) != len(want) {
		t.Fatalf("stats = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("stats[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestLimitsOver(t *testing.T) {
	stats := []TopicStat{{Name: "Big", Bullets: 26, Bytes: 100}, {Name: "Fat", Bullets: 3, Bytes: 9000}, {Name: "Ok", Bullets: 3, Bytes: 100}}
	for _, tc := range []struct {
		name string
		l    Limits
		want string
	}{
		{"bullets", Limits{Bullets: 25, Bytes: 8192, Topics: 60}, "Big (26 bullets, limit 25); Fat (9000 bytes, limit 8192)"},
		{"topics", Limits{Topics: 2}, "index has 3 topics (limit 2)"},
		{"zero disables", Limits{}, ""},
		{"under", Limits{Bullets: 30, Bytes: 10000, Topics: 5}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := strings.Join(tc.l.Over(stats), "; "); got != tc.want {
				t.Fatalf("Over = %q, want %q", got, tc.want)
			}
		})
	}
}
