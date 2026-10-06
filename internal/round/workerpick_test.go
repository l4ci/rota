package round

import (
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/backlog"
)

func TestPickOf(t *testing.T) {
	file := func(line string) backlog.Item { return backlog.Item{ID: "B01", Line: line} }
	issue := func(labels ...string) backlog.Item { return backlog.Item{ID: "7", Number: 7, Labels: labels} }
	for _, c := range []struct {
		name         string
		it           backlog.Item
		harness, mod string
		by, msg      string
	}{
		{name: "no labels", it: issue("type:feature")},
		{name: "harness", it: issue("harness:codex"), harness: "codex"},
		{name: "model", it: issue("model:gpt-5.5-codex"), mod: "gpt-5.5-codex"},
		{name: "both", it: issue("harness:claude", "model:claude-opus-5-5", "size:Minor"), harness: "claude", mod: "claude-opus-5-5"},
		{name: "unknown harness", it: issue("harness:gemini"), by: BlockHarnessLabel, msg: "harness:gemini"},
		{name: "two harness", it: issue("harness:claude", "harness:codex"), by: BlockHarnessLabel, msg: "harness:claude, harness:codex"},
		{name: "two model", it: issue("model:a", "model:b"), by: BlockModelLabel, msg: "model:a, model:b"},
		{name: "empty model", it: issue("model:"), by: BlockModelLabel, msg: "model:"},
		{name: "injection label", it: issue("model:x;rm -rf"), by: BlockModelLabel, msg: "not a valid model id"},
		{name: "flag-like label", it: issue("model:--yolo"), by: BlockModelLabel, msg: "not a valid model id"},
		{name: "file injection", it: file("- **[B01] x.** Model: $(id)"), by: BlockModelLabel, msg: "not a valid model id"},
		{name: "file fields", it: file("- **[B01] x.** Harness: codex Model: gpt-5.5-codex Milestone: M1"), harness: "codex", mod: "gpt-5.5-codex"},
		{name: "file unknown", it: file("- **[B01] x.** Harness: gemini"), by: BlockHarnessLabel, msg: "Harness: gemini"},
		{name: "file none", it: file("- **[B01] x.** Related: B02")},
	} {
		t.Run(c.name, func(t *testing.T) {
			p, err := PickOf(c.it)
			if c.by != "" {
				be, ok := err.(*BlockedError)
				if !ok || be.By != c.by || !strings.Contains(be.Msg, c.msg) {
					t.Fatalf("err = %v, want blockedBy %q naming %q", err, c.by, c.msg)
				}
				return
			}
			if err != nil || p.Harness != c.harness || p.Model != c.mod {
				t.Fatalf("got %+v, %v; want %s/%s", p, err, c.harness, c.mod)
			}
		})
	}
}

func TestResolveKindModel(t *testing.T) {
	// harness: --kind > label > slot kind > claude
	for _, c := range []struct{ flag, label, slot, want string }{
		{"codex", "claude", "claude", "codex"},
		{"", "codex", "claude", "codex"},
		{"", "", "codex", "codex"},
		{"", "", "", "claude"},
	} {
		if got := resolveKind(c.flag, c.label, c.slot); got != c.want {
			t.Errorf("resolveKind(%q,%q,%q) = %q, want %q", c.flag, c.label, c.slot, got, c.want)
		}
	}
	// model: --model > label > tier map
	for _, c := range []struct{ flag, label, tier, want string }{
		{"f", "l", "t", "f"}, {"", "l", "t", "l"}, {"", "", "t", "t"}, {"", "", "", ""},
	} {
		if got := resolveModel(c.flag, c.label, c.tier); got != c.want {
			t.Errorf("resolveModel(%q,%q,%q) = %q, want %q", c.flag, c.label, c.tier, got, c.want)
		}
	}
}
