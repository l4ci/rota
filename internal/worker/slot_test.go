package worker

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/jsonx"
)

func render(t *testing.T, s *Slot) string {
	t.Helper()
	b, err := jsonx.MarshalCompact(s.Raw())
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func boundSlot() *Slot {
	s := NewSlot("ben", "ben/5-x", "/wt/ben", "main", "tab1")
	s.Bind(Binding{Task: "#5", ClaimID: "ben@1", Kind: "task", Tier: "standard", Model: "sonnet", TierReason: "size"})
	s.SetPR("https://example.test/pull/9")
	_ = s.MarkState("busy", "t1")
	return s
}

// One table of transitions: every verb that frees a slot lands on the same
// record, whichever field set it used to clear.
func TestSlotTransitions(t *testing.T) {
	for _, tc := range []struct {
		name string
		do   func(*Slot)
		want string
	}{
		{"unbind", func(s *Slot) { s.Unbind() },
			`{"name":"ben","branch":"ben/5-x","worktree":"/wt/ben","base":"main","handle":"tab1","state":"busy","task":null,"pr":null,"relays":[],"configDir":null,"activeAt":"t1"}`},
		{"park keeps handle", func(s *Slot) { s.Park(false) },
			`{"name":"ben","branch":"park/ben","worktree":"/wt/ben","base":"main","handle":"tab1","state":"idle","task":null,"pr":null,"relays":[],"configDir":null,"activeAt":"t1"}`},
		{"park drops handle", func(s *Slot) { s.Park(true) },
			`{"name":"ben","branch":"park/ben","worktree":"/wt/ben","base":"main","handle":null,"state":"idle","task":null,"pr":null,"relays":[],"configDir":null,"activeAt":"t1"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := boundSlot()
			tc.do(s)
			got := render(t, s)
			// The bound fixture's keys follow activeAt-less creation order; compare
			// on the fields a verb is responsible for.
			var g, w map[string]any
			_ = json.Unmarshal([]byte(got), &g)
			_ = json.Unmarshal([]byte(tc.want), &w)
			for _, k := range []string{"task", "pr", "claimId", "kind", "tier", "model", "tierReason", "state", "branch", "handle"} {
				if g[k] != w[k] {
					t.Errorf("%s: got %v want %v (%s)", k, g[k], w[k], got)
				}
			}
		})
	}
}

func TestSlotMarkStateRejectsUnknown(t *testing.T) {
	s := boundSlot()
	if err := s.MarkState("bogus", "t"); err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("want rejection, got %v", err)
	}
	if s.State() != "busy" {
		t.Errorf("state changed to %q", s.State())
	}
	if err := s.MarkState("DONE", "t2"); err != nil || s.State() != "done" || s.ActiveAt() != "t2" {
		t.Errorf("DONE: %v %q %q", err, s.State(), s.ActiveAt())
	}
}

func TestSlotBindUnbindLeavesNothingOfLastIssue(t *testing.T) {
	s := boundSlot()
	s.Unbind()
	if s.Task()+s.ClaimID()+s.Kind()+s.Tier()+s.Model()+s.TierReason()+s.PR() != "" {
		t.Errorf("leftover binding: %s", render(t, s))
	}
}

func TestSlotFileUnchangedOnDisk(t *testing.T) {
	root := t.TempDir()
	if err := UpdateDoc(root, func(doc *jsonx.Object) { AppendSlot(doc, NewSlot("ben", "park/ben", "/wt", "main", "")) }); err != nil {
		t.Fatal(err)
	}
	found, err := UpdateSlot(root, "ben", func(s *Slot) { s.Bind(Binding{Task: "#1", ClaimID: "ben@1"}); _ = s.MarkState("busy", "") })
	if err != nil || !found {
		t.Fatal(found, err)
	}
	reg := LoadRegistry(root)
	got := render(t, reg.Slot("ben"))
	want := `{"name": "ben", "branch": "park/ben", "worktree": "/wt", "base": "main", "handle": null, "state": "busy", "task": "#1", "pr": null, "relays": [], "configDir": null, "claimId": "ben@1"}`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}
