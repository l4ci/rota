package cli

import (
	"bytes"
	"context"
	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/rotatree"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/round"
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
	// undated serves the comments without a created_at.
	undated bool
	prState string // the PR's state; "" is OPEN
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
			at := `,"created_at":"2026-10-08T10:0` + string(rune('0'+i)) + `:00Z"`
			if f.undated {
				at = ""
			}
			rows = append(rows, `{"id":`+string(rune('1'+i))+`,"body":"`+bj+`","user":{"login":"rev"}`+at+`}`)
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

// At the cap the pending batch maps to a held Pending outcome.
func TestReviewStepAutoAtTheCapIsPendingAndHeld(t *testing.T) {
	c, dir, _ := reviewCtx(t, &reviewForge{comments: []string{"please rename x"}})
	if err := worker.Update(dir, func(d *worker.Doc) { d.Slot("nia").SetHandle("w1:t1") }); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := recordBounceVia(worker.Env{}, dir, "577", "", ""); err != nil {
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
	if _, err := roundcfg.FromConfig(config.Load(rotatree.Config(dir))); err == nil {
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
	rec := verdict.NewRecord(verdict.ReviewSpec, verdict.Fail, "abcdef1234", verdict.Body{Summary: "bad", Findings: []verdict.Finding{{Severity: "major", Title: "no test", File: "a.go", Line: 3}}}, testNow)
	if _, err := verdict.AddBranch(dir, verdict.BranchKey("", "nia/577-x"), rec); err != nil {
		t.Fatal(err)
	}
	v := failVerdict(dir, "nia/577-x")
	if v == nil || !strings.Contains(v.Text, verdict.ReviewSpec+" FAIL at abcdef1: bad") || !strings.Contains(v.Text, "[major] no test (a.go:3)") {
		t.Fatalf("%+v", v)
	}
}

// A CONCERNS verdict with a blocking finding is Failing (it may be bounced) but
// not an effective FAIL: the relay input reads EffectiveFail, so it relays
// nothing. Swapping the two predicates at either call site fails this test.
func TestRelayInputIgnoresConcernsWithABlocker(t *testing.T) {
	dir := workerProject(t, ghCfg)
	rec := verdict.NewRecord(verdict.ReviewQuality, verdict.Concerns, "abcdef1234", verdict.Body{Summary: "meh", Findings: []verdict.Finding{{Severity: "major", Title: "real"}}}, testNow)
	if !rec.Failing() {
		t.Fatal("fixture must be Failing, or the test proves nothing")
	}
	if _, err := verdict.AddBranch(dir, verdict.BranchKey("", "nia/577-x"), rec); err != nil {
		t.Fatal(err)
	}
	if v := failVerdict(dir, "nia/577-x"); v != nil {
		t.Errorf("failVerdict relayed a CONCERNS verdict: %+v", v)
	}
	if got := openFindings(dir, "nia/577-x"); got != nil {
		t.Errorf("openFindings listed a CONCERNS verdict: %v", got)
	}
}

// A FAIL verdict whose timestamp does not parse cannot be ordered against the
// review cursor, so it is not relayed.
func TestFailVerdictIgnoresAnUnparseableTimestamp(t *testing.T) {
	dir := workerProject(t, ghCfg)
	rec := verdict.NewRecord(verdict.ReviewSpec, verdict.Fail, "abcdef1234", verdict.Body{Summary: "bad"}, testNow)
	rec.RecordedAt = "yesterday"
	if _, err := verdict.AddBranch(dir, verdict.BranchKey("", "nia/577-x"), rec); err != nil {
		t.Fatal(err)
	}
	if v := failVerdict(dir, "nia/577-x"); v != nil {
		t.Fatalf("%+v", v)
	}
}

// review-relay reads the round settings once, from the round env: a faked
// Deps.RoundEnv decides the cap the verb reports, not a second config load.
func TestRoundReviewRelayTakesSettingsFromRoundEnv(t *testing.T) {
	dir := workerProject(t, ghCfg)
	doneSlot(t, dir)
	deps := useReviewForge(&reviewForge{})
	base := deps.RoundEnv
	deps.RoundEnv = func(ctx context.Context, root string) round.Env {
		e := base(ctx, root)
		e.Round.MaxBounces = 7
		return e
	}
	code, out, errOut := rotaInWith(t, deps, dir, "round", "review-relay", "nia", "--json")
	if d := data(t, out); code != 0 || d["max"] != 7.0 {
		t.Fatalf("%d %v %s", code, d, errOut)
	}
}

// An invalid round.* key keeps the verb's documented exit 3 (resolution).
func TestRoundReviewRelayBadRoundConfigExits3(t *testing.T) {
	dir := workerProject(t, ghCfg)
	doneSlot(t, dir)
	if err := os.WriteFile(filepath.Join(dir, ".rota", "config.json"), []byte(`{"round":{"maxBounces":"many"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := roundcfg.FromConfig(config.Load(rotatree.Config(dir))); err == nil {
		t.Skip("config still loads")
	}
	code, _, errOut := rotaInWith(t, useReviewForge(&reviewForge{}), dir, "round", "review-relay", "nia")
	if code != 3 || !strings.Contains(errOut, "round.maxBounces") {
		t.Fatalf("%d %s", code, errOut)
	}
}

// relaySlot seeds w1 as a done slot with PR 9 and a session, and returns the
// relay brief path.
func relaySlot(t *testing.T, f *reviewForge) (*Ctx, string, string) {
	t.Helper()
	deps := useReviewForge(f)
	useHost(deps, &cliHost{inSession: true})
	dir := workerProject(t, ghCfg)
	rotaInWith(t, deps, dir, "worker", "pool", "init", "--slots", "1", "--base", "main")
	rotaInWith(t, deps, dir, "worker", "dispatch", "w1", "--task", "577", "--body-file", filepath.Join(dir, "go.mod"))
	handBack(t, dir)
	brief := filepath.Join(t.TempDir(), "relay.md")
	if err := os.WriteFile(brief, []byte("REVIEW "+prURL+" (1 items)\nItem 1: please rename x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return &Ctx{Deps: deps, ctx: context.Background(), Stderr: io.Discard}, dir, brief
}

// handBack marks w1 done on PR 9: the worker handed the PR back.
func handBack(t *testing.T, dir string) {
	t.Helper()
	if err := worker.Update(dir, func(d *worker.Doc) {
		s := d.Slot("w1")
		s.SetPR(prURL)
		_ = s.MarkState("done", "")
	}); err != nil {
		t.Fatal(err)
	}
}

// `worker dispatch --relay --review-through` consumes the review input the
// relay carried: the next watch poll does not report it again, and a comment
// that landed after the read is reported (#1866).
func TestWorkerDispatchRelayConsumesWhatItCarried(t *testing.T) {
	f := &reviewForge{comments: []string{"please rename x"}} // dated 10:00:00
	c, dir, brief := relaySlot(t, f)
	key := roundwatch.ReviewKey("w1")
	if got := watchForge(context.Background(), c, dir)[key]; got == "" {
		t.Fatal("setup: the comment should be pending")
	}
	// A second comment lands after the orchestrator's read (10:00:30) and before the relay.
	f.comments = append(f.comments, "and the other thing") // dated 10:01:00
	if code, _, errOut := rotaInWith(t, c.Deps, dir, "worker", "dispatch", "w1", "--body-file", brief, "--relay", "--review-through", "2026-10-08T10:00:30Z"); code != 0 {
		t.Fatalf("relay: %d %s", code, errOut)
	}
	handBack(t, dir) // the worker reworks and hands the PR back
	if got := watchForge(context.Background(), c, dir)[key]; got != "1 from rev "+prURL {
		t.Fatalf("only the later comment is pending: %q", got)
	}
	// With the later comment gone, nothing is pending: the relayed one was consumed.
	f.comments = f.comments[:1]
	if got, ok := watchForge(context.Background(), c, dir)[key]; ok {
		t.Fatalf("relayed batch reported again: %q", got)
	}
}

// A relay without --review-through leaves the cursor alone, whatever its text.
func TestWorkerDispatchRelayWithoutFlagLeavesTheCursor(t *testing.T) {
	f := &reviewForge{comments: []string{"please rename x"}}
	c, dir, brief := relaySlot(t, f)
	if code, _, errOut := rotaInWith(t, c.Deps, dir, "worker", "dispatch", "w1", "--body-file", brief, "--relay"); code != 0 {
		t.Fatalf("relay: %d %s", code, errOut)
	}
	handBack(t, dir)
	if got := watchForge(context.Background(), c, dir)[roundwatch.ReviewKey("w1")]; got == "" {
		t.Fatal("a plain relay consumed the review input")
	}
}

func TestWorkerDispatchReviewThroughNeedsRelayAndATime(t *testing.T) {
	c, dir, brief := relaySlot(t, &reviewForge{})
	if code, _, _ := rotaInWith(t, c.Deps, dir, "worker", "dispatch", "w1", "--body-file", brief, "--review-through", "2026-10-08T10:00:30Z"); code != 2 {
		t.Errorf("without --relay: %d, want 2", code)
	}
	if code, _, _ := rotaInWith(t, c.Deps, dir, "worker", "dispatch", "w1", "--body-file", brief, "--relay", "--review-through", "yesterday"); code != 2 {
		t.Errorf("bad time: %d, want 2", code)
	}
}

// Input the forge dates nothing for cannot be shown to predate the read, so it
// stays pending after --review-through.
func TestWorkerDispatchReviewThroughLeavesUndatedInputPending(t *testing.T) {
	f := &reviewForge{comments: []string{"please rename x"}, undated: true}
	c, dir, brief := relaySlot(t, f)
	key := roundwatch.ReviewKey("w1")
	if code, _, errOut := rotaInWith(t, c.Deps, dir, "worker", "dispatch", "w1", "--body-file", brief, "--relay", "--review-through", "2026-10-08T10:00:30Z"); code != 0 {
		t.Fatalf("relay: %d %s", code, errOut)
	}
	handBack(t, dir)
	if got := watchForge(context.Background(), c, dir)[key]; got != "1 from rev "+prURL {
		t.Fatalf("undated input consumed: %q", got)
	}
	if got := worker.LoadRegistryTolerant(dir).Slot("w1").ReviewSeen(); got != "" {
		t.Errorf("cursor moved to %q", got)
	}
}

func TestWorkerDispatchReviewThroughRefusesTheFutureAndAMissingPR(t *testing.T) {
	c, dir, brief := relaySlot(t, &reviewForge{})
	if code, _, _ := rotaInWith(t, c.Deps, dir, "worker", "dispatch", "w1", "--body-file", brief, "--relay", "--review-through", "2999-01-01T00:00:00Z"); code != 2 {
		t.Errorf("future time: %d, want 2", code)
	}
	if err := worker.Update(dir, func(d *worker.Doc) { d.Slot("w1").SetPR("") }); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := rotaInWith(t, c.Deps, dir, "worker", "dispatch", "w1", "--body-file", brief, "--relay", "--review-through", "2026-10-08T10:00:30Z"); code != 3 {
		t.Errorf("no PR: %d, want 3", code)
	}
}

// --review-through bounds consumption, not the relay text: a signed REVIEW
// brief, a plain note and a REVIEW for another PR consume the same batch.
func TestWorkerDispatchReviewThroughIgnoresTheRelayText(t *testing.T) {
	bodies := map[string]string{
		"signed REVIEW": "--- ORCHESTRATOR (round 3) ---\nREVIEW " + prURL + " (1 items)\nItem 1: please rename x\n--- ROTA-SIG abc123 ---\n",
		"gap note":      "no review here, just a note\n",
		"other PR":      "REVIEW https://github.com/o/r/pull/10 (1 items)\nItem 1: other\n",
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			f := &reviewForge{comments: []string{"please rename x"}} // dated 10:00:00
			c, dir, _ := relaySlot(t, f)
			brief := filepath.Join(t.TempDir(), "relay.md")
			if err := os.WriteFile(brief, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			if code, _, errOut := rotaInWith(t, c.Deps, dir, "worker", "dispatch", "w1", "--body-file", brief, "--relay", "--review-through", "2026-10-08T10:00:30Z"); code != 0 {
				t.Fatalf("relay: %d %s", code, errOut)
			}
			handBack(t, dir)
			if got, ok := watchForge(context.Background(), c, dir)[roundwatch.ReviewKey("w1")]; ok {
				t.Errorf("batch still pending: %q", got)
			}
			if got := worker.LoadRegistryTolerant(dir).Slot("w1").ReviewSeen(); got == "" {
				t.Error("cursor not moved")
			}
		})
	}
}

// A forge failure while consuming does not undo the relay: it is sent, the
// verb exits 0, warns that the input stays pending, and the cursor stays put.
func TestWorkerDispatchReviewThroughForgeFailureWarnsAndExits0(t *testing.T) {
	f := &reviewForge{comments: []string{"please rename x"}, failReads: true}
	c, dir, brief := relaySlot(t, f)
	code, _, errOut := rotaInWith(t, c.Deps, dir, "worker", "dispatch", "w1", "--body-file", brief, "--relay", "--review-through", "2026-10-08T10:00:30Z")
	if code != 0 {
		t.Fatalf("relay: %d %s", code, errOut)
	}
	if !strings.Contains(errOut, "review input stays pending") {
		t.Errorf("no warning: %q", errOut)
	}
	if got := worker.LoadRegistryTolerant(dir).Slot("w1").ReviewSeen(); got != "" {
		t.Errorf("cursor moved to %q", got)
	}
}
