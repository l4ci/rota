package round

import (
	"testing"

	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/jsonx"
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
	}); err != nil {
		t.Fatal(err)
	}
	f.set.Roster = []string{"zed", "ben", "dana"}

	if _, err := f.transfer("12", "zed", nil); blockedBy(t, err) != BlockExternal {
		t.Fatalf("the fixture's external slot should be refused: %v", err)
	}
	name, ok := IdleSlot(worker.LoadRegistryTolerant(f.root), f.set.Roster, "", "")
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
	roster := []string{"ben", "cy", "dana"}
	var e Env
	names := func(ss []*worker.Slot) (out []string) {
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
