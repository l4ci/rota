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
	for _, chain := range []string{
		"default the issue's harness: label, then round.harnesses, then slot kind, else claude",
		"default the issue's model: label, then the tier map, then the harness default",
	} {
		if !strings.Contains(out+errOut, chain) {
			t.Errorf("help omits default chain %q: %s %s", chain, out, errOut)
		}
	}
}

func TestRoundTransferHumanRefusesWorkerFlags(t *testing.T) {
	for _, flags := range [][]string{
		{"--kind", "codex"},
		{"--model", "task-model"},
		{"--kind", "codex", "--model", "task-model"},
		{"--kind", ""},
		{"--model", ""},
	} {
		t.Run(strings.Join(flags, " "), func(t *testing.T) {
			f := newScopeFx(t, "warn", "")
			args := append([]string{"--json", "round", "transfer", "1", "--to", "human", "--holder-pid", "4242"}, flags...)
			code, out, errOut := rotaInWith(t, f.deps, f.root, args...)
			if code != 2 || !strings.Contains(out+errOut, "--kind and --model require --to <slot>, not --to human") {
				t.Fatalf("want usage refusal for worker flags with human target, got %d %s %s", code, out, errOut)
			}
			reg := worker.LoadRegistryTolerant(f.root)
			if reg.Slot("ben").HeldID() != "1" || reg.Slot("dana").HeldID() != "" {
				t.Fatal("refusal changed the slot assignments")
			}
		})
	}
}
