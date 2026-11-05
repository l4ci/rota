package host

import (
	"context"
	"errors"
	"testing"
)

const claudeScreen = "⏺ working on it\n\n╭────────────╮\n│ > half a human draft │\n╰────────────╯\n"

func claudeCoreSpawn(t *testing.T) (Host, *coreFake, string) {
	t.Helper()
	h, f := coreFixture(t)
	f.app = "claude-code"
	f.screen = claudeScreen
	s, e := h.Spawn(context.Background(), SpawnOpts{Slot: "dana", Cwd: "/work", Launch: "claude", BootTimeout: 1})
	if e != nil {
		t.Fatalf("claude spawn readiness: %v", e)
	}
	return h, f, s
}

func TestCoreClaudeSpawnReadyOnNativeStatus(t *testing.T) {
	for _, state := range []string{"idle", "done"} {
		h, f := coreFixture(t)
		f.app, f.status = "claude-code", state
		f.screen = claudeScreen
		if _, e := h.Spawn(context.Background(), SpawnOpts{Slot: "dana", Cwd: "/work", Launch: "claude", BootTimeout: 1}); e != nil {
			t.Fatalf("%s: %v", state, e)
		}
		if f.observes != 0 {
			t.Fatalf("%s: styled composer observer used for claude", state)
		}
	}
}

func TestCoreClaudeSendSkipsComposerCheck(t *testing.T) {
	for _, tc := range []struct{ state, wantIf string }{{"idle", "idle"}, {"done", ""}, {"working", "working"}} {
		t.Run(tc.state, func(t *testing.T) {
			h, f, s := claudeCoreSpawn(t)
			f.status = tc.state
			e := h.Send(context.Background(), "dana", s, coreBrief(t, "task"))
			if e != nil && !errors.Is(e, ErrSubmitted) {
				t.Fatalf("send=%v", e)
			}
			if f.sends != 1 || len(f.sendIf) != 1 {
				t.Fatalf("sends=%d if=%v want %s", f.sends, f.sendIf, tc.wantIf)
			}
			if got, _ := f.sendIf[0].(string); got != tc.wantIf {
				t.Fatalf("sends=%d if=%v want %s", f.sends, f.sendIf, tc.wantIf)
			}
		})
	}
}

func TestCoreClaudeBlockedIsRefusedAsDialog(t *testing.T) {
	h, f, s := claudeCoreSpawn(t)
	f.status = "blocked"
	if e := h.Send(context.Background(), "dana", s, coreBrief(t, "task")); !errors.Is(e, ErrDialogOpen) {
		t.Fatalf("blocked=%v", e)
	}
	// Core reports idle but the pane shows a dialog: rota's own check refuses.
	f.status = "idle"
	f.screen = "──────────────\n Update available\n\n ❯ Install now\n   Later\n\n Enter to select · Esc to cancel\n"
	var d *DialogError
	if e := h.Send(context.Background(), "dana", s, coreBrief(t, "task")); !errors.As(e, &d) {
		t.Fatalf("dialog=%v", e)
	}
	if f.sends != 0 {
		t.Fatalf("typed into a dialog: %d", f.sends)
	}
}

func TestCoreCodexSendStillChecksComposer(t *testing.T) {
	h, f := coreFixture(t)
	s := coreSpawn(t, h)
	f.screen = "no composer here\n"
	if e := h.Send(context.Background(), "dana", s, coreBrief(t, "task")); e == nil {
		t.Fatal("codex sent without a recognized composer")
	}
	f.screen = "› my draft\n"
	if e := h.Send(context.Background(), "dana", s, coreBrief(t, "task2")); !errors.Is(e, ErrDraftOnPrompt) {
		t.Fatalf("draft=%v", e)
	}
	if f.sends != 0 {
		t.Fatalf("sends=%d", f.sends)
	}
	f.screen = "› \n"
	f.status = "done"
	h.Send(context.Background(), "dana", s, coreBrief(t, "task3"))
	if len(f.sendIf) != 1 || f.sendIf[0] != nil {
		t.Fatalf("codex done send if=%v", f.sendIf)
	}
}
