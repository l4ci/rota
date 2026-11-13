package postgate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

func TestRunReturnsRegistryErrorBeforeStartingTrain(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(worker.RegistryPath(root)), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(worker.RegistryPath(root), []byte(`{"slots":`), 0644); err != nil {
		t.Fatal(err)
	}
	called := false
	e := Env{Worker: worker.Env{Git: func(context.Context, string, ...string) (git.Result, error) {
		called = true
		return git.Result{}, errors.New("train should not start")
	}}}
	run := e.Run(context.Background(), root, Request{Train: &worker.TrainOpts{Targets: []string{"kit"}}}).Train
	if !errors.Is(run.Err, fsio.ErrUnreadable) || !strings.Contains(run.Err.Error(), "postgate: load train targets") || called {
		t.Fatalf("want registry error before train starts; got err=%v, git called=%v", run.Err, called)
	}
}

// The gate's process boundary is fake: this exercises Run's bookkeeping port
// without creating a git repository or running git. The worker still reads its
// own registry, so only that input lives on disk.
func TestRunUsesBookWithoutGit(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(worker.RegistryPath(root)), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(worker.RegistryPath(root), []byte(`{"slots":[{"name":"kit","branch":"feature","task":"12"}]}`), 0644); err != nil {
		t.Fatal(err)
	}
	b := &fakeBook{}
	cfg, _ := jsonx.Decode([]byte(`{"forge":{"provider":"local"}}`))
	e := Env{Book: b, Worker: worker.Env{
		Config: func(string) any { return cfg },
		Git: func(_ context.Context, _ string, args ...string) (git.Result, error) {
			switch strings.Join(args, " ") {
			case "rev-parse --git-common-dir":
				return git.Result{Stdout: filepath.Join(root, ".git")}, nil
			case "rev-parse --verify --quiet main", "rev-parse --verify --quiet feature", "rev-parse --short=7 feature":
				return git.Result{Stdout: "abcdef0"}, nil
			case "merge-base --is-ancestor main feature":
				return git.Result{ExitCode: 1}, nil
			case "merge-base --is-ancestor feature main":
				return git.Result{}, nil
			case "rev-list --count feature..main":
				return git.Result{Stdout: "1"}, nil
			default:
				t.Fatalf("unexpected git command: %v", args)
				return git.Result{}, nil
			}
		},
	}}
	run := e.Run(context.Background(), root, Request{Gate: &worker.GateOpts{Slot: "kit", Base: "main", HoldsLandLock: true}}).Gate
	if run.Err != nil || run.Refused || run.Result.Verdict != worker.GateStale || run.BookErr != nil {
		t.Fatalf("gate: %+v", run)
	}
	if len(b.calls) != 0 || len(b.posts) != 1 || b.posts[0].branch != "feature" || b.posts[0].head != "abcdef0" {
		t.Fatalf("bookkeeping: calls=%v posts=%+v", b.calls, b.posts)
	}
}

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

// observedBook retains coverage of the real registry adapter while checking
// that the train goes through the supplied port.
type observedBook struct {
	Book
	cleared []string
}

func (b *observedBook) ClearBounces(root, issue string) error {
	b.cleared = append(b.cleared, issue)
	return b.Book.ClearBounces(root, issue)
}

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
	}}}
	book := &observedBook{Book: WorkerBook{e.Worker}}
	e.Book = book
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
	if len(book.cleared) != 1 || book.cleared[0] != "1" {
		t.Errorf("injected Book cleared %v, want only landed item 1", book.cleared)
	}
	// A failed verify counts no bounce: only a gate pass or a review does.
	if bounces(root, "12") != 0 || bounces(root, "1") != 0 {
		t.Fatalf("bounces culprit=%d landed=%d", bounces(root, "12"), bounces(root, "1"))
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
			e := Env{Worker: worker.Env{}, Report: func(context.Context, string, Report) error { called = true; return nil }}
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

func bounces(root, issue string) int {
	return worker.LoadRegistryTolerant(root).Bounces(issue)
}
