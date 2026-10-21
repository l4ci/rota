package round

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/proc"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/worker"
)

func TestIdleSlotMatchesKindAndAccountAndSkipsHeldAndExternal(t *testing.T) {
	root := t.TempDir()
	writeRegistry(t, root,
		slot(root, "ext", "park/ext", func(o *jsonx.Object) { o.Set("kind", worker.KindExternal) }),
		slot(root, "ben", "ben/5-x", nil),
		slot(root, "cy", "park/cy", func(o *jsonx.Object) { o.Set("kind", harness.Codex); o.Set("codexAccount", "work") }),
		slot(root, "dana", "park/dana", func(o *jsonx.Object) { o.Set("account", "b") }),
		slot(root, "eli", "park/eli", func(o *jsonx.Object) { o.Set("account", "b") }),
		slot(root, "fay", "park/fay", func(o *jsonx.Object) { o.Set("account", "b"); o.Set("state", "busy") }),
	)
	reg := worker.LoadRegistryTolerant(root)
	roster := []string{"ext", "ben", "cy", "dana", "eli", "fay", "gone"}
	for _, c := range []struct{ kind, account, want string }{
		{"", "b", "dana"},
		{harness.Codex, "work", "cy"},
		{harness.Codex, "", ""},
		{"", "", ""}, // ext is idle and on no account, but adopted: never a receiver
	} {
		if got, ok := IdleSlot(reg, roster, c.kind, c.account); got != c.want || ok != (c.want != "") {
			t.Errorf("IdleSlot(%q, %q) = %q, %v; want %q", c.kind, c.account, got, ok, c.want)
		}
	}
	if got := CountIdle(reg, roster); got != 3 {
		t.Errorf("idle count = %d, want 3 (cy, dana, eli; fay is busy)", got)
	}
}

// The limit watcher hands its pick straight to Transfer, so a slot IdleSlot
// returns must never be one Transfer refuses.
func TestIdleSlotNeverPicksASlotTransferRefuses(t *testing.T) {
	f := newMoveFx(t)
	if err := worker.Update(f.root, func(d *worker.Doc) {
		reg := worker.LoadRegistryTolerant(f.root)
		ext := slot(f.root, "zed", "park/zed", func(o *jsonx.Object) { o.Set("kind", worker.KindExternal) })
		d.SetSlots(append(reg.Slots(), ext))
		d.SetSlotCount(3)
	}); err != nil {
		t.Fatal(err)
	}
	f.set.Roster = []string{"zed", "ben", "dana"}

	if _, err := f.transfer("12", "zed", nil); blockedBy(t, err) != BlockExternal {
		t.Fatalf("the fixture's external slot should be refused: %v", err)
	}
	// The watcher offers only the round's active slots (cli buildLimits).
	reg := worker.LoadRegistryTolerant(f.root)
	active, _ := Members(reg, f.set)
	name, ok := IdleSlot(reg, active, "", "")
	if !ok || name != "dana" {
		t.Fatalf("idle receiver = %q, %v; want dana (zed is external, ben holds 12)", name, ok)
	}
	if _, err := f.transfer("12", name, nil); err != nil {
		t.Fatalf("Transfer refused the slot IdleSlot picked: %v", err)
	}
}

func TestPickSlotsOrderIsResumeIdleParkable(t *testing.T) {
	root := t.TempDir()
	writeRegistry(t, root,
		slot(root, "ben", "park/ben", nil),
		slot(root, "cy", "cy/7-x", nil),
		slot(root, "dana", "dana/9-x", nil), // holds another issue, no PR: not parkable
	)
	reg := worker.LoadRegistryTolerant(root)
	roster := roundcfg.Settings{Roster: []string{"ben", "cy", "dana"}}
	var e Env
	names := func(ss []*worker.Slot, _ []string) (out []string) {
		for _, s := range ss {
			out = append(out, s.Name())
		}
		return
	}
	if got := names(e.pickSlots(bg, reg, roster, "7", 3)); len(got) != 2 || got[0] != "cy" || got[1] != "ben" {
		t.Errorf("resume then idle, busy dropped: %v", got)
	}
	if got := names(e.pickSlots(bg, reg, roster, "8", 1)); len(got) != 1 || got[0] != "ben" {
		t.Errorf("idle with nothing to resume: %v", got)
	}
	if got := names(e.pickSlots(bg, reg, roster, "8", 2, reg.Slot("dana"))); len(got) != 2 || got[0] != "dana" || got[1] != "ben" {
		t.Errorf("a pre-chosen slot leads and is not listed twice: %v", got)
	}
}

