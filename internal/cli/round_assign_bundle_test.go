package cli

import (
	"strings"
	"testing"
)

// round assign --bundle on the scripted forge: #2 leads, #3..#5 are members.
func TestRoundAssignBundle(t *testing.T) {
	args := func(extra ...string) []string {
		return append([]string{"--json", "round", "assign", "2", "--agent", "dana", "--holder-pid", "4242"}, extra...)
	}
	t.Run("a repeated member or the lead is a usage error", func(t *testing.T) {
		f := newScopeFx(t, "warn", "")
		for _, b := range []string{"3,3", "2", "#2,3"} {
			code, out, errOut := rotaInWith(t, f.deps, f.root, args("--bundle", b, "--check-only")...)
			if code != 2 {
				t.Errorf("--bundle %s: exit %d, want 2: %s %s", b, code, out, errOut)
			}
		}
	})
	t.Run("check-only lists the bundle and each member", func(t *testing.T) {
		f := newScopeFx(t, "warn", "")
		code, out, errOut := rotaInWith(t, f.deps, f.root, args("--bundle", "3,4", "--check-only")...)
		if code != 0 {
			t.Fatalf("exit %d: %s %s", code, out, errOut)
		}
		d := data(t, out)
		bundle, _ := d["bundle"].([]any)
		members, _ := d["members"].([]any)
		if len(bundle) != 2 || bundle[0] != "3" || bundle[1] != "4" || len(members) != 2 {
			t.Fatalf("bundle %v members %v", bundle, members)
		}
		for i, id := range []string{"3", "4"} {
			m, _ := members[i].(map[string]any)
			if m["id"] != id || m["ready"] != true || m["checks"] == nil {
				t.Errorf("member %d: %v", i, m)
			}
		}
		if d["ready"] != true {
			t.Errorf("ready: %v", d["ready"])
		}
	})
	t.Run("a single issue carries no bundle fields", func(t *testing.T) {
		f := newScopeFx(t, "warn", "")
		code, out, errOut := rotaInWith(t, f.deps, f.root, args("--check-only")...)
		if code != 0 {
			t.Fatalf("exit %d: %s %s", code, out, errOut)
		}
		if d := data(t, out); d["bundle"] != nil || d["members"] != nil {
			t.Errorf("bundle fields on a single issue: %v", d)
		}
	})
	t.Run("past round.bundleMax is refused with exit 4", func(t *testing.T) {
		f := newScopeFx(t, "warn", `,"bundleMax":2`)
		code, out, errOut := rotaInWith(t, f.deps, f.root, args("--bundle", "3,4,5")...)
		if code != 4 {
			t.Fatalf("exit %d, want 4: %s %s", code, out, errOut)
		}
		if d := data(t, out); d["blockedBy"] != "bundle" || d["changed"] != false {
			t.Errorf("data: %v", d)
		}
		if !strings.Contains(out+errOut, "round.bundleMax") {
			t.Errorf("the refusal names the key: %s %s", out, errOut)
		}
	})
}
