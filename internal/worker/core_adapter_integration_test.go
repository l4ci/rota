package worker

import (
	"encoding/json"
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

// Drive native-idle retained captures through worker relay bookkeeping and the
// real host adapter, then prove the same relay can be sent once after clearing.
func TestCoreAdapterRelayRetainedComposer(t *testing.T) {
	var fixture struct {
		Cols, Rows int
		Cases      []struct{ Name, State, Plain, Paint string }
	}
	b, err := os.ReadFile("../host/testdata/core-retained-drafts.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(b, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, capture := range fixture.Cases {
		if capture.Name != "leading-newline-draft" && capture.Name != "trailing-glyph-draft" {
			continue
		}
		t.Run(capture.Name, func(t *testing.T) {
			dir := newProject(t, `{"round":{"dispatch":"herdr-core"}}`)
			if _, err := goInit(t, dir, InitOpts{Slots: 1, Base: "main"}); err != nil {
				t.Fatal(err)
			}
			peer, h := coretest.New(t, harness.Codex)
			rig := &codexRig{loggedIn: true, missing: map[string]bool{"herdr": true}}
			e := rig.env(h)
			initial, err := e.Dispatch(bg, dir, DispatchOpts{Slot: "w1", Task: "T1", Kind: harness.Codex, BodyFile: writeBrief(t, "initial task")})
			if err != nil {
				t.Fatal(err)
			}
			peer.Capture(capture.Plain, capture.Paint, fixture.Cols, fixture.Rows)
			relay := DispatchOpts{Slot: "w1", Relay: true, BodyFile: writeBrief(t, "one relay after the human draft is cleared")}
			if _, err = e.Dispatch(bg, dir, relay); err == nil || !strings.Contains(err.Error(), "draft") {
				t.Fatalf("retained input did not refuse relay: %v", err)
			}
			runs, sends, _ := peer.Counts()
			if runs != 1 || sends != 1 {
				t.Fatalf("retained input was mutated: runs=%d sends=%d", runs, sends)
			}
			slot := LoadRegistryTolerant(dir).Slot("w1")
			if slot.Handle() != initial.Handle || len(slot.Relays()) != 0 {
				t.Fatal("refused relay changed session or logged delivery")
			}
			for _, empty := range fixture.Cases {
				if empty.Name == "leading-newline-cleared" {
					peer.Capture(empty.Plain, empty.Paint, fixture.Cols, fixture.Rows)
				}
			}
			if _, err = e.Dispatch(bg, dir, relay); err != nil {
				t.Fatal(err)
			}
			runs, sends, _ = peer.Counts()
			if runs != 1 || sends != 2 || len(LoadRegistryTolerant(dir).Slot("w1").Relays()) != 1 {
				t.Fatalf("cleared relay retry duplicated or lost delivery: runs=%d sends=%d", runs, sends)
			}
		})
	}
}

func TestCoreAdapterUnknownTaskCanBeConfirmedAndRetried(t *testing.T) {
	dir := newProject(t, `{"round":{"dispatch":"herdr-core"}}`)
	if _, err := goInit(t, dir, InitOpts{Slots: 1, Base: "main"}); err != nil {
		t.Fatal(err)
	}
	peer, h := coretest.New(t, harness.Codex)
	rig := &codexRig{loggedIn: true, missing: map[string]bool{"herdr": true}}
	e := rig.env(h)
	e.Getenv = func(string) string { return "" }
	task := DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "original unknown assignment"), Task: "T1", Kind: harness.Codex}
	peer.Mode("uncertain")
	if _, err := e.Dispatch(bg, dir, task); err == nil {
		t.Fatal("uncertain task acknowledged")
	}
	handle := LoadRegistryTolerant(dir).Slot("w1").Handle()
	peer.Mode("")
	if err := e.ConfirmCoreNotSubmitted(bg, dir, "w1"); err != nil {
		t.Fatal(err)
	}
	if !LoadRegistryTolerant(dir).Slot("w1").Unsent() {
		t.Fatal("confirmation did not report not submitted")
	}
	if _, err := e.Dispatch(bg, dir, task); err != nil {
		t.Fatal(err)
	}
	sent := peer.Sent()
	runs, sends, _ := peer.Counts()
	if runs != 1 || sends != 2 || sent[0] != sent[1] || LoadRegistryTolerant(dir).Slot("w1").Handle() != handle {
		t.Fatalf("recovery replaced original assignment: runs=%d sends=%d payloads=%v", runs, sends, sent)
	}
}
