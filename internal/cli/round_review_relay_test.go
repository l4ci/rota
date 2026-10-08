package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/roundwatch"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/verdict"
	"github.com/l4ci/rota/internal/worker"
)

// reviewForge answers the GitHub calls of the review poll for PR 9: one
// reviewer comment on the thread, no reviews, and POSTs recorded.
type reviewForge struct {
	comments []string // thread comment bodies, oldest first
	posted   []string
	calls    []string
	// failReads makes the thread read fail.
	failReads bool
	prState   string // the PR's state; "" is OPEN
}

func (f *reviewForge) exec(_ context.Context, _ string, name string, args []string, _ []byte) ([]byte, []byte, int, error) {
	line := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, line)
	switch {
	case strings.HasPrefix(line, "gh pr view 9"):
		state := f.prState
		if state == "" {
			state = "OPEN"
		}
		return []byte(`{"headRefName":"nia/577-x","headRefOid":"abc123","baseRefName":"main","state":"` + state + `"}`), nil, 0, nil
	case strings.Contains(line, "-X POST"):
		for _, a := range args {
			if b, ok := strings.CutPrefix(a, "body="); ok {
				f.posted = append(f.posted, b)
			}
		}
		return []byte(`{"id":77}`), nil, 0, nil
	case strings.Contains(line, "issues/comments/77"):
		return []byte(`{"id":77,"html_url":"https://github.com/o/r/pull/9#issuecomment-77"}`), nil, 0, nil
	case f.failReads && strings.Contains(line, "issues/9/comments"):
		return nil, []byte("boom"), 1, nil
	case strings.Contains(line, "issues/9/comments"):
		var rows []string
		for i, b := range f.comments {
			bj := strings.ReplaceAll(strings.ReplaceAll(b, `\`, `\\`), "\n", `\n`)
			bj = strings.ReplaceAll(bj, `"`, `\"`)
			rows = append(rows, `{"id":`+string(rune('1'+i))+`,"body":"`+bj+`","user":{"login":"rev"},"created_at":"2026-10-08T10:0`+string(rune('0'+i))+`:00Z"}`)
		}
		return []byte("[" + strings.Join(rows, ",") + "]"), nil, 0, nil
	case strings.Contains(line, "pulls/9/reviews"), strings.Contains(line, "pulls/9/comments"):
		return []byte("[]"), nil, 0, nil
	}
	return nil, []byte("unexpected call: " + line), 2, nil
}

func useReviewForge(f *reviewForge) *Deps {
	deps := testDeps()
	deps.TrackerOptions = []tracker.Option{tracker.WithExec(f.exec, func(n string) (string, error) { return "/fake/" + n, nil })}
	return deps
}

// doneSlot seeds slot nia as done on issue 577 with PR 9.
func doneSlot(t *testing.T, dir string) {
	t.Helper()
	write := func(p, s string) {
		if err := os.WriteFile(filepath.Join(dir, p), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(".rota", "workers.json"), `{"slots":[{"name":"nia","task":"577","branch":"nia/577-x","state":"done","pr":"https://github.com/o/r/pull/9"}]}`)
}

func TestRoundReviewRelayNothingToRelay(t *testing.T) {
	dir := workerProject(t, ghCfg)
	doneSlot(t, dir)
	f := &reviewForge{}
	code, out, errOut := rotaInWith(t, useReviewForge(f), dir, "round", "review-relay", "nia", "--json")
	if d := data(t, out); code != 0 || d["items"] != 0.0 || d["changed"] != false {
		t.Fatalf("%d %v %s", code, d, errOut)
	}
}

func TestRoundReviewRelayUnknownSlotAndBusySlot(t *testing.T) {
	dir := workerProject(t, ghCfg)
	doneSlot(t, dir)
	deps := useReviewForge(&reviewForge{comments: []string{"fix it"}})
	if code, _, _ := rotaInWith(t, deps, dir, "round", "review-relay", "zed"); code != 3 {
		t.Errorf("unknown slot: %d, want 3", code)
	}
	if err := worker.Update(dir, func(d *worker.Doc) { _ = d.Slot("nia").MarkState("busy", "") }); err != nil {
		t.Fatal(err)
	}
	code, out, _ := rotaInWith(t, deps, dir, "round", "review-relay", "nia", "--json")
	if d := data(t, out); code != 4 || d["blockedBy"] != "state" {
		t.Errorf("busy slot: %d %v", code, d)
	}
}

func TestWatchForgeReportsReviewInputOnADoneSlot(t *testing.T) {
	dir := workerProject(t, ghCfg)
	doneSlot(t, dir)
	f := &reviewForge{comments: []string{"please rename x"}}
	deps := useReviewForge(f)
	c := &Ctx{Deps: deps, ctx: context.Background(), Stderr: io.Discard}
	key := roundwatch.ReviewKey("nia")
	if got := watchForge(context.Background(), c, dir)[key]; got != "1 from rev https://github.com/o/r/pull/9" {
		t.Fatalf("review key %q", got)
	}
	// Polling again reports the same thing: nothing consumed the batch.
	if got := watchForge(context.Background(), c, dir)[key]; got != "1 from rev https://github.com/o/r/pull/9" {
		t.Fatalf("second poll %q", got)
	}
	// The worker's own reply and a consumed cursor are not review input.
	f.comments = []string{"Fixed in abc.\n<!-- rota:worker-reply nia -->"}
	if got, ok := watchForge(context.Background(), c, dir)[key]; ok {
		t.Fatalf("worker's own reply reported as review: %q", got)
	}
	f.comments = []string{"please rename x"}
	if err := worker.Update(dir, func(d *worker.Doc) { d.Slot("nia").SetReviewSeen("2026-10-08T10:00:00Z#n1") }); err != nil {
		t.Fatal(err)
	}
	if got, ok := watchForge(context.Background(), c, dir)[key]; ok {
		t.Fatalf("consumed batch reported again: %q", got)
	}
	// A busy slot is not polled.
	if err := worker.Update(dir, func(d *worker.Doc) { d.Slot("nia").SetReviewSeen(""); _ = d.Slot("nia").MarkState("busy", "") }); err != nil {
		t.Fatal(err)
	}
	if got, ok := watchForge(context.Background(), c, dir)[key]; ok {
		t.Fatalf("busy slot polled: %q", got)
	}
}

const prURL = "https://github.com/o/r/pull/9"

func reviewCtx(t *testing.T, f *reviewForge) (*Ctx, string, *bytes.Buffer) {
	t.Helper()
	dir := workerProject(t, ghCfg)
	doneSlot(t, dir)
	var errBuf bytes.Buffer
	c := &Ctx{Deps: useReviewForge(f), ctx: context.Background(), Stderr: &errBuf}
	return c, dir, &errBuf
}

// Under manual a waiting review is reported and never holds the merge.
func TestReviewStepManualReportsAndDoesNotHold(t *testing.T) {
	c, dir, _ := reviewCtx(t, &reviewForge{comments: []string{"please rename x"}})
	got := reviewStep(c, dir, roundcfg.Settings{MaxBounces: 3, ReviewLoop: roundcfg.ReviewLoopManual}, "nia")
	if !got.Pending || got.Hold || got.Relayed || got.Detail != "1 from rev "+prURL {
		t.Fatalf("%+v", got)
	}
}

// A poll that fails is a warning, never a silent pass and never an item: it
// holds nothing under manual and holds the slot under auto.
func TestReviewStepPollFailureIsReported(t *testing.T) {
	for loop, hold := range map[string]bool{roundcfg.ReviewLoopManual: false, roundcfg.ReviewLoopAuto: true} {
		c, dir, errBuf := reviewCtx(t, &reviewForge{failReads: true})
		got := reviewStep(c, dir, roundcfg.Settings{MaxBounces: 3, ReviewLoop: loop}, "nia")
		if got.Pending || got.Hold != hold || got.Relayed || got.Detail != "" {
			t.Errorf("%s: %+v", loop, got)
		}
		if !strings.Contains(errBuf.String(), "review poll of nia") {
			t.Errorf("%s: the error is not on stderr: %q", loop, errBuf.String())
		}
	}
}

// Under auto, a relay that cannot go out holds the slot with a stable detail.
func TestReviewStepAutoHoldsWhatItCannotRelay(t *testing.T) {
	c, dir, _ := reviewCtx(t, &reviewForge{comments: []string{"please rename x"}})
	set := roundcfg.Settings{MaxBounces: 3, ReviewLoop: roundcfg.ReviewLoopAuto}
	// doneSlot has no session handle: the relay is refused.
	got := reviewStep(c, dir, set, "nia")
	if !got.Pending || !got.Hold || got.Relayed || !strings.Contains(got.Detail, "not relayed") || !strings.Contains(got.Detail, prURL) {
		t.Fatalf("refused relay: %+v", got)
	}
	if again := reviewStep(c, dir, set, "nia"); again.Detail != got.Detail {
		t.Errorf("detail changes pass to pass: %q then %q", got.Detail, again.Detail)
	}
}

// At the cap the pending batch maps to a held Pending outcome.
func TestReviewStepAutoAtTheCapIsPendingAndHeld(t *testing.T) {
	c, dir, _ := reviewCtx(t, &reviewForge{comments: []string{"please rename x"}})
	if err := worker.Update(dir, func(d *worker.Doc) { d.Slot("nia").SetHandle("w1:t1") }); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := worker.RecordBounce(dir, "577", "", ""); err != nil {
			t.Fatal(err)
		}
	}
	got := reviewStep(c, dir, roundcfg.Settings{MaxBounces: 2, ReviewLoop: roundcfg.ReviewLoopAuto}, "nia")
	if !got.Pending || !got.Hold || !strings.Contains(got.Detail, "bounce cap") {
		t.Fatalf("%+v", got)
	}
}

// The review poll must not skip silently when the round config cannot load.
func TestWatchForgeSurfacesABadRoundConfig(t *testing.T) {
	c, dir, errBuf := reviewCtx(t, &reviewForge{comments: []string{"x"}})
	if err := os.WriteFile(filepath.Join(dir, ".rota", "config.json"), []byte(`{"round":{"maxBounces":"many"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := roundcfg.Load(dir); err == nil {
		t.Skip("config still loads")
	}
	watchForge(context.Background(), c, dir)
	watchForge(context.Background(), c, dir)
	if n := strings.Count(errBuf.String(), "review poll skipped"); n != 1 {
		t.Fatalf("want one stderr note, got %d: %q", n, errBuf.String())
	}
}

func TestFailVerdictReadsAFailReview(t *testing.T) {
	dir := workerProject(t, ghCfg)
	if failVerdict(dir, "nia/577-x") != nil {
		t.Fatal("a verdict from nowhere")
	}
	rec := verdict.NewRecord(verdict.ReviewSpec, verdict.Fail, "abcdef1234", verdict.Body{Summary: "bad", Findings: []verdict.Finding{{Severity: "major", Title: "no test", File: "a.go", Line: 3}}})
	if _, err := verdict.AddBranch(dir, verdict.BranchKey("", "nia/577-x"), rec); err != nil {
		t.Fatal(err)
	}
	v := failVerdict(dir, "nia/577-x")
	if v == nil || !strings.Contains(v.Text, verdict.ReviewSpec+" FAIL at abcdef1: bad") || !strings.Contains(v.Text, "[major] no test (a.go:3)") {
		t.Fatalf("%+v", v)
	}
}

// Under auto a relay that goes out is the Relayed outcome, not pending or held.
func TestReviewStepAutoRelayedMapsToRelayed(t *testing.T) {
	f := &reviewForge{comments: []string{"please rename x"}}
	c, dir, errBuf := reviewCtx(t, f)
	useHost(c.Deps, &cliHost{})
	wt := filepath.Join(dir, "nia-wt")
	if err := os.Mkdir(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := worker.UpdateSlot(dir, "nia", func(s *worker.Slot) { s.SetHandle("w1:t1"); s.Raw().Set("worktree", wt) }); err != nil {
		t.Fatal(err)
	}
	got := reviewStep(c, dir, roundcfg.Settings{MaxBounces: 3, ReviewLoop: roundcfg.ReviewLoopAuto}, "nia")
	if !got.Relayed || got.Pending || got.Hold || got.Detail != "1 item(s), bounce 1 of 3 "+prURL {
		t.Fatalf("%+v: %s", got, errBuf)
	}
}

// A FAIL verdict whose timestamp does not parse cannot be ordered against the
// review cursor, so it is not relayed.
func TestFailVerdictIgnoresAnUnparseableTimestamp(t *testing.T) {
	dir := workerProject(t, ghCfg)
	rec := verdict.NewRecord(verdict.ReviewSpec, verdict.Fail, "abcdef1234", verdict.Body{Summary: "bad"})
	rec.RecordedAt = "yesterday"
	if _, err := verdict.AddBranch(dir, verdict.BranchKey("", "nia/577-x"), rec); err != nil {
		t.Fatal(err)
	}
	if v := failVerdict(dir, "nia/577-x"); v != nil {
		t.Fatalf("%+v", v)
	}
}
