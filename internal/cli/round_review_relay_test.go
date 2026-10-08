package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/roundwatch"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// reviewForge answers the GitHub calls of the review poll for PR 9: one
// reviewer comment on the thread, no reviews, and POSTs recorded.
type reviewForge struct {
	comments []string // thread comment bodies, oldest first
	posted   []string
	calls    []string
}

func (f *reviewForge) exec(_ context.Context, _ string, name string, args []string, _ []byte) ([]byte, []byte, int, error) {
	line := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, line)
	switch {
	case strings.Contains(line, "-X POST"):
		for _, a := range args {
			if b, ok := strings.CutPrefix(a, "body="); ok {
				f.posted = append(f.posted, b)
			}
		}
		return []byte(`{"id":77}`), nil, 0, nil
	case strings.Contains(line, "issues/comments/77"):
		return []byte(`{"id":77,"html_url":"https://github.com/o/r/pull/9#issuecomment-77"}`), nil, 0, nil
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
	c := &Ctx{Deps: deps, ctx: context.Background()}
	key := roundwatch.ReviewKey("nia")
	if got := watchForge(context.Background(), c, dir)[key]; got != "1 from rev" {
		t.Fatalf("review key %q", got)
	}
	// Polling again reports the same thing: nothing consumed the batch.
	if got := watchForge(context.Background(), c, dir)[key]; got != "1 from rev" {
		t.Fatalf("second poll %q", got)
	}
	// The worker's own reply and a consumed cursor are not review input.
	f.comments = []string{"Fixed in abc.\n<!-- rota:worker-reply nia -->"}
	if got, ok := watchForge(context.Background(), c, dir)[key]; ok {
		t.Fatalf("worker's own reply reported as review: %q", got)
	}
	f.comments = []string{"please rename x"}
	if err := worker.Update(dir, func(d *worker.Doc) { d.Slot("nia").SetReviewSeen("2026-10-08T10:00:00Z") }); err != nil {
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
