package worker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGateVacuousRefusesTestsThatPassWithoutChange(t *testing.T) {
	w := newWorld(t, ghURL)
	w.setConfig(`{"test":{"full":["true"],"vacuousCheck":true,"filePattern":"\\.test$","vacuousCommand":"sh {files}"}}`)
	if err := os.WriteFile(filepath.Join(w.worker, "work.test"), []byte("test 1 = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitq(t, w.worker, "add", "work.test")
	gitq(t, w.worker, "commit", "-qm", "add vacuous test")
	gitq(t, w.worker, "push", "-q", "origin", "w1")
	w.forge("sha", gitq(t, w.worker, "rev-parse", "HEAD"))
	res, err := w.gate(false, GateOpts{})
	if err != nil || res.Verdict != "vacuous-tests" || res.Changed || !strings.Contains(res.Err, "work.test") {
		t.Fatalf("want vacuous-tests naming work.test with nothing landed, got %+v, %v", res, err)
	}
	if w.onOriginMain("work.txt") || w.forgeWord("state") != "OPEN" {
		t.Fatal("vacuous PR landed")
	}
	if trees := w.scratchTrees(); len(trees) != 0 {
		t.Fatalf("scratch trees left: %v", trees)
	}
}

func TestTrainVacuousNamesMemberAndLandsNothing(t *testing.T) {
	w := trainWorld(t, "true", "b1", "b2")
	w.setConfig(`{"test":{"full":["true"],"vacuousCheck":true,"filePattern":"\\.test$","vacuousCommand":"sh {files}"}}`)
	gitq(t, w.dir, "checkout", "-q", "b2")
	trainWrite(t, w, "work.test", "test -f b1.txt") // depends only on an earlier member
	gitq(t, w.dir, "checkout", "-q", "main")
	res, err := w.train(TrainOpts{Targets: []string{"b1", "b2"}, Order: []string{"b1", "b2"}})
	if err != nil || res.Verdict != "vacuous-tests" || res.Culprit != "b2" || res.Changed || len(res.Landed) != 0 {
		t.Fatalf("want vacuous b2 and nothing landed, got %+v, %v", res, err)
	}
	if !strings.Contains(res.Err, "work.test") {
		t.Fatalf("tests missing from refusal: %s", res.Err)
	}
}

func TestGateVacuousOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name, body, cfg, verdict, note string
		removeWork                     bool
	}{
		{name: "dependent", body: "test -f work.txt", verdict: GatePass, note: "VACUOUS-PASS"},
		{name: "already red", body: "false", verdict: GateVerifyFailed},
		{name: "disabled", body: "true", cfg: `{"test":{"full":["true"]}}`, verdict: GatePass},
		{name: "no test changes", verdict: GatePass, note: "VACUOUS-SKIP"},
		{name: "only tests", body: "true", removeWork: true, verdict: GateVacuousTests},
		{name: "docs and skills only", removeWork: true, verdict: GatePass, note: "VACUOUS-SKIP"},
		{name: "bad pattern", body: "true", cfg: `{"test":{"full":["true"],"vacuousCheck":true,"filePattern":"["}}`, verdict: GateCheckBroke},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t, ghURL)
			cfg := `{"test":{"full":["true"],"vacuousCheck":true,"filePattern":"\\.test$","vacuousCommand":"sh {files}"}}`
			if tc.cfg != "" {
				cfg = tc.cfg
			}
			w.setConfig(cfg)
			if tc.removeWork {
				gitq(t, w.worker, "rm", "work.txt")
			}
			if tc.body != "" {
				// Spaces and quotes must stay one shell argument.
				if err := os.WriteFile(filepath.Join(w.worker, "work's check.test"), []byte(tc.body+"\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				gitq(t, w.worker, "add", "work's check.test")
			}
			if tc.name == "docs and skills only" {
				for _, file := range []string{"docs/guide.md", "skills/example/SKILL.md"} {
					if err := os.MkdirAll(filepath.Dir(filepath.Join(w.worker, file)), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(w.worker, file), []byte("# guide\n"), 0o644); err != nil {
						t.Fatal(err)
					}
					gitq(t, w.worker, "add", file)
				}
			}
			gitq(t, w.worker, "commit", "--allow-empty", "-qm", "test outcome")
			gitq(t, w.worker, "push", "-q", "origin", "w1")
			w.forge("sha", gitq(t, w.worker, "rev-parse", "HEAD"))
			res, err := w.gate(false, GateOpts{})
			if err != nil || res.Verdict != tc.verdict {
				t.Fatalf("%+v %v", res, err)
			}
			if tc.note != "" && !strings.Contains(strings.Join(res.Notes, "\n"), tc.note) {
				t.Fatalf("missing %s in %v", tc.note, res.Notes)
			}
			if tc.verdict != GatePass && (res.Changed || w.forgeWord("state") != "OPEN") {
				t.Fatal("failing check landed")
			}
			if tc.verdict == GatePass && !res.Changed {
				t.Fatal("passing check did not land")
			}
			if trees := w.scratchTrees(); len(trees) != 0 {
				t.Fatalf("left scratch trees: %v", trees)
			}
		})
	}
}

