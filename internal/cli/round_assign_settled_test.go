package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/host"
)

func TestRoundAssignSettledFile(t *testing.T) {
	for _, flag := range []string{"--settled-file", "--body-file"} {
		t.Run(flag, func(t *testing.T) {
			f := newScopeFx(t, "warn", "")
			h := &cliHost{inSession: true}
			f.deps.Host = func(host.Kind) host.Host { return h }
			useHost(f.deps, h)
			file := filepath.Join(t.TempDir(), "answers.md")
			if err := os.WriteFile(file, []byte("m: Keep the callback signature.\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			code, out, errOut := rotaInWith(t, f.deps, f.root, "round", "assign", "2", "--agent", "dana", "--holder-pid", "4242", "--accept-overlap", flag, file)
			if code != 0 {
				t.Fatalf("assign: %d %s %s", code, out, errOut)
			}
			brief := h.calls[len(h.calls)-1]
			if !strings.Contains(brief, "Settled answers (verbatim):\n\nm: Keep the callback signature.") {
				t.Fatalf("answers missing from brief: %s", brief)
			}
		})
	}
}

func TestRoundAssignSettledFileHelp(t *testing.T) {
	code, out, errOut := rotaIn(t, t.TempDir(), "round", "assign", "--help")
	if code != 0 || !strings.Contains(out+errOut, "--settled-file") || !strings.Contains(out+errOut, "deprecated alias for --settled-file") {
		t.Fatalf("help: %d %s %s", code, out, errOut)
	}
}
