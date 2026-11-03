package worker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/testutil/coretest"
)

func TestCoreAdapterPendingCreateCannotEscapePoolCleanup(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(map[bool]string{false: "reconciled", true: "unknown"}[missing], func(t *testing.T) {
			dir := newProject(t, "{\"round\":{\"dispatch\":\"herdr-core\"}}")
			if _, err := goInit(t, dir, InitOpts{Slots: 1, Base: "main"}); err != nil {
				t.Fatal(err)
			}
			peer, h := coretest.New(t, harness.Codex)
			peer.Mode("uncertain-run")
			rig := &codexRig{loggedIn: true, missing: map[string]bool{"herdr": true}}
			e := rig.env(h)
			if _, err := e.Dispatch(bg, dir, DispatchOpts{Slot: "w1", Task: "T1", Kind: harness.Codex, BodyFile: writeBrief(t, "not sent")}); err == nil {
				t.Fatal("unknown creation acknowledged")
			}
			s := LoadRegistryTolerant(dir).Slot("w1")
			if s == nil || !strings.HasPrefix(s.Handle(), "core-pending:") {
				t.Fatal("unknown creation lost pool cleanup authority")
			}
			wt := s.Worktree()
			if missing {
				peer.Mode("missing")
			} else {
				peer.Mode("")
			}
			reaped, err := e.Reap(dir, []string{"w1"}, false)
			_, sends, kills := peer.Counts()
			if sends != 0 {
				t.Fatal("unready creation received input")
			}
			if missing {
				if err == nil || len(reaped) != 0 || kills != 0 || LoadRegistryTolerant(dir).Slot("w1") == nil {
					t.Fatalf("unknown cleanup discarded evidence: reaped=%v err=%v kills=%d", reaped, err, kills)
				}
				if _, err := os.Stat(wt); err != nil {
					t.Fatal("unknown worker worktree removed")
				}
			} else {
				if err != nil || len(reaped) != 1 || kills != 1 || LoadRegistryTolerant(dir).Slot("w1") != nil {
					t.Fatalf("reconciled cleanup failed: reaped=%v err=%v kills=%d", reaped, err, kills)
				}
				if _, err := os.Stat(wt); !os.IsNotExist(err) {
					t.Fatalf("reaped worktree retained: %v", err)
				}
			}
		})
	}
}

func TestCoreAdapterDispatchAndBusyRelay(t *testing.T) {
	dir := newProject(t, `{"round":{"dispatch":"herdr-core"}}`)
	if _, err := goInit(t, dir, InitOpts{Slots: 1, Base: "main"}); err != nil {
		t.Fatal(err)
	}
	peer, h := coretest.New(t, harness.Codex)
	rig := &codexRig{loggedIn: true, missing: map[string]bool{"herdr": true}}
	e := rig.env(h)
	e.Getenv = func(string) string { return "" }
	brief := "implement one task\nwith literal $HOME and `code`\n"
	task := DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, brief), Task: "T1", Kind: harness.Codex}
	result, err := e.Dispatch(bg, dir, task)
	if err != nil {
		t.Fatal(err)
	}
	handle := result.Handle
	runs, sends, _ := peer.Counts()
	if runs != 1 || sends != 1 || handle == "" {
		t.Fatalf("dispatch runs=%d sends=%d handle=%q", runs, sends, handle)
	}
	sent := peer.Sent()[0]
	if !strings.Contains(sent, brief) || !strings.Contains(sent, "--- ROTA-SIG ") {
		t.Fatalf("unsigned or changed brief: %q", sent)
	}
	run := peer.Run()
	if run["cwd"] != filepath.Join(dir, ".worktrees", "w1") || run["no_parent"] != true {
		t.Fatalf("spawn=%v", run)
	}
	if strings.Contains(run["line"].(string), "CODEX_HOME=") || rig.ran("herdr") != 0 {
		t.Fatal("default account or legacy integration changed")
	}
	peer.Busy()
	relay := DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "answer once\n"), Relay: true}
	response, err := e.Dispatch(bg, dir, relay)
	if err != nil || !response.Queued {
		t.Fatalf("busy relay=%+v %v", response, err)
	}
	if _, err = e.Dispatch(bg, dir, relay); err != nil {
		t.Fatal(err)
	}
	runs, sends, _ = peer.Counts()
	if runs != 1 || sends != 2 {
		t.Fatalf("retry duplicated task/relay: runs=%d sends=%d", runs, sends)
	}
	s := LoadRegistryTolerant(dir).Slot("w1")
	if s.Handle() != handle || len(s.Relays()) != 1 || s.State() == StateDone {
		t.Fatalf("retry changed authority or identity: %+v", s.Raw())
	}
}

func TestCoreAdapterDispatchUnknownRetryRetainsSignedPayload(t *testing.T) {
	dir := newProject(t, `{"round":{"dispatch":"herdr-core"}}`)
	if _, err := goInit(t, dir, InitOpts{Slots: 1, Base: "main"}); err != nil {
		t.Fatal(err)
	}
	peer, h := coretest.New(t, harness.Codex)
	peer.Mode("uncertain")
	rig := &codexRig{loggedIn: true, missing: map[string]bool{"herdr": true}}
	e := rig.env(h)
	e.Getenv = func(string) string { return "" }
	task := DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "work once\n"), Task: "T1", Kind: harness.Codex}
	if _, err := e.Dispatch(bg, dir, task); err == nil {
		t.Fatal("uncertain transport acknowledged")
	}
	handle := LoadRegistryTolerant(dir).Slot("w1").Handle()
	peer.Mode("")
	if _, err := e.Dispatch(bg, dir, task); err == nil {
		t.Fatal("unknown without pickup evidence must remain actionable")
	}
	runs, sends, _ := peer.Counts()
	if runs != 1 || sends != 1 || LoadRegistryTolerant(dir).Slot("w1").Handle() != handle {
		t.Fatalf("uncertain retry replayed: runs=%d sends=%d", runs, sends)
	}
	if !strings.Contains(peer.Sent()[0], "--- ROTA-SIG ") {
		t.Fatal("original uncertain payload was unsigned")
	}
}
