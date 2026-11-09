package round

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/backlog/trackertest"
	"github.com/l4ci/rota/internal/cfgdir"
	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/proof"
	"github.com/l4ci/rota/internal/testutil/coretest"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

func TestCoreAdapterRoundSnapshotAbsenceAndIdentity(t *testing.T) {
	for _, mode := range []string{"missing", "incarnation", "snapshot-unreadable", "unowned-same-cwd", "finished"} {
		t.Run(mode, func(t *testing.T) {
			f := newMoveFx(t)
			peer, h := coretest.New(t, harness.Codex)
			handle, err := h.Spawn(bg, host.SpawnOpts{Slot: "ben", Cwd: f.wt("ben"), Launch: "codex", BootTimeout: 1})
			if err != nil {
				t.Fatal(err)
			}
			if err = worker.Update(f.root, func(d *worker.Doc) { d.SetHost(host.HerdrCore) }); err != nil {
				t.Fatal(err)
			}
			if err = rawSlot(f.root, "ben", func(s *jsonx.Object) { s.Set("handle", handle); s.Set("state", "busy") }); err != nil {
				t.Fatal(err)
			}
			f.env.HostName = host.HerdrCore
			f.env.Snapshot = h.Capabilities().Snapshotter.Snapshot
			f.wire(func(w *worker.Env) { w.NewHost = func(host.Kind) host.Host { return h } })
			peer.Mode(mode)
			health := f.env.Health(bg, f.root, f.slot("ben"), f.now)
			if health.Health != HealthUnverifiable || health.Known {
				t.Fatalf("%s snapshot health=%+v", mode, health)
			}
			rep, err := f.env.Status(bg, f.root)
			if err != nil {
				t.Fatal(err)
			}
			for _, finding := range rep.Findings {
				if finding.Slot == "ben" && finding.Kind == DeadTab {
					t.Fatalf("%s offered destructive dead-tab repair", mode)
				}
			}
			if _, err = f.reclaim("ben", nil); blockedBy(t, err) != BlockUnverifiable {
				t.Fatalf("%s reclaim must refuse: %v", mode, err)
			}
			_, _, kills := peer.Counts()
			if kills != 0 || f.slot("ben").Handle() != handle || f.slot("ben").Task() != "12" {
				t.Fatalf("uncertain identity lost authority: kills=%d slot=%+v", kills, f.slot("ben").Raw())
			}
		})
	}
}

