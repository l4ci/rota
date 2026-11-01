package postgate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

func landingRepo(t *testing.T) string {
	t.Helper()
	root := gittest.NewRepo(t, "main")
	gittest.Run(t, root, "checkout", "-qb", "feature")
	if err := os.WriteFile(filepath.Join(root, "feature.txt"), []byte("feature"), 0644); err != nil {
		t.Fatal(err)
	}
	gittest.Run(t, root, "add", "feature.txt")
	gittest.Run(t, root, "commit", "-qm", "feature")
	gittest.Run(t, root, "checkout", "-q", "main")
	if err := os.MkdirAll(filepath.Join(root, ".rota"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".rota", "workers.json"), []byte(`{"slots":[{"name":"kit","branch":"feature","task":"12"}]}`), 0644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestRunReportsRefusal(t *testing.T) {
	root := landingRepo(t)
	var reports []Report
	env := Env{Worker: worker.Env{}, Report: func(_ context.Context, _ string, r Report) error { reports = append(reports, r); return nil }}
	r := env.Run(context.Background(), root, Request{Gate: &worker.GateOpts{Slot: "kit", Base: "main"}}).Gate
	if r.Err != nil || r.Result.Verdict != worker.GateNoVerify {
		t.Fatalf("gate: %+v", r)
	}
	if len(reports) != 1 {
		t.Fatalf("refusal reports = %d, want 1", len(reports))
	}
}

// reportForge exercises the real report transport protocol at its remote boundary.
type reportForge struct {
	tracker.Forge
	notes []tracker.Comment
}

func (f *reportForge) MRNotes(context.Context, int) ([]tracker.Comment, error) { return f.notes, nil }
func (f *reportForge) AddMRNote(_ context.Context, _ int, body string) (string, error) {
	f.notes = append(f.notes, tracker.Comment{ID: "1", Author: "kit", Body: body})
	return "1", nil
}
func (f *reportForge) AuthenticatedUser(context.Context) (string, error) { return "kit", nil }

func TestRunTrainReportsCulpritAndEndsLandedItems(t *testing.T) {
	root := landingRepo(t)

	gittest.Run(t, root, "checkout", "-qb", "good", "main")
	gittest.Commit(t, root, "good", "good.txt", "good")
	gittest.Run(t, root, "checkout", "-q", "main")
	if err := os.WriteFile(filepath.Join(root, ".rota", "workers.json"), []byte(`{"slots":[{"name":"good","branch":"good","task":"1"},{"name":"kit","branch":"feature","task":"12"}]}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := worker.RecordItemStart(root, "1", time.Now()); err != nil {
		t.Fatal(err)
	}
	cfg, _ := jsonx.Decode([]byte(`{"test":{"full":["fixture-check"]},"round":{"maxBounces":3}}`))
	old := classify
	classify = func(v string) worker.VerdictMeaning { m := old(v); m.Bounce = v == worker.GateVerifyFailed; return m }
	t.Cleanup(func() { classify = old })
	forge := &reportForge{}
	tmpDir := t.TempDir()
	e := Env{Worker: worker.Env{Getenv: func(key string) string {
		if key == "TMPDIR" {
			return tmpDir
		}
		return ""
	}, Config: func(string) any { return cfg }, Shell: func(_ context.Context, dir, _ string) (string, int) {
		if _, err := os.Stat(filepath.Join(dir, "feature.txt")); err == nil {
			return "broken feature", 1
		}
		return "", 0
	}}, Park: (&parked{}).park}
	e.Report = func(ctx context.Context, _ string, r Report) error {
		// The report is durable before any clocks or bounce accounting change.
		if _, ok := worker.LoadRegistryTolerant(root).ItemStart("1"); !ok {
			t.Error("landed clock cleared before report")
		}
		if bounces(root, "12") != 0 {
			t.Error("culprit counted before report")
		}
		return tracker.PostReport(ctx, forge, len(forge.notes)+1, r.Body)
	}
	req := Request{Train: &worker.TrainOpts{Targets: []string{"good", "kit"}, Base: "main", Order: []string{"good", "kit"}, LandGreen: true}}
	run := e.Run(context.Background(), root, req).Train
	if run.Err != nil || run.Refused || run.Result.Verdict != worker.GateVerifyFailed || run.Result.Culprit != "kit" || len(run.Book) != 0 {
		t.Fatalf("train: %+v", run)
	}
	if len(forge.notes) != 2 || !strings.Contains(forge.notes[0].Body, "Outcome: **pass**") || !strings.Contains(forge.notes[1].Body, "TRAIN-FAIL kit") {
		t.Fatalf("reports: %+v", forge.notes)
	}
	if _, ok := worker.LoadRegistryTolerant(root).ItemStart("1"); ok {
		t.Error("landed item clock still running")
	}
	if bounces(root, "12") != 1 || bounces(root, "1") != 0 {
		t.Fatalf("bounces culprit=%d landed=%d", bounces(root, "12"), bounces(root, "1"))
	}
	// Repeating the culprit's head does not count a second bounce.
	e.Report = nil
	run = e.Run(context.Background(), root, Request{Train: &worker.TrainOpts{Targets: []string{"kit"}, Base: "main"}}).Train
	if run.Err != nil || bounces(root, "12") != 1 {
		t.Fatalf("repeat: %+v count=%d", run, bounces(root, "12"))
	}
}

func TestRunReportFailurePreservesRefusal(t *testing.T) {
	root := landingRepo(t)
	e := Env{Worker: worker.Env{}, Report: func(context.Context, string, Report) error { return errors.New("forge unavailable") }}
	run := e.Run(context.Background(), root, Request{Gate: &worker.GateOpts{Slot: "kit", Base: "main"}}).Gate
	if run.Err != nil || !run.Refused || run.Result.Verdict != worker.GateNoVerify || len(run.Warnings) != 1 || run.Warnings[0] != "gate report pending: forge unavailable" {
		t.Fatalf("run: %+v", run)
	}
}

func TestRunPreviewsNeverReportOrBookkeep(t *testing.T) {
	for _, train := range []bool{false, true} {
		t.Run(map[bool]string{false: "gate", true: "train"}[train], func(t *testing.T) {
			root := landingRepo(t)
			if err := worker.RecordItemStart(root, "12", time.Now()); err != nil {
				t.Fatal(err)
			}
			called := false
			e := Env{Worker: worker.Env{}, Report: func(context.Context, string, Report) error { called = true; return nil }, Park: func(string, string) error { t.Fatal("preview parked"); return nil }}
			req := Request{Gate: &worker.GateOpts{Slot: "kit", Base: "main", CheckOnly: true}}
			if train {
				req = Request{Train: &worker.TrainOpts{Targets: []string{"kit"}, Base: "main", Plan: true}}
			}
			r := e.Run(context.Background(), root, req)
			if called || len(r.Gate.Warnings)+len(r.Train.Warnings) != 0 {
				t.Fatalf("preview reported: %+v", r)
			}
			if _, ok := worker.LoadRegistryTolerant(root).ItemStart("12"); !ok {
				t.Fatal("preview ended item")
			}
		})
	}
}

func TestRunRecordedPRWithoutOriginNeverReachesForge(t *testing.T) {
	root := landingRepo(t)
	if _, err := worker.UpdateSlot(root, "kit", func(s *worker.Slot) { s.SetPR("20") }); err != nil {
		t.Fatal(err)
	}
	e := Env{Worker: worker.Env{Forge: func(string, string, tracker.Settings) (worker.Forge, error) {
		t.Fatal("gate reached forge without origin")
		return nil, nil
	}}, Report: func(context.Context, string, Report) error {
		t.Fatal("report reached forge without origin")
		return nil
	}}
	r := e.Run(context.Background(), root, Request{Gate: &worker.GateOpts{Slot: "kit", Base: "main"}}).Gate
	if r.Err != nil || r.Result.Verdict != worker.GateCheckBroke {
		t.Fatalf("run: %+v", r)
	}
}
