package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/testutil/coretest"
	"github.com/l4ci/rota/internal/worker"
)

func TestWorkerDispatchConfirmNotSubmitted(t *testing.T) {
	for _, tc := range []struct {
		name             string
		queued, explicit bool
	}{
		{"held-issue", false, false}, {"queued-pr", true, false}, {"explicit-pr-after-reassignment", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			queued := tc.queued
			dir := workerProject(t, `{"round":{"dispatch":"herdr-core"}}`)
			deps := testDeps()
			if tc.explicit {
				var forgeCalls []string
				deps = workerForge(t, &forgeCalls)
				gittest.Run(t, dir, "remote", "add", "origin", "https://github.com/o/r.git")
			}
			pinNow(deps, "2026-11-05T12:34:56Z")
			peer, h := coretest.New(t, "claude-code")
			useHost(deps, h)
			deps.Getenv = func(string) string { return "" }
			if code, out, errs := rotaInWith(t, deps, dir, "worker", "pool", "init", "--slots", "1", "--base", "main"); code != 0 {
				t.Fatalf("pool: %d %s %s", code, out, errs)
			}
			handle, err := h.Spawn(context.Background(), host.SpawnOpts{Slot: "w1", Cwd: filepath.Join(dir, ".worktrees", "w1"), Launch: "claude", BootTimeout: 1})
			if err != nil {
				t.Fatal(err)
			}
			prior := jsonx.NewObject()
			prior.Set("summary", "prior delivered relay")
			if err := worker.Update(dir, func(d *worker.Doc) {
				s := d.Slot("w1")
				s.Dispatch(handle, "T1", harness.Claude, "2026-11-05T00:00:00Z")
				if queued {
					if tc.explicit {
						s.SetTask("T2")
					} else {
						s.SetTask("")
					}
					d.QueuePR(worker.QueuedPR{From: "w1", Issue: "T1", PR: "https://github.com/o/r/pull/1", Relays: []any{prior}})
				} else {
					s.AppendRelay(prior)
				}
			}); err != nil {
				t.Fatal(err)
			}
			brief := filepath.Join(t.TempDir(), "relay")
			if err := os.WriteFile(brief, []byte("one recoverable relay"), 0600); err != nil {
				t.Fatal(err)
			}
			send := []string{"worker", "dispatch", "w1", "--relay", "--body-file", brief, "--json"}
			if tc.explicit {
				send = append(send, "--pr", "1")
			}
			peer.Mode("uncertain")
			if code, _, _ := rotaInWith(t, deps, dir, send...); code != 5 {
				t.Fatalf("uncertain relay exit=%d, want 5", code)
			}
			peer.Mode("")
			if code, _, _ := rotaInWith(t, deps, dir, send...); code != 5 {
				t.Fatalf("unconfirmed retry exit=%d, want 5", code)
			}
			if code, _, _ := rotaInWith(t, deps, dir, "worker", "dispatch", "w1", "--confirm-not-submitted", "--body-file", brief); code != 2 {
				t.Fatalf("confirmation combined with send exit=%d, want 2", code)
			}
			for _, mode := range []string{"replaced", "unreadable"} {
				peer.Mode(mode)
				if code, _, _ := rotaInWith(t, deps, dir, "worker", "dispatch", "w1", "--confirm-not-submitted"); code != 4 {
					t.Fatalf("%s delivery confirmation exit=%d, want 4", mode, code)
				}
			}
			failedRecord, _ := worker.LoadRegistryTolerant(dir).Slot("w1").Raw().Get("coreDelivery")
			if p := failedRecord.(*jsonx.Object); jsonx.Bool(p, "confirmedByHand") || jsonx.Str(p, "confirmedAt") != "" {
				t.Fatalf("refused confirmation wrote a manual audit: %v", p)
			}
			peer.Mode("")
			code, out, errs := rotaInWith(t, deps, dir, "worker", "dispatch", "w1", "--confirm-not-submitted", "--json")
			if code != 0 {
				t.Fatalf("confirmation: exit=%d %s %s", code, out, errs)
			}
			if d := data(t, out); d["delivery"] != "not-submitted" || d["changed"] != true {
				t.Fatalf("confirmation data=%v", d)
			}
			runs, sends, _ := peer.Counts()
			if runs != 1 || sends != 1 {
				t.Fatalf("confirmation sent input: runs=%d sends=%d", runs, sends)
			}
			reg := worker.LoadRegistryTolerant(dir)
			s := reg.Slot("w1")
			logs := s.Relays()
			if queued {
				logs = reg.QueuedFrom("w1").Relays
			}
			pending, _ := s.Raw().Get("coreDelivery")
			confirmation := pending.(*jsonx.Object)
			if !jsonx.Bool(confirmation, "confirmedByHand") || jsonx.Str(confirmation, "confirmedAt") != "2026-11-05T12:34:56Z" {
				t.Fatalf("manual confirmation audit missing: %v", confirmation)
			}
			if s.Handle() != handle || len(logs) != 1 || s.RelayPending() != 0 {
				t.Fatalf("reconciliation changed ownership or retained false provenance: %+v logs=%v", s.Raw(), logs)
			}
			if code, out, errs := rotaInWith(t, deps, dir, send...); code != 0 {
				t.Fatalf("retry: %d %s %s", code, out, errs)
			}
			pending, _ = worker.LoadRegistryTolerant(dir).Slot("w1").Raw().Get("coreDelivery")
			confirmation = pending.(*jsonx.Object)
			if !jsonx.Bool(confirmation, "confirmedByHand") || jsonx.Str(confirmation, "confirmedAt") != "2026-11-05T12:34:56Z" {
				t.Fatalf("retry erased manual confirmation audit: %v", confirmation)
			}
			sent := peer.Sent()
			if len(sent) != 2 || sent[0] != sent[1] || !strings.Contains(sent[1], `n="2"`) {
				t.Fatalf("retry changed original relay identity: %v", sent)
			}
			// Positive delivery evidence cannot be dismissed by the confirmation flag.
			if code, _, _ := rotaInWith(t, deps, dir, "worker", "dispatch", "w1", "--confirm-not-submitted"); code != 4 {
				t.Fatalf("picked-up delivery was cleared: exit=%d", code)
			}
		})
	}
}
