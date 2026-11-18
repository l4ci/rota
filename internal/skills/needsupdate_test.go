package skills

import "testing"

func TestNeedsUpdate(t *testing.T) {
	ok := RootStatus{Installed: true, Mode: ModeStub, Current: true}
	with := func(f func(*RootStatus)) RootStatus { r := ok; f(&r); return r }
	cases := []struct {
		name     string
		root     RootStatus
		damaged  bool
		needsUpd bool
	}{
		{"current stub", ok, false, false},
		{"stale", with(func(r *RootStatus) { r.Current = false }), true, true},
		{"edited", with(func(r *RootStatus) { r.Edited = []string{"a"} }), true, true},
		{"missing", with(func(r *RootStatus) { r.Missing = []string{"a"} }), true, true},
		{"current full install", with(func(r *RootStatus) { r.Mode = ModeFull }), false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.root.Damaged(); got != c.damaged {
				t.Errorf("Damaged = %v, want %v", got, c.damaged)
			}
			if got := (Report{Roots: []RootStatus{c.root}}).NeedsUpdate(); got != c.needsUpd {
				t.Errorf("NeedsUpdate = %v, want %v", got, c.needsUpd)
			}
		})
	}
	t.Run("nothing installed", func(t *testing.T) {
		if !(Report{Roots: []RootStatus{{}}}).NeedsUpdate() {
			t.Error("want true")
		}
	})
	// Plugin roots never carry a Mode and are updated by Claude Code, so they
	// never need a rota skills update, damaged or not.
	t.Run("installed plugin root ignored", func(t *testing.T) {
		plug := RootStatus{Root: Root{Plugin: true}, Installed: true}
		if (Report{Roots: []RootStatus{plug}}).NeedsUpdate() {
			t.Error("want false for a plugin root")
		}
	})
	t.Run("uninstalled root ignored", func(t *testing.T) {
		if (Report{Roots: []RootStatus{ok, {}}}).NeedsUpdate() {
			t.Error("want false")
		}
	})
}
