package round

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/worker"
)

type relayRoundHost struct {
	*hostFake
	panes []string
}

func TestStatusDrivenCompletionRejectsNewerWork(t *testing.T) {
	for name, tc := range map[string]struct {
		native string
		absent bool
		before func(*worker.Slot)
		during func(*worker.Slot)
		want   string
	}{
		"native idle": {native: "idle", want: "done"},
		"native done": {native: "done", want: "done"},
		"tmux alive":  {want: "done"},
		"working":     {native: "working"},
		"blocked":     {native: "blocked"},
		"unknown":     {native: "unknown"},
		"absent":      {absent: true},
		"pending relay": {native: "idle", before: func(s *worker.Slot) {
			s.SetRelayPending(1, "2026-11-03T10:00:00Z")
		}},
		"transfer awaits done": {native: "idle", before: func(s *worker.Slot) { s.SetAwaitingDone(true) }},
		"unsent brief":         {native: "idle", before: func(s *worker.Slot) { s.SetUnsent(true) }},
		"new question": {native: "idle", before: func(s *worker.Slot) {
			s.SetEvent(worker.EventQuestion, "Which next?", s.ActiveAt())
		}},
		"answered old question": {native: "idle", want: "done", before: func(s *worker.Slot) {
			at := s.ActiveAt()
			s.SetEvent(worker.EventQuestion, "Already answered", "2000-01-01T00:00:00Z")
			s.Touch(at)
		}},
		"relay during snapshot": {native: "idle", during: func(s *worker.Slot) {
			s.Dispatch(s.Handle(), "", "", "2026-11-03T10:00:00Z")
			s.SetRelayPending(1, "2026-11-03T10:00:00Z")
		}},
		"new session during snapshot": {native: "idle", during: func(s *worker.Slot) {
			s.Dispatch("new:session", "13", "", "2026-11-03T10:00:00Z")
		}},
		"completed replacement reuses handle": {native: "idle", during: func(s *worker.Slot) {
			s.Dispatch(s.Handle(), "13", "", "2026-11-03T10:00:00Z")
			if err := s.MarkState("done", "2026-11-03T10:00:00Z"); err != nil {
				panic(err)
			}
		}},
	} {
		t.Run(name, func(t *testing.T) {
			f := newMoveFx(t)
			f.finish(t, "ben", pr7)
			if tc.before != nil {
				if _, err := worker.UpdateSlot(f.root, "ben", tc.before); err != nil {
					t.Fatal(err)
				}
			}
			handle := f.slot("ben").Handle()
			f.env.Snapshot = func(context.Context) ([]host.Agent, error) {
				if tc.during != nil {
					if _, err := worker.UpdateSlot(f.root, "ben", tc.during); err != nil {
						t.Fatal(err)
					}
				}
				if tc.absent {
					return nil, nil
				}
				return []host.Agent{{Tab: handle, Name: "ben", Cwd: f.wt("ben"), Status: tc.native}}, nil
			}
			rep, err := f.env.Status(bg, f.root)
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range rep.Rows {
				if row.Name == "ben" {
					if row.State != tc.want || row.HostState != tc.native {
						t.Fatalf("state=%q hostState=%q, want %q/%q", row.State, row.HostState, tc.want, tc.native)
					}
					return
				}
			}
			t.Fatal("status omitted ben")
		})
	}
}

func (h *relayRoundHost) Capture(context.Context, string, string, int) string {
	if len(h.panes) == 0 {
		return h.capture
	}
	pane := h.panes[0]
	h.panes = h.panes[1:]
	return pane
}

func TestRelayCompletionWithoutCommitAllowsNextAssignment(t *testing.T) {
	for _, pushedRevision := range []bool{false, true} {
		t.Run(map[bool]string{false: "text reply", true: "pushed revision"}[pushedRevision], func(t *testing.T) {
			f := newMoveFx(t)
			f.finish(t, "ben", pr7)
			branch := f.slot("ben").Branch()
			gitIn(t, f.wt("ben"), "push", "origin", branch)
			head := gitIn(t, f.wt("ben"), "rev-parse", "HEAD")
			h := &relayRoundHost{hostFake: f.host}
			f.ws.NewHost = func(host.Kind) host.Host { return h }
			f.env.Worker = wk(f.ws)
			brief := filepath.Join(t.TempDir(), "reply.md")
			if err := os.WriteFile(brief, []byte("Clarify the PR body.\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := f.ws.DispatchRelay(bg, f.root, worker.DispatchOpts{Slot: "ben", BodyFile: brief}); err != nil {
				t.Fatal(err)
			}
			if f.slot("ben").State() != "busy" {
				t.Fatal("relay did not reopen the slot")
			}
			if pushedRevision {
				f.commit(t, "ben", "revision.txt")
				gitIn(t, f.wt("ben"), "push", "origin", branch)
			}
			// A push and the old sentinel still cannot complete queued relay work.
			h.capture = "ROTA-DONE ben " + pr7 + "\n"
			poll, err := f.ws.Poll(bg, f.root, worker.PollOpts{Slot: "ben"})
			if err != nil || poll.Slots[0].State != worker.StateBusy || f.slot("ben").State() != "busy" {
				t.Fatalf("old completion after relay: %+v, %v", poll, err)
			}
			if _, err := f.assign("13", "ben"); err == nil || blockedBy(t, err) != BlockSlotBusy {
				t.Fatalf("assignment before relay completion: %v", err)
			}
			h.panes = []string{f.host.sent + "\n● working\n", strings.Repeat("● work\n", 80) + "ROTA-DONE ben\n"}
			poll, err = f.ws.Poll(bg, f.root, worker.PollOpts{Slot: "ben"})
			if err != nil || poll.Slots[0].State != worker.StateDone || f.slot("ben").State() != "done" {
				t.Fatalf("fresh completion after relay: %+v, %v", poll, err)
			}
			if !pushedRevision && gitIn(t, f.wt("ben"), "rev-parse", "HEAD") != head {
				t.Fatal("text-only completion moved the head")
			}
			f.env.Snapshot = func(context.Context) ([]host.Agent, error) {
				return []host.Agent{{Tab: f.slot("ben").Handle(), Name: "ben", Cwd: f.wt("ben"), Status: "idle"}}, nil
			}
			status, err := f.env.Status(bg, f.root)
			if err != nil {
				t.Fatalf("status after completion: %v", err)
			}
			found := false
			for _, row := range status.Rows {
				if row.Name == "ben" {
					found = true
					if row.HostState != "idle" || row.State != "done" || f.slot("ben").State() != "done" {
						t.Fatalf("native idle status must expose recorded completion separately: %+v", row)
					}
				}
			}
			if !found {
				t.Fatal("status omitted ben")
			}
			res, err := f.assign("13", "ben")
			if err != nil || !res.Dispatched || f.slot("ben").Task() != "13" {
				t.Fatalf("assignment after pane-only completion: %+v, %v", res, err)
			}
			if q := f.queued(); len(q) != 1 || q[0].Issue != "12" || q[0].PR != pr7 {
				t.Fatalf("previous PR was not queued: %+v", q)
			}
		})
	}
}