func TestPickSlotsSkipsAccountsWithoutLogin(t *testing.T) {
	root, good, bad := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(good, ".credentials.json"), []byte(`{"claudeAiOauth":{"accessToken":"t"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	writeRegistry(t, root,
		slot(root, "ben", "park/ben", func(o *jsonx.Object) { o.Set("configDir", bad) }),
		slot(root, "cy", "park/cy", func(o *jsonx.Object) { o.Set("configDir", good) }),
		slot(root, "dana", "park/dana", nil), // no config dir: nothing to check
	)
	reg := worker.LoadRegistryTolerant(root)
	set := roundcfg.Settings{Roster: []string{"ben", "cy", "dana"}}
	var e Env
	got, skipped := e.pickSlots(bg, reg, set, "8", 3)
	if len(got) != 2 || got[0].Name() != "cy" || got[1].Name() != "dana" {
		t.Errorf("picked %v, want cy then dana", got)
	}
	if len(skipped) != 1 || !strings.Contains(skipped[0], "ben") || !strings.Contains(skipped[0], "no credentials file") {
		t.Errorf("skipped = %v, want ben with its problem", skipped)
	}
}

// Codex has no usage meter, so its slots are exempt from the login check even
// when their config dir holds no credentials.
func TestSlotLoginProblemExemptsCodex(t *testing.T) {
	empty := t.TempDir()
	root := t.TempDir()
	writeRegistry(t, root,
		slot(root, "ben", "park/ben", func(o *jsonx.Object) { o.Set("configDir", empty) }),
		slot(root, "cy", "park/cy", func(o *jsonx.Object) { o.Set("kind", harness.Codex); o.Set("configDir", empty) }),
	)
	reg := worker.LoadRegistryTolerant(root)
	if (Env{}).SlotLoginProblem(reg.Slot("ben")) == "" {
		t.Error("a claude slot with no credentials should have a login problem")
	}
	if why := (Env{}).SlotLoginProblem(reg.Slot("cy")); why != "" {
		t.Errorf("codex slot: %q, want no login problem", why)
	}
	got, skipped := Env{}.pickSlots(bg, reg, roundcfg.Settings{Roster: []string{"cy"}}, "8", 1)
	if len(got) != 1 || got[0].Name() != "cy" || len(skipped) != 0 {
		t.Errorf("picked %v skipped %v, want cy picked", got, skipped)
	}
}

// The login check reads through Env.Login, so a darwin Keychain entry makes a
// slot with no credentials file usable, and a miss names the Keychain.
func TestSlotLoginProblemReadsThroughEnvLogin(t *testing.T) {
	dir := t.TempDir()
	root := t.TempDir()
	writeRegistry(t, root, slot(root, "ben", "park/ben", func(o *jsonx.Object) { o.Set("configDir", dir) }))
	s := worker.LoadRegistryTolerant(root).Slot("ben")
	keychain := func(blob string) worker.Credentials {
		return worker.Credentials{GOOS: "darwin", Run: func(context.Context, proc.Cmd) (proc.Result, error) {
			if blob == "" {
				return proc.Result{ExitCode: 44}, nil
			}
			return proc.Result{Stdout: blob + "\n"}, nil
		}}
	}
	if why := (Env{Login: keychain(`{"claudeAiOauth":{"accessToken":"kc"}}`)}).SlotLoginProblem(s); why != "" {
		t.Errorf("keychain entry: %q, want usable", why)
	}
	if why := (Env{Login: keychain("")}).SlotLoginProblem(s); !strings.Contains(why, "Keychain") {
		t.Errorf("keychain miss: %q, want it to name the Keychain", why)
	}
}
