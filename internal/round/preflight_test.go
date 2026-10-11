package round

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const noModelPlaceholder = `{"work":{"workerCommand":"mywrap --dangerously-skip-permissions"}}`

// preflight is the one answer to "can this item go to this slot": these cases
// cross it directly, with no slot claimed and nothing dispatched.
func TestPreflight(t *testing.T) {
	for name, c := range map[string]struct {
		labels   []string
		cfg      string
		meters   map[string]bool
		in       func(*preflightIn)
		id       string
		by       string // "" means it passes
		named    string
		model    string
		applied  string
		warnings int
	}{
		"tier map model":                    {model: "sonnet", applied: "sonnet"},
		"label beats tier":                  {labels: []string{"model:opus"}, model: "opus", applied: "opus"},
		"flag beats label":                  {labels: []string{"model:opus"}, in: func(i *preflightIn) { i.FlagModel = "haiku" }, model: "haiku", applied: "haiku"},
		"label, no {model}":                 {labels: []string{"model:opus"}, cfg: noModelPlaceholder, by: BlockModelLabel, named: "model:opus"},
		"--model, no {model}":               {cfg: noModelPlaceholder, in: func(i *preflightIn) { i.FlagModel = "opus" }, by: BlockModelLabel, named: "--model opus"},
		"tier model, no {model} only warns": {cfg: noModelPlaceholder, model: "sonnet", applied: "", warnings: 1},
		"outside the scope":                 {id: "20", by: BlockOutOfScope},
		"outside the scope, resuming":       {id: "20", in: func(i *preflightIn) { i.Resuming = true }, model: "sonnet", applied: "sonnet"},
		"every account cooling":             {cfg: twoAccounts, meters: map[string]bool{"a": true, "b": true}, by: BlockQuota},
		"every account cooling, resuming": {cfg: twoAccounts, meters: map[string]bool{"a": true, "b": true},
			in: func(i *preflightIn) { i.Resuming = true }, model: "sonnet", applied: "sonnet"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newAssignFixture(t)
			f.set.Models = map[string]map[string]string{"claude": {"standard": "sonnet"}}
			if c.cfg != "" {
				f.config(t, c.cfg)
				f.set.Models = map[string]map[string]string{"claude": {"standard": "sonnet"}}
			}
			if c.meters != nil {
				f.meters(t, c.meters)
			}
			f.be.add("20", "Elsewhere", "M09", false, "## Acceptance\n- [ ] x")
			id := firstNonEmpty(c.id, "12")
			f.labelled(id, c.labels...)
			in := preflightIn{Item: f.be.items[id], Settings: f.set, Tier: "standard"}
			if c.in != nil {
				c.in(&in)
			}
			out, err := f.env.preflight(bg, f.root, f.be, in)
			if c.by != "" {
				if by := blockedBy(t, err); by != c.by || !strings.Contains(err.Error(), c.named) {
					t.Fatalf("want blockedBy %q naming %q, got %v", c.by, c.named, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if out.Model != c.model || out.AppliedModel != c.applied || len(out.Warnings) != c.warnings {
				t.Errorf("model %q applied %q warnings %v, want %q %q and %d warnings", out.Model, out.AppliedModel, out.Warnings, c.model, c.applied, c.warnings)
			}
		})
	}
}

// Transfer asks the same questions as Assign: the receiver is refused before
// anything moves, and starts on the item's model: label.
func TestTransferPreflightMatchesAssign(t *testing.T) {
	t.Run("model label binds the receiver", func(t *testing.T) {
		f := newMoveFx(t)
		f.set.Models = map[string]map[string]string{"claude": {"standard": "sonnet"}}
		f.be.items["12"].Labels = []string{"model:opus"}
		if _, err := f.transfer("12", "dana", nil); err != nil {
			t.Fatal(err)
		}
		if m := f.slot("dana").Model(); m != "opus" {
			t.Errorf("receiver model = %q, want the label's opus", m)
		}
	})
	t.Run("a label the launch line cannot carry is refused", func(t *testing.T) {
		f := newMoveFx(t)
		if err := os.WriteFile(filepath.Join(f.root, ".rota", "config.json"), []byte(noModelPlaceholder), 0o644); err != nil {
			t.Fatal(err)
		}
		f.be.items["12"].Labels = []string{"model:opus"}
		_, err := f.transfer("12", "dana", nil)
		if by := blockedBy(t, err); by != BlockModelLabel {
			t.Fatalf("got %v", err)
		}
		if f.be.claims["12"] != "ben@1" || f.slot("dana").Task() != "" {
			t.Errorf("a refusal moves nothing: %v", f.be.claims)
		}
	})
	t.Run("an item outside the scope is refused", func(t *testing.T) {
		f := newMoveFx(t)
		f.be.items["12"].Fields.Milestone = "M09"
		_, err := f.transfer("12", "dana", nil)
		if by := blockedBy(t, err); by != BlockOutOfScope {
			t.Fatalf("got %v", err)
		}
		if f.be.claims["12"] != "ben@1" || f.slot("dana").Task() != "" {
			t.Errorf("a refusal moves nothing: %v", f.be.claims)
		}
	})
	t.Run("to the human needs none of it", func(t *testing.T) {
		f := newMoveFx(t)
		f.be.items["12"].Fields.Milestone = "M09"
		f.be.items["12"].Labels = []string{"model:a", "model:b"}
		if _, err := f.transfer("12", HumanTarget, nil); err != nil {
			t.Fatal(err)
		}
	})
}
