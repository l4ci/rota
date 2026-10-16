package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/worker"
)

// sendProject is a project whose slot ben holds B07, busy since an old stamp.
func sendProject(t *testing.T) string {
	t.Helper()
	dir := doneProject(t, "")
	os.WriteFile(filepath.Join(dir, ".rota", "workers.json"),
		[]byte(`{"slots":[{"name":"ben","branch":"feat/x","task":"B07","state":"busy","activeAt":"2020-01-01T00:00:00Z"}]}`), 0o644)
	return dir
}

func ledgerRows(t *testing.T, dir string) []string {
	t.Helper()
	b, _ := os.ReadFile(filepath.Join(dir, ".rota", "ledger.jsonl"))
	var rows []string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if l != "" {
			rows = append(rows, strings.ReplaceAll(l, `": `, `":`))
		}
	}
	return rows
}

func slotOf(t *testing.T, dir, name string) *worker.Slot {
	t.Helper()
	reg, err := worker.LoadRegistry(dir)
	if err != nil {
		t.Fatal(err)
	}
	return reg.Slot(name)
}

func TestWorkerSendBlocked(t *testing.T) {
	for _, h := range []string{"herdr", "tmux", "solo"} {
		t.Run(h, func(t *testing.T) {
			dir := sendProject(t)
			recordRoundHost(t, dir, h)
			code, out, errOut := rotaIn(t, dir, "worker", "send", "ben", "--blocked", "A or B?", "--json")
			if d := data(t, out); code != 0 || d["event"] != "blocked" || d["state"] != "blocked" || d["seq"] != 1.0 {
				t.Fatalf("%d %v %s", code, d, errOut)
			}
			s := slotOf(t, dir, "ben")
			ev := s.Event()
			if s.State() != "blocked" || ev == nil || ev.Seq != 1 || ev.Kind != "blocked" || ev.Body != "A or B?" || ev.TS == "" {
				t.Fatalf("slot = %s %+v", s.State(), ev)
			}
			if rows := ledgerRows(t, dir); len(rows) != 1 || !strings.Contains(rows[0], `"kind":"blocked"`) || !strings.Contains(rows[0], "A or B?") {
				t.Fatalf("ledger = %v", rows)
			}
			// A second send is a second event: seq 2 and a second row.
			rotaIn(t, dir, "worker", "send", "ben", "--blocked", "C or D?")
			if ev := slotOf(t, dir, "ben").Event(); ev.Seq != 2 || ev.Body != "C or D?" {
				t.Errorf("second event = %+v", ev)
			}
			if rows := ledgerRows(t, dir); len(rows) != 2 {
				t.Errorf("ledger = %v, want two rows", rows)
			}
		})
	}
}

func TestWorkerSendQuestionIsABlockedStateWithItsOwnKind(t *testing.T) {
	dir := sendProject(t)
	if code, out, _ := rotaIn(t, dir, "worker", "send", "ben", "--question", "Which flag?"); code != 0 {
		t.Fatalf("%d %s", code, out)
	}
	s := slotOf(t, dir, "ben")
	if ev := s.Event(); s.State() != "blocked" || ev.Kind != "question" || ev.Body != "Which flag?" {
		t.Errorf("slot = %s %+v", s.State(), ev)
	}
	if rows := ledgerRows(t, dir); len(rows) != 1 || !strings.Contains(rows[0], `"event":"question"`) {
		t.Errorf("ledger = %v", rows)
	}
}

func TestWorkerSendRefusals(t *testing.T) {
	dir := sendProject(t)
	for name, c := range map[string]struct {
		args []string
		want int
	}{
		"no event":     {[]string{"ben"}, 2},
		"two events":   {[]string{"ben", "--blocked", "x", "--heartbeat"}, 2},
		"empty body":   {[]string{"ben", "--blocked", "  "}, 2},
		"unknown slot": {[]string{"zed", "--blocked", "x"}, 3},
		"no slot":      {[]string{"--heartbeat"}, 2},
	} {
		if code, _, _ := rotaIn(t, dir, append([]string{"worker", "send"}, c.args...)...); code != c.want {
			t.Errorf("%s: %d, want %d", name, code, c.want)
		}
	}
	// A corrupt registry fails closed (#712): no write over what cannot be read.
	os.WriteFile(filepath.Join(dir, ".rota", "workers.json"), []byte(`{"slots": [`), 0o644)
	if code, _, _ := rotaIn(t, dir, "worker", "send", "ben", "--blocked", "x"); code != 5 {
		t.Errorf("corrupt registry: %d, want 5", code)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, ".rota", "workers.json")); string(b) != `{"slots": [` {
		t.Errorf("a corrupt registry was rewritten: %s", b)
	}
}

