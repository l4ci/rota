package cli

import (
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/roundcfg"
)

// treeLeaves is every verb path in the command tree, without the leading
// "rota ".
func treeLeaves() map[string]bool {
	out := map[string]bool{}
	for _, v := range VerbPaths(Tree()) {
		out["rota "+v] = true
	}
	return out
}

func TestSupportFencedVerbsRefuseOnSupport(t *testing.T) {
	root := supportProject(t)
	for _, path := range roundcfg.SupportActions(roundcfg.FenceRefused) {
		t.Run(path, func(t *testing.T) {
			code, env, _ := rotaRun(t, append([]string{"--json", "-C", root}, strings.Fields(path)[1:]...)...)
			if code != ExitRefused {
				t.Fatalf("exit %d, want %d: %v", code, ExitRefused, env)
			}
			if got := envObj(env, "data")["blockedBy"]; got != "role" {
				t.Errorf("blockedBy = %v", got)
			}
		})
	}
}

func TestSupportAllowedVerbsPassTheFence(t *testing.T) {
	// File mode, so no verb reaches for a forge CLI; the fence reads only the role.
	root := trackerProject(t, "")
	writeLocalConfig(t, root, `{"round":{"role":"support"}}`+"\n")
	// A sample: many allowed verbs launch agents, sleep or install into the
	// harness. The classify test keeps the tables disjoint, so no allowed verb
	// reaches the pre-verb fence.
	for _, path := range []string{"rota knowledge query", "rota knowledge topics", "rota config show", "rota round status", "rota ship strategy", "rota release version"} {
		t.Run(path, func(t *testing.T) {
			_, env, _ := rotaRun(t, append([]string{"--json", "-C", root}, strings.Fields(path)[1:]...)...)
			if got := envObj(env, "data")["blockedBy"]; got == "role" {
				t.Errorf("%s hit the support fence", path)
			}
		})
	}
}

// Every verb is classified: a new verb has to land in a roundcfg support
// table, so it cannot ship unfenced. Disjointness and the fenced set are
// tested in roundcfg without the tree.
func TestSupportTablesClassifyEveryVerb(t *testing.T) {
	leaves := treeLeaves()
	for leaf := range leaves {
		if roundcfg.SupportClass(leaf) == roundcfg.FenceUnclassified {
			t.Errorf("%s is in no support table", leaf)
		}
	}
	for _, class := range []roundcfg.FenceClass{roundcfg.FenceRefused, roundcfg.FenceInVerb, roundcfg.FenceAllowed} {
		for _, p := range roundcfg.SupportActions(class) {
			if !leaves[p] {
				t.Errorf("%s is in a support table but is not a verb", p)
			}
		}
	}
}
