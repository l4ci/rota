package cli

import (
	"strings"
	"testing"
)

// `tier set` and `tier get` read a title as `hit` does: a case variant reaches
// the stored bullet's record, and an unknown title is refused without a write.
func TestKnowledgeTierResolvesStoredTitle(t *testing.T) {
	dir := knProject(t, false)
	set := knNew(t, dir, "", "knowledge", "tier", "set", "--topic", "Architecture", "--title", " alpha RULE ", "--tier", "deprecated", "--json")
	for _, want := range []string{`"title": "Alpha rule"`, `"previousTier": "confirmed"`} {
		if set.rc != 0 || !strings.Contains(set.stdout, want) {
			t.Errorf("set case variant: rc=%d, want %s in %s", set.rc, want, set.stdout)
		}
	}
	get := knNew(t, dir, "", "knowledge", "tier", "get", "--topic", "Architecture", "--title", "ALPHA rule", "--json")
	for _, want := range []string{`"title": "Alpha rule"`, `"tier": "deprecated"`} {
		if get.rc != 0 || !strings.Contains(get.stdout, want) {
			t.Errorf("get case variant: rc=%d, want %s in %s", get.rc, want, get.stdout)
		}
	}

	before := knTree(t, dir)["knowledge-tier.json"]
	for _, args := range [][]string{
		{"knowledge", "tier", "set", "--topic", "Architecture", "--title", "nosuch", "--tier", "deprecated"},
		{"knowledge", "tier", "get", "--topic", "Architecture", "--title", "nosuch"},
		{"knowledge", "tier", "set", "--topic", "Nosuch", "--title", "Alpha rule", "--tier", "deprecated"},
	} {
		if got := knNew(t, dir, "", args...); got.rc != 3 {
			t.Errorf("%v: rc = %d, want 3; %s", args, got.rc, got.stdout)
		}
	}
	if after := knTree(t, dir)["knowledge-tier.json"]; after != before {
		t.Errorf("refused call wrote the sidecar:\n%s", after)
	}
}
