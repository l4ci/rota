package knowledge

import (
	"os"
	"path/filepath"
	"testing"
)

func TestQueryRenderInEveryScope(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		opts             QueryOpts
	}{
		{"empty at EOF", "", "\n", QueryOpts{}},
		{"filtered body", "\n- **Old** — hidden <!-- 2026-01-01 -->\n", "\n", QueryOpts{Tier: Confirmed}},
		{"provisional", "\n- **Old** — hidden <!-- 2026-01-01 -->\n", "\n- **Old** — hidden (provisional) <!-- 2026-01-01 -->\n", QueryOpts{}},
	} {
		for _, scope := range []string{Umbrella, "web"} {
			t.Run(tc.name+"/"+scope, func(t *testing.T) {
				s := lockedStore(t)
				s.Repos = map[string]string{"web": filepath.Join(s.Root, "web")}
				p, err := s.KnowledgePath(Umbrella)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte("## Arch"+tc.body), 0o644); err != nil {
					t.Fatal(err)
				}
				want := "## Arch\n"
				if scope == "web" {
					want += "> from: .rota/KNOWLEDGE.md\n"
				}
				want += tc.want
				out, missing, err := s.Query(scope, []string{"Arch"}, tc.opts)
				if err != nil || len(missing) != 0 || out != want {
					t.Fatalf("Query = %q, missing=%v, err=%v; want %q", out, missing, err, want)
				}
			})
		}
	}
}

func TestQueryRenderPreservesScopeOrderAndSourceTiers(t *testing.T) {
	s := lockedStore(t)
	s.Repos = map[string]string{"web": filepath.Join(s.Root, "web")}
	for scope, content := range map[string]string{
		Umbrella: "## A\n- **Shared** — a <!-- 2026-01-01 -->\n## B\n- **Shared** — b <!-- 2026-01-01 -->\n",
		"web":    "## a\n- **Local** — local <!-- 2026-01-01 -->\n",
	} {
		p, err := s.KnowledgePath(scope)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.TierSet("web", "a", "Local", Confirmed); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ scope, want string }{
		{Umbrella, "## A\n\n- **Shared** — a (provisional) <!-- 2026-01-01 -->\n\n## B\n\n- **Shared** — b (provisional) <!-- 2026-01-01 -->\n"},
		{"web", "## B\n> from: .rota/KNOWLEDGE.md\n\n- **Shared** — b (provisional) <!-- 2026-01-01 -->\n\n## A\n> from: .rota/KNOWLEDGE.md\n\n- **Shared** — a (provisional) <!-- 2026-01-01 -->\n> from: .rota/knowledge/web/KNOWLEDGE.md\n\n- **Local** — local <!-- 2026-01-01 -->\n"},
	} {
		t.Run(tc.scope, func(t *testing.T) {
			out, missing, err := s.Query(tc.scope, []string{"B", "A", "absent"}, QueryOpts{})
			if err != nil || out != tc.want || len(missing) != 1 || missing[0] != "absent" {
				t.Fatalf("Query = %q, missing=%v, err=%v; want %q", out, missing, err, tc.want)
			}
		})
	}
}