func TestGateVacuousGoDefaultRunsSelectedTests(t *testing.T) {
	for _, dependent := range []bool{false, true} {
		name := "vacuous"
		if dependent {
			name = "dependent"
		}
		t.Run(name, func(t *testing.T) {
			w := newWorld(t, ghURL)
			trainWrite(t, w, "go.mod", "module example.com/check\n\ngo 1.22")
			trainWrite(t, w, "feature.go", "package check\nfunc answer() int { return 1 }")
			// An unrelated existing test must not stand in for the changed test file.
			trainWrite(t, w, "old_test.go", "package check\nimport \"testing\"\nfunc TestUnrelated(t *testing.T) { t.Fatal(\"do not run\") }")
			gitq(t, w.dir, "push", "-q", "origin", "main")
			gitq(t, w.worker, "fetch", "-q", "origin", "main")
			gitq(t, w.worker, "merge", "-q", "origin/main", "-m", "sync")
			source := "package check\nimport \"testing\"\nfunc TestAnswer(t *testing.T) { if 1 != 1 { t.Fatal(\"bad\") } }\n"
			if dependent {
				source = "package check\nimport \"testing\"\nfunc TestAnswer(t *testing.T) { if answer() != 2 { t.Fatal(\"want 2\") } }\n"
			}
			for file, body := range map[string]string{"feature.go": "package check\nfunc answer() int { return 2 }\n", "feature_test.go": source} {
				if err := os.WriteFile(filepath.Join(w.worker, file), []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
				gitq(t, w.worker, "add", file)
			}
			gitq(t, w.worker, "commit", "-qm", "add selected test")
			gitq(t, w.worker, "push", "-q", "origin", "w1")
			w.forge("sha", gitq(t, w.worker, "rev-parse", "HEAD"))
			w.setConfig(`{"test":{"full":["true"],"vacuousCheck":true}}`)
			res, err := w.gate(false, GateOpts{})
			want := GateVacuousTests
			if dependent {
				want = GatePass
			}
			if err != nil || res.Verdict != want {
				t.Fatalf("%+v %v", res, err)
			}
			if dependent && !w.onOriginMain("feature_test.go") {
				t.Fatal("dependent test did not land")
			}
		})
	}
}

func TestGateVacuousRestoresModifiedDeletedAndRenamedFiles(t *testing.T) {
	for _, change := range []string{"modify", "delete", "rename"} {
		t.Run(change, func(t *testing.T) {
			w := newWorld(t, ghURL)
			trainWrite(t, w, "work.test", "true")
			gitq(t, w.dir, "push", "-q", "origin", "main")
			gitq(t, w.worker, "fetch", "-q", "origin", "main")
			gitq(t, w.worker, "merge", "-q", "origin/main", "-m", "sync")
			body := ""
			switch change {
			case "modify":
				if err := os.WriteFile(filepath.Join(w.worker, "seed.txt"), []byte("changed\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				gitq(t, w.worker, "add", "seed.txt")
				body = `test "$(cat seed.txt)" = changed`
			case "delete":
				gitq(t, w.worker, "rm", "seed.txt")
				body = "test ! -f seed.txt"
			case "rename":
				gitq(t, w.worker, "mv", "seed.txt", "moved.txt")
				body = "test -f moved.txt && test ! -f seed.txt"
			}
			if err := os.WriteFile(filepath.Join(w.worker, "work.test"), []byte(body+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			gitq(t, w.worker, "add", "work.test")
			gitq(t, w.worker, "commit", "-qm", "change production and test")
			gitq(t, w.worker, "push", "-q", "origin", "w1")
			w.forge("sha", gitq(t, w.worker, "rev-parse", "HEAD"))
			w.setConfig(`{"test":{"full":["true"],"vacuousCheck":true,"filePattern":"\\.test$","vacuousCommand":"sh {files}"}}`)
			res, err := w.gate(false, GateOpts{})
			if err != nil || res.Verdict != GatePass || !res.Changed || !strings.Contains(strings.Join(res.Notes, "\n"), "VACUOUS-PASS") {
				t.Fatalf("%+v %v", res, err)
			}
		})
	}
}

func TestTrainVacuousDependentMemberPasses(t *testing.T) {
	w := trainWorld(t, "true", "b1", "b2")
	gitq(t, w.dir, "checkout", "-q", "b2")
	trainWrite(t, w, "work.test", "test -f b1.txt && test -f b2.txt")
	gitq(t, w.dir, "checkout", "-q", "main")
	w.setConfig(`{"test":{"full":["true"],"vacuousCheck":true,"filePattern":"\\.test$","vacuousCommand":"sh {files}"}}`)
	res, err := w.train(TrainOpts{Targets: []string{"b1", "b2"}, Order: []string{"b1", "b2"}})
	if err != nil || res.Verdict != GatePass || strings.Join(res.Landed, ",") != "b1,b2" {
		t.Fatalf("%+v %v", res, err)
	}
	if !strings.Contains(strings.Join(res.Notes, "\n"), "b2: VACUOUS-PASS") {
		t.Fatalf("missing pass note: %v", res.Notes)
	}
}