func TestHeartbeatRefreshesStallClock(t *testing.T) {
	dir := sendProject(t)
	code, out, _ := rotaIn(t, dir, "worker", "send", "ben", "--heartbeat", "--json")
	if d := data(t, out); code != 0 || d["event"] != "heartbeat" || d["state"] != "busy" {
		t.Fatalf("%d %v", code, d)
	}
	s := slotOf(t, dir, "ben")
	if s.State() != "busy" {
		t.Errorf("a heartbeat moved the state to %s: alive is not progress", s.State())
	}
	if s.LastAlive() == "2020-01-01T00:00:00Z" {
		t.Error("a heartbeat did not restart the stall clock")
	}
	if s.HeartbeatAt() == "" || s.LastAlive() != s.HeartbeatAt() {
		t.Errorf("heartbeatAt = %q, lastAlive = %q", s.HeartbeatAt(), s.LastAlive())
	}
	if s.Event() != nil {
		t.Errorf("a heartbeat is not an event: %+v", s.Event())
	}
	if rows := ledgerRows(t, dir); len(rows) != 0 {
		t.Errorf("a heartbeat wrote ledger rows: %v", rows)
	}
}

func TestWorkerDoneWritesEvent(t *testing.T) {
	dir := doneProject(t, "")
	if code, out, _ := rotaIn(t, dir, "worker", "done", "ben", "--json"); code != 0 {
		t.Fatalf("%d %s", code, out)
	}
	if ev := slotOf(t, dir, "ben").Event(); ev == nil || ev.Kind != "done" || ev.Seq != 1 {
		t.Errorf("event = %+v", ev)
	}
	// Already done by a pane read, no event yet: the verb still records its word.
	os.WriteFile(filepath.Join(dir, ".rota", "workers.json"),
		[]byte(`{"slots":[{"name":"ben","branch":"feat/x","task":"B07","state":"done"}]}`), 0o644)
	rotaIn(t, dir, "worker", "done", "ben")
	if ev := slotOf(t, dir, "ben").Event(); ev == nil || ev.Kind != "done" {
		t.Errorf("already-done slot got no event: %+v", ev)
	}
}

func TestWorkerReplyWritesEvent(t *testing.T) {
	dir := workerProject(t, ghCfg)
	doneSlot(t, dir)
	// The bounce left a review cursor: a reply must not move it.
	worker.UpdateSlot(dir, "nia", func(s *worker.Slot) { s.Raw().Set("reviewSeen", "cursor-1"); s.MarkState("busy", "") })
	os.WriteFile(filepath.Join(dir, "reply.md"), []byte("Fixed in abc1234.\n"), 0o644)
	code, _, errOut := rotaInWith(t, useReviewForge(&reviewForge{}), dir, "worker", "reply", "nia", "--body-file", "reply.md")
	if code != 0 {
		t.Fatalf("%d %s", code, errOut)
	}
	s := slotOf(t, dir, "nia")
	if ev := s.Event(); ev == nil || ev.Kind != "review-reply" {
		t.Fatalf("event = %+v", ev)
	}
	if s.State() != "busy" {
		t.Errorf("state = %s: a reply posted mid-fix must not mark the slot done", s.State())
	}
	if got := jsonx.Str(s.Raw(), "reviewSeen"); got != "cursor-1" {
		t.Errorf("reviewSeen = %q: a reply re-ran BaselineReview", got)
	}
	rows := ledgerRows(t, dir)
	n := 0
	for _, r := range rows {
		if strings.Contains(r, `"kind":"review-reply"`) {
			n++
		}
	}
	if n != 1 {
		t.Errorf("review-reply rows = %d in %v", n, rows)
	}
	if !strings.Contains(strings.Join(rows, "\n"), `"seq":1`) {
		t.Errorf("review-reply row has no seq: %v", rows)
	}
}

// A heartbeat while a question stands must not lose the question.
func TestHeartbeatKeepsTheStandingEvent(t *testing.T) {
	dir := sendProject(t)
	rotaIn(t, dir, "worker", "send", "ben", "--blocked", "A or B?")
	if code, out, _ := rotaIn(t, dir, "worker", "send", "ben", "--heartbeat"); code != 0 {
		t.Fatalf("%d %s", code, out)
	}
	s := slotOf(t, dir, "ben")
	ev := s.Event()
	if s.State() != "blocked" || ev == nil || ev.Kind != "blocked" || ev.Body != "A or B?" || ev.Seq != 1 {
		t.Errorf("slot = %s %+v", s.State(), ev)
	}
	if ev.TS < s.ActiveAt() {
		t.Errorf("event ts %s older than activeAt %s: the heartbeat killed it", ev.TS, s.ActiveAt())
	}
}

func TestLedgerRowsCarryTheEventSeq(t *testing.T) {
	dir := sendProject(t)
	rotaIn(t, dir, "worker", "send", "ben", "--blocked", "one")
	rotaIn(t, dir, "worker", "send", "ben", "--blocked", "two")
	rows := ledgerRows(t, dir)
	if len(rows) != 2 || !strings.Contains(rows[0], `"seq":1`) || !strings.Contains(rows[1], `"seq":2`) {
		t.Errorf("ledger = %v: rows and slot record must share the event id", rows)
	}
}
