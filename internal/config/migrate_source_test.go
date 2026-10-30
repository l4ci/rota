package config

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/jsonx"
)

func decodeCfg(t *testing.T, s string) *jsonx.Object {
	t.Helper()
	v, err := jsonx.Decode([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return v.(*jsonx.Object)
}

func compact(t *testing.T, o *jsonx.Object) string {
	t.Helper()
	b, err := jsonx.MarshalCompact(o)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func detects(p string) func() string { return func() string { return p } }

func TestMigrateSource(t *testing.T) {
	cases := []struct {
		name, in, want string
		detect         func() string
		unresolved     bool
		forge          string
	}{
		{"file", `{"backlog":{"backend":"file"}}`, `{"issues":{"source":"file"}}`, nil, false, ""},
		{"issues explicit github, origin agrees", `{"backlog":{"backend":"issues"},"issues":{"provider":"github"}}`, `{"issues":{"source":"github"}}`, detects("github"), false, ""},
		{"issues auto resolves from origin", `{"backlog":{"backend":"issues"},"issues":{"provider":"auto"}}`, `{"issues":{"source":"gitlab"}}`, detects("gitlab"), false, ""},
		{"issues provider unset resolves from origin", `{"backlog":{"backend":"issues"}}`, `{"issues":{"source":"github"}}`, detects("github"), false, ""},
		{"issues auto, nothing detected stays legacy", `{"backlog":{"backend":"issues"},"issues":{"provider":"auto"}}`, `{"backlog":{"backend":"issues"},"issues":{"provider":"auto"}}`, detects(""), true, ""},
		{"issues auto, no detector stays legacy", `{"backlog":{"backend":"issues"}}`, `{"backlog":{"backend":"issues"}}`, nil, true, ""},
		{"explicit provider the origin misses pins the forge", `{"backlog":{"backend":"issues"},"issues":{"provider":"gitlab"}}`, `{"issues":{"source":"gitlab"},"forge":{"provider":"gitlab"}}`, detects(""), false, "gitlab"},
		{"file backlog keeps its explicit forge", `{"backlog":{"backend":"file"},"issues":{"provider":"github"}}`, `{"issues":{"source":"file"},"forge":{"provider":"github"}}`, detects(""), false, "github"},
		{"provider only, backend defaults to file", `{"issues":{"provider":"auto","retryWaitSeconds":5}}`, `{"issues":{"retryWaitSeconds":5,"source":"file"}}`, nil, false, ""},
		{"source already set wins, legacy dropped", `{"backlog":{"backend":"file"},"issues":{"source":"github"}}`, `{"issues":{"source":"github"}}`, nil, false, ""},
		{"source already set, provider the origin misses pins the forge", `{"issues":{"source":"gitlab","provider":"gitlab"}}`, `{"issues":{"source":"gitlab"},"forge":{"provider":"gitlab"}}`, detects(""), false, "gitlab"},
		{"source already set, provider the origin agrees with", `{"issues":{"source":"gitlab","provider":"gitlab"}}`, `{"issues":{"source":"gitlab"}}`, detects("gitlab"), false, ""},
		{"umbrella whose sub-repos disagree is forge", `{"backlog":{"backend":"issues"},"issues":{"provider":"auto"}}`, `{"issues":{"source":"forge"}}`, detects("forge"), false, ""},
		{"other keys stay", `{"models":{"worker":"sonnet"},"backlog":{"backend":"file","x":1}}`, `{"models":{"worker":"sonnet"},"backlog":{"x":1},"issues":{"source":"file"}}`, nil, false, ""},
		{"nothing legacy", `{"models":{"worker":"sonnet"}}`, `{"models":{"worker":"sonnet"}}`, nil, false, ""},
		{"garbage backend left alone", `{"backlog":{"backend":"git"}}`, `{"backlog":{"backend":"git"}}`, nil, true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := compact(t, decodeCfg(t, tc.want))
			got, m := MigrateSource(decodeCfg(t, tc.in), tc.detect)
			if s := compact(t, got); s != want {
				t.Fatalf("got  %s\nwant %s", s, want)
			}
			if m.Unresolved != tc.unresolved {
				t.Errorf("Unresolved = %v, want %v", m.Unresolved, tc.unresolved)
			}
			if m.Forge != tc.forge {
				t.Errorf("Forge = %q, want %q", m.Forge, tc.forge)
			}
			if wantChanged := compact(t, decodeCfg(t, tc.in)) != want; m.Changed != wantChanged {
				t.Errorf("Changed = %v, want %v", m.Changed, wantChanged)
			}
			// idempotent: a second run changes nothing.
			again, m2 := MigrateSource(got, tc.detect)
			if s := compact(t, again); s != want || m2.Changed {
				t.Errorf("second run changed the config: %s changed=%v", s, m2.Changed)
			}
		})
	}
}

// Origin detection runs once per migration, however many branches read it.
func TestMigrateSourceDetectsOnce(t *testing.T) {
	calls := 0
	detect := func() string { calls++; return "github" }
	MigrateSource(decodeCfg(t, `{"backlog":{"backend":"issues"}}`), detect)
	if calls > 1 {
		t.Errorf("detect ran %d times", calls)
	}
}

func TestMigrateSourceFileSharesReadContract(t *testing.T) {
	root := writeLayers(t, `{"work":`, "-")
	_, err := MigrateSourceFile(root, detects("github"), true)
	if !errors.Is(err, ErrCorrupt) || !strings.Contains(err.Error(), "config.json") {
		t.Fatalf("corrupt: err = %v, want ErrCorrupt naming config.json", err)
	}

	root = writeLayers(t, `{}`, "-")
	if err := os.Remove(configPath(root)); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(configPath(root), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err = MigrateSourceFile(root, detects("github"), true)
	if err == nil || errors.Is(err, ErrCorrupt) || !strings.Contains(err.Error(), "config.json") {
		t.Fatalf("read error: err = %v, want a wrapped read error naming config.json", err)
	}
}