func TestCoreAdapterRoundReplacementNeverCompletesIssue(t *testing.T) {
	f := newMoveFx(t)
	peer, h := coretest.New(t, harness.Codex)
	handle, err := h.Spawn(context.Background(), host.SpawnOpts{Slot: "ben", Cwd: f.wt("ben"), Launch: "codex", BootTimeout: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err = worker.Update(f.root, func(d *worker.Doc) { d.SetHost(host.HerdrCore) }); err != nil {
		t.Fatal(err)
	}
	if err = rawSlot(f.root, "ben", func(s *jsonx.Object) { s.Set("handle", handle); s.Set("state", "busy"); s.Set("kind", harness.Codex) }); err != nil {
		t.Fatal(err)
	}
	f.env.HostName = host.HerdrCore
	f.env.Snapshot = h.Capabilities().Snapshotter.Snapshot
	f.wire(func(w *worker.Env) { w.NewHost = func(host.Kind) host.Host { return h } })
	peer.Mode("replaced")
	health := f.env.Health(bg, f.root, f.slot("ben"), f.now)
	if health.Health != HealthUnverifiable {
		t.Fatalf("replacement snapshot must suspend before poll: %+v", health)
	}
	polled, err := f.ws.Poll(bg, f.root, worker.PollOpts{Slot: "ben", Lines: 60})
	if err != nil || len(polled.Slots) != 1 || polled.Slots[0].State != worker.StateUnverifiable {
		t.Fatalf("replacement poll=%+v %v", polled, err)
	}
	rep, err := f.env.Status(bg, f.root)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rep.Rows {
		if r.Name == "ben" && (r.HostState == "done" || r.HostState == "gone") {
			t.Fatalf("replacement became success/death: %+v", r)
		}
	}
	if f.slot("ben").Task() != "12" || f.slot("ben").Handle() != handle || f.slot("ben").State() == worker.StateDone {
		t.Fatal("replacement cleared held issue or completed work")
	}
	_, _, kills := peer.Counts()
	if kills != 0 {
		t.Fatal("monitoring killed replacement shell")
	}
}

func TestCoreAdapterAcceptReadsReviewCompletionWithoutPoll(t *testing.T) {
	for _, replaced := range []bool{false, true} {
		t.Run(fmt.Sprint(replaced), func(t *testing.T) {
			f := newMoveFx(t)
			f.mintReview(t, "12")
			peer, h := coretest.New(t, harness.Codex)
			handle, err := h.Spawn(bg, host.SpawnOpts{Slot: "ben", Cwd: f.wt("ben"), Launch: "codex", BootTimeout: 1})
			if err != nil {
				t.Fatal(err)
			}
			setHost(t, f.root, host.HerdrCore)
			if err := rawSlot(f.root, "ben", func(s *jsonx.Object) {
				s.Set("handle", handle)
				s.Set("kind", harness.Codex)
			}); err != nil {
				t.Fatal(err)
			}
			f.wire(func(w *worker.Env) { w.NewHost = func(host.Kind) host.Host { return h } })
			peer.Capture("ROTA-DONE ben issues:none\n", "", 80, 24)
			if replaced {
				peer.Mode("replaced")
			}
			res, err := f.accept("12", nil)
			if replaced {
				if blockedBy(t, err) != BlockSlotBusy || res.Changed || f.be.items["12"].Closed || f.slot("ben").Task() != "12" {
					t.Fatalf("replacement accepted: %+v %v", res, err)
				}
			} else if err != nil || !res.Changed || !res.NoFindings || !f.be.items["12"].Closed || f.slot("ben").Task() != "" {
				t.Fatalf("current completion not accepted: %+v %v", res, err)
			}
		})
	}
}

// The terminal's native idle state is lifecycle evidence. Completion still
// crosses the real issue backend's acceptance and proof guards.
func TestCoreAdapterRoundNativeIdleCannotBypassIssueGates(t *testing.T) {
	f := newMoveFx(t)
	peer, h := coretest.New(t, harness.Codex)
	handle, err := h.Spawn(bg, host.SpawnOpts{Slot: "ben", Cwd: f.wt("ben"), Launch: "codex", BootTimeout: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err = worker.Update(f.root, func(d *worker.Doc) { d.SetHost(host.HerdrCore) }); err != nil {
		t.Fatal(err)
	}
	if err = rawSlot(f.root, "ben", func(s *jsonx.Object) {
		s.Set("handle", handle)
		s.Set("kind", harness.Codex)
		s.Set("state", worker.StateBusy)
	}); err != nil {
		t.Fatal(err)
	}
	remote := &trackertest.Fake{Issues: []tracker.Issue{{Key: tracker.NumKey(12), Title: "Implement task", State: "open", Labels: []string{"type:task", "in-progress"}, Body: "## Acceptance\n- [ ] AC-1: required behavior verified\n"}}}
	be := &backlog.Issues{Cfg: config.Load(cfgdir.OSEnv(), f.root), Tracker: remote, CountProof: proof.CountRows, LookupProof: proof.Lookup}
	if won, _, err := be.Claim("12", f.slot("ben").ClaimID()); err != nil || !won {
		t.Fatalf("claim=%v %v", won, err)
	}
	f.env.HostName = host.HerdrCore
	f.env.Snapshot = h.Capabilities().Snapshotter.Snapshot
	f.wire(func(w *worker.Env) { w.NewHost = func(host.Kind) host.Host { return h } })
	if native := h.Status(bg, "ben", handle); native != "idle" {
		t.Fatalf("fixture native status=%q", native)
	}
	polled, err := f.ws.Poll(bg, f.root, worker.PollOpts{Slot: "ben", Lines: 60})
	if err != nil || len(polled.Slots) != 1 || polled.Slots[0].State == worker.StateDone {
		t.Fatalf("native idle bypassed typed completion: %+v %v", polled, err)
	}
	if _, err = f.env.Status(bg, f.root); err != nil {
		t.Fatal(err)
	}
	if changed, err := be.Complete("12", backlog.CompleteInput{Reason: "done"}); changed || err == nil || !strings.Contains(err.Error(), "AC-1") {
		t.Fatalf("unmet acceptance bypassed: changed=%v err=%v", changed, err)
	}
	// Explicit unmet-reason is the backend's supported way to reach its proof
	// guard independently. It still must not close an issue without proof.
	if changed, err := be.Complete("12", backlog.CompleteInput{Reason: "done", UnmetReason: "test isolates the proof guard"}); changed || !errors.Is(err, backlog.ErrProofMissing) {
		t.Fatalf("missing proof bypassed: changed=%v err=%v", changed, err)
	}
	if accepted, err := f.env.Accept(bg, f.root, be, AcceptInput{ID: "12"}); err == nil || accepted.Changed {
		t.Fatalf("ordinary task used no-PR acceptance lane: %+v %v", accepted, err)
	}
	if remote.Issues[0].State != "open" || f.slot("ben").Task() != "12" || f.slot("ben").Handle() != handle {
		t.Fatal("native idle released or closed unverified issue")
	}
	for _, call := range remote.Calls {
		if call.Method == "close" {
			t.Fatal("issue gate reached tracker close")
		}
	}
	_, _, kills := peer.Counts()
	if kills != 0 {
		t.Fatal("gate failure wound down worker")
	}
}
