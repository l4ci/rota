package worker

import (
	"reflect"
	"testing"

	"github.com/l4ci/rota/internal/ledger"
)

func TestSlotHoldingAndHolders(t *testing.T) {
	d := holderDoc() // ben: branch ben/7-x, bundle #8; queued PR for #9
	task := NewSlot("dana", "dana/12-y", "/wt/dana", "main", "")
	task.Bind(Binding{Task: "#12"})
	file := NewSlot("kit", "kit/b07-z", "/wt/kit", "main", "")
	file.Bind(Binding{Task: "b07"})
	rival := NewSlot("nia", "nia/12-y", "/wt/nia", "main", "")
	rival.Bind(Binding{Task: "12"})
	d.AppendSlot(task)
	d.AppendSlot(file)
	d.AppendSlot(rival)
	d.AppendSlot(NewSlot("sam", ParkBranch("sam"), "/wt/sam", "main", ""))
	reg := d.Registry

	cases := []struct {
		name    string
		id      string
		first   string
		holders []string
	}{
		{"branch issue", "7", "ben", []string{"ben"}},
		{"branch issue, hash", "#7", "ben", []string{"ben"}},
		{"task, hash", "#12", "dana", []string{"dana", "nia"}},
		{"task, bare", "12", "dana", []string{"dana", "nia"}},
		{"file-mode id, lower case", "b07", "kit", []string{"kit"}},
		{"file-mode id, upper case", "B07", "kit", []string{"kit"}},
		{"bundle member is no holder", "#8", "", nil},
		{"queued PR is no slot", "#9", "", nil},
		{"unheld", "#99", "", nil},
		{"empty id matches no idle slot", "", "", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ""
			if s := reg.SlotHolding(c.id); s != nil {
				got = s.Name()
			}
			if got != c.first {
				t.Errorf("SlotHolding = %q, want %q", got, c.first)
			}
			var names []string
			for _, s := range reg.Holders(c.id, HolderOpts{}) {
				names = append(names, s.Name())
			}
			if !reflect.DeepEqual(names, c.holders) {
				t.Errorf("Holders = %v, want %v", names, c.holders)
			}
		})
	}

	var names []string
	for _, s := range reg.Holders("12", HolderOpts{Skip: "dana"}) {
		names = append(names, s.Name())
	}
	if !reflect.DeepEqual(names, []string{"nia"}) {
		t.Errorf("Holders skipping dana = %v, want [nia]", names)
	}
	// A bundle member is found by the bundle variant, not by the slot holder.
	if s, _ := reg.BundleHolder("#8", HolderOpts{}); s == nil || s.Name() != "ben" {
		t.Errorf("BundleHolder #8 = %v, want ben", s)
	}
	if s := ledgerSlot(reg, ledger.Entry{Issue: "b07"}); s == nil || s.Name() != "kit" {
		t.Errorf("ledgerSlot b07 = %v, want kit", s)
	}
}
