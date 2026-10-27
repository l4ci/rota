package worker

import "testing"

func TestBundleHolder(t *testing.T) {
	reg := holderDoc().Registry
	cases := []struct {
		name     string
		id       string
		opts     HolderOpts
		slot, pr string // want slot name / queued PR
	}{
		{"member of slot", "#8", HolderOpts{}, "ben", ""},
		{"member of slot, lead scope", "#8", HolderOpts{Lead: true}, "ben", ""},
		{"slot lead is no member", "#7", HolderOpts{}, "", ""},
		{"slot lead, lead scope", "#7", HolderOpts{Lead: true}, "ben", ""},
		{"member of queued PR", "#10", HolderOpts{}, "", "#90"},
		{"queued lead is no member", "#9", HolderOpts{}, "", ""},
		{"queued lead, lead scope", "#9", HolderOpts{Lead: true}, "", "#90"},
		{"resume skips own slot", "#8", HolderOpts{Lead: true, Skip: "ben"}, "", ""},
		{"resume skips own slot lead", "#7", HolderOpts{Lead: true, Skip: "ben"}, "", ""},
		{"skip of another slot keeps match", "#8", HolderOpts{Skip: "dana"}, "ben", ""},
		{"skip leaves queued PRs", "#10", HolderOpts{Skip: "ben"}, "", "#90"},
		{"normalized id", "8", HolderOpts{}, "ben", ""},
		{"unheld", "#99", HolderOpts{Lead: true}, "", ""},
		{"empty", "", HolderOpts{Lead: true}, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, q := reg.BundleHolder(c.id, c.opts)
			gotSlot, gotPR := "", ""
			if s != nil {
				gotSlot = s.Name()
			}
			if q != nil {
				gotPR = q.PR
			}
			if gotSlot != c.slot || gotPR != c.pr {
				t.Fatalf("slot=%q pr=%q, want slot=%q pr=%q", gotSlot, gotPR, c.slot, c.pr)
			}
		})
	}
}
