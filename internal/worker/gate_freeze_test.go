package worker

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestGateFreezeAfterLandedFailure(t *testing.T) {
	w := newWorld(t, ghURL)
	w.setConfig(`{"test":{"full":["test ! -f other.txt"]}}`)
	w.forge("pushBeforeMerge", "other.txt")
	res, err := w.gate(false, GateOpts{})
	if err != nil || res.Verdict != GateVerifyFailed || !res.Changed {
		t.Fatalf("gate: %+v %v", res, err)
	}
	b, err := os.ReadFile(filepath.Join(w.dir, ".git", "rota", "freeze.json"))
	if err != nil {
		t.Fatalf("landed failure must persist a freeze: %v", err)
	}
	var f struct {
		Active      bool
		Base, SHA   string
		PRs, Checks []string
	}
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	if !f.Active || f.Base != "main" || f.SHA != gitq(t, w.dir, "rev-parse", "HEAD") || len(f.PRs) != 1 || f.PRs[0] != ghURL || len(f.Checks) == 0 {
		t.Fatalf("freeze must identify landed PR, failed checks and full base SHA: %s", b)
	}
}

func TestGateFreezeRefusesLanding(t *testing.T) {
	w := newWorld(t, ghURL)
	p := filepath.Join(w.dir, ".git", "rota")
	if err := os.MkdirAll(p, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "freeze.json"), []byte(`{"active":true,"base":"main","sha":"red","prs":["old"],"checks":["false"],"repairIssue":"99"}`), 0644); err != nil {
		t.Fatal(err)
	}
	res, err := w.gate(false, GateOpts{})
	if err != nil || res.Verdict != "freeze" || res.Changed {
		t.Fatalf("frozen gate must refuse without landing: %+v %v", res, err)
	}
}

func TestTrainFreezeRefusesUnrelatedMembers(t *testing.T) {
	w := trainWorld(t, "true", "b1", "b2")
	p := filepath.Join(w.dir, ".git", "rota")
	if err := os.MkdirAll(p, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "freeze.json"), []byte(`{"active":true,"base":"main","sha":"red","repairIssue":"99"}`), 0644); err != nil {
		t.Fatal(err)
	}
	res, err := w.train(TrainOpts{Targets: []string{"b1", "b2"}})
	if err != nil || res.Verdict != "freeze" || res.Changed {
		t.Fatalf("frozen train must refuse: %+v %v", res, err)
	}
}

func TestGateFreezeRepairCanLandAndClear(t *testing.T) {
	w := newWorld(t, ghURL)
	e := w.env(false).WithDefaults()
	// A repair PR number names the same PR as its full URL.
	p := filepath.Join(w.dir, ".git", "rota", "freeze.json")
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(`{"active":true,"base":"main","sha":"red","repairPR":"7"}`), 0644); err != nil {
		t.Fatal(err)
	}
	res, err := e.Gate(bg, w.dir, GateOpts{Slot: "w1", Base: "main"})
	if err != nil || res.Verdict != GatePass || !res.Changed {
		t.Fatalf("repair gate: %+v %v", res, err)
	}
	b, _ := os.ReadFile(p)
	var f struct {
		Active                bool
		ClearedBy, ClearedSHA string
	}
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	if f.Active || f.ClearedBy == "" || f.ClearedSHA != gitq(t, w.dir, "rev-parse", "HEAD") {
		t.Fatalf("verified repair did not clear with provenance: %s", b)
	}
}

func TestGateFreezeScratchFailureDoesNotFreeze(t *testing.T) {
	w := newWorld(t, ghURL)
	w.setConfig(`{"test":{"full":["false"]}}`)
	res, err := w.gate(false, GateOpts{})
	if err != nil || res.Verdict != GateVerifyFailed || res.Changed {
		t.Fatalf("scratch gate: %+v %v", res, err)
	}
	if _, err := os.Stat(filepath.Join(w.dir, ".git", "rota", "freeze.json")); !os.IsNotExist(err) {
		t.Fatalf("scratch failure must not freeze: %v", err)
	}
}

func TestTrainFreezeAfterLandedFailure(t *testing.T) {
	w := newWorld(t, ghURL)
	w.setConfig(`{"test":{"full":["test ! -f other.txt"]}}`)
	w.forge("pushBeforeMerge", "other.txt")
	res, err := w.train(TrainOpts{Targets: []string{"w1"}})
	if err != nil || res.Verdict != GateVerifyFailed || !res.Changed {
		t.Fatalf("landed train: %+v %v", res, err)
	}
	b, err := os.ReadFile(filepath.Join(w.dir, ".git", "rota", "freeze.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Active      bool
		PRs, Checks []string
		SHA         string
	}
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	if !f.Active || len(f.PRs) != 1 || f.PRs[0] != ghURL || len(f.Checks) == 0 || f.SHA != gitq(t, w.dir, "rev-parse", "HEAD") {
		t.Fatalf("train freeze: %s", b)
	}
}

func TestTrainFreezeRepairClearsOnlyWithVerification(t *testing.T) {
	for _, skip := range []bool{false, true} {
		t.Run(map[bool]string{false: "verified", true: "skipped"}[skip], func(t *testing.T) {
			w := newWorld(t, ghURL)
			p := filepath.Join(w.dir, ".git", "rota", "freeze.json")
			if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(`{"active":true,"base":"main","sha":"red","repairPR":"7"}`), 0644); err != nil {
				t.Fatal(err)
			}
			if skip {
				res, err := w.gate(false, GateOpts{NoVerify: true})
				if err != nil || res.Verdict != GatePass {
					t.Fatalf("skipped repair: %+v %v", res, err)
				}
			} else {
				res, err := w.train(TrainOpts{Targets: []string{"w1"}})
				if err != nil || res.Verdict != GatePass {
					t.Fatalf("train repair: %+v %v", res, err)
				}
			}
			b, _ := os.ReadFile(p)
			var f struct{ Active bool }
			if err := json.Unmarshal(b, &f); err != nil {
				t.Fatal(err)
			}
			if f.Active != skip {
				t.Fatalf("active=%v skip=%v: %s", f.Active, skip, b)
			}
		})
	}
}
