package round

import (
	"context"
	"os"
	"testing"

	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/jsonx"
)

func TestStatusCodexTerminalWait(t *testing.T) {
	for _, c := range []struct {
		name, kind, native, fixture, want string
		capture                           bool
	}{
		{"wait", harness.Codex, "blocked", "terminal-wait", "working", true},
		{"narrow wait", harness.Codex, "blocked", "terminal-wait-narrow", "working", true},
		{"sentinel", harness.Codex, "blocked", "blocked", "blocked", true},
		{"approval", harness.Codex, "blocked", "approval", "blocked", true},
		{"capture unavailable", harness.Codex, "blocked", "", "blocked", false},
		{"empty capture", harness.Codex, "blocked", "", "blocked", true},
		{"already working", harness.Codex, "working", "", "working", false},
		{"other harness", harness.Claude, "blocked", "", "blocked", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := newRepo(t, map[string]string{"dana": "dana/2178-wait"})
			writeRegistry(t, root, slot(root, "dana", "dana/2178-wait", func(s *jsonx.Object) {
				s.Set("handle", "w1:t1")
				s.Set("kind", c.kind)
			}))
			e := env([]host.Agent{{Tab: "w1:t1", Status: c.native}}, nil)
			if c.capture {
				var pane []byte
				if c.fixture != "" {
					var err error
					pane, err = os.ReadFile("../worker/testdata/panes/codex-" + c.fixture + ".txt")
					if err != nil {
						t.Fatal(err)
					}
				}
				e.Capture = func(context.Context, string, string, int) string { return string(pane) }
			} else if c.name != "capture unavailable" {
				e.Capture = func(context.Context, string, string, int) string {
					t.Fatal("snapshot-only status must not read this pane")
					return ""
				}
			}
			rep, err := e.Status(context.Background(), root)
			if err != nil {
				t.Fatal(err)
			}
			if r, ok := rowOf(rep, "dana"); !ok || r.HostState != c.want {
				t.Fatalf("%+v, want hostState %s", r, c.want)
			}
		})
	}
}
