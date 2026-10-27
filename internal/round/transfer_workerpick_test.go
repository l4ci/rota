package round

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/worker"
)

func TestTransferHumanRefusesWorkerOverrides(t *testing.T) {
	for _, opts := range []TransferOpts{
		{To: HumanTarget, Kind: "codex"},
		{To: HumanTarget, Model: "task-model"},
		{To: HumanTarget, Kind: "codex", Model: "task-model"},
	} {
		_, _, err := opts.checkFlags(roundcfg.Settings{})
		if exitOf(err) != 2 || !strings.Contains(err.Error(), "--kind and --model require --to <slot>, not --to human") {
			t.Fatalf("want usage refusal for %+v, got %v", opts, err)
		}
	}
}

func TestTransferWorkerPickPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name, kind, model, slotKind, project, tierModel string
		labels                                          []string
		wantKind, wantModel, wantSource                 string
	}{
		{name: "flags beat labels", kind: "codex", model: "flag-model", labels: []string{"harness:claude", "model:label-model"}, tierModel: "tier-model", wantKind: "codex", wantModel: "flag-model", wantSource: KindFromFlag},
		{name: "model flag keeps labelled harness", model: "flag-model", labels: []string{"harness:codex", "model:label-model"}, tierModel: "tier-model", wantKind: "codex", wantModel: "flag-model", wantSource: KindFromLabel},
		{name: "labels beat slot and tier", slotKind: "claude", labels: []string{"harness:codex", "model:label-model"}, tierModel: "tier-model", wantKind: "codex", wantModel: "label-model", wantSource: KindFromLabel},
		{name: "tier map uses resolved harness", kind: "codex", tierModel: "tier-model", wantKind: "codex", wantModel: "tier-model", wantSource: KindFromFlag},
		{name: "project default beats slot", project: "codex", slotKind: "claude", tierModel: "tier-model", wantKind: "codex", wantModel: "tier-model", wantSource: KindFromHarnesses},
		{name: "slot kind", slotKind: "codex", tierModel: "tier-model", wantKind: "codex", wantModel: "tier-model", wantSource: KindFromSlot},
		{name: "harness default model", kind: "codex", wantKind: "codex", wantSource: KindFromFlag},
		{name: "default harness", tierModel: "tier-model", wantKind: "claude", wantModel: "tier-model", wantSource: KindFromDefault},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newMoveFx(t)
			if err := os.WriteFile(filepath.Join(f.root, ".rota", "config.json"), []byte(`{"round":{"dispatch":"herdr"}}`), 0o644); err != nil {
				t.Fatal(err)
			}
			f.host.name = host.Herdr
			if err := worker.Update(f.root, func(d *worker.Doc) { d.SetHost(host.Herdr) }); err != nil {
				t.Fatal(err)
			}
			f.wire(func(w *worker.Env) {
				w.LookPath = func(n string) (string, error) { return "/fake/" + n, nil }
				w.Run = func(_ context.Context, name string, args, _ []string) (host.Result, error) {
					if filepath.Base(name) == "codex" && strings.Join(args, " ") == "--help" {
						return host.Result{Stdout: codexHelp}, nil
					}
					return host.Result{}, nil
				}
			})
			f.env.Round.Models = map[string]map[string]string{
				tc.wantKind: {"standard": tc.tierModel},
			}
			if tc.project != "" {
				f.env.Round.Harnesses = []string{tc.project}
			}
			f.be.items["12"].Labels = tc.labels
			rawSlot(f.root, "dana", func(s *jsonx.Object) { s.Set("kind", tc.slotKind) })
			res, err := f.transfer("12", "dana", func(o *TransferOpts) { o.Kind, o.Model = tc.kind, tc.model })
			if err != nil || !res.Dispatched {
				t.Fatalf("transfer: %+v, %v", res, err)
			}
			s := f.slot("dana")
			if s.HarnessKind() != tc.wantKind || s.Model() != tc.wantModel || s.KindSource() != tc.wantSource {
				t.Fatalf("receiver: kind %q from %q model %q; want %q from %q model %q", s.HarnessKind(), s.KindSource(), s.Model(), tc.wantKind, tc.wantSource, tc.wantModel)
			}
			if !strings.HasPrefix(f.host.launch, tc.wantKind+" ") {
				t.Fatalf("wrong harness launched: %s", f.host.launch)
			}
			if tc.wantModel != "" && !strings.Contains(f.host.launch, " --model "+tc.wantModel+" ") || tc.wantModel == "" && strings.Contains(f.host.launch, " --model ") {
				t.Fatalf("wrong model launched: %s", f.host.launch)
			}
		})
	}
}
