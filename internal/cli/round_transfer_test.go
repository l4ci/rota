package cli

import (
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/worker"
)

func TestRoundTransferWorkerFlagsRefuseLikeAssign(t *testing.T) {
	for _, tc := range []struct {
		name, cfg, flag, value, message string
		code                            int
	}{
		{"unknown harness", "", "--kind", "unknown-harness", "kind must be", 2},
		{"unusable model", `,"commands":{"claude":"mywrap --dangerously-skip-permissions"}`, "--model", "task-model", "--model task-model cannot apply: round.commands.claude has no {model} placeholder", 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newScopeFx(t, "warn", tc.cfg)
			for _, verb := range []string{"assign", "transfer"} {
				id, target := "2", "--agent"
				if verb == "transfer" {
					id, target = "1", "--to"
				}
				code, out, errOut := rotaInWith(t, f.deps, f.root, "--json", "round", verb, id, target, "dana", "--holder-pid", "4242", tc.flag, tc.value)
				if code != tc.code || !strings.Contains(out+errOut, tc.message) {
					t.Fatalf("%s: want exit %d naming %q, got %d %s %s", verb, tc.code, tc.message, code, out, errOut)
				}
				reg := worker.LoadRegistryTolerant(f.root)
				if reg.Slot("ben").HeldID() != "1" || reg.Slot("dana").HeldID() != "" {
					t.Fatal("refusal changed the slot assignments")
				}
			}
		})
	}
}

func TestRoundTransferWorkerFlagsHelp(t *testing.T) {
	code, out, errOut := rotaIn(t, t.TempDir(), "round", "transfer", "--help")
	if code != 0 || !strings.Contains(out+errOut, "--kind") || !strings.Contains(out+errOut, "--model") {
		t.Fatalf("help: %d %s %s", code, out, errOut)
	}
}
