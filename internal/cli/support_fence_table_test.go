package cli

import (
	"strings"
	"testing"
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
	for _, path := range supportFenced {
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

// Every verb is classified: a new verb has to land in supportFenced,
// supportInVerb or supportAllowed, so it cannot ship unfenced.
func TestSupportTablesClassifyEveryVerb(t *testing.T) {
	seen := map[string]string{}
	for name, table := range map[string][]string{"supportFenced": supportFenced, "supportInVerb": supportInVerb, "supportAllowed": supportAllowed} {
		for _, p := range table {
			if other, dup := seen[p]; dup {
				t.Errorf("%s is in both %s and %s", p, other, name)
			}
			seen[p] = name
		}
	}
	leaves := treeLeaves()
	for leaf := range leaves {
		if seen[leaf] == "" {
			t.Errorf("%s is in no support table", leaf)
		}
	}
	for p, table := range seen {
		if !leaves[p] {
			t.Errorf("%s names %s, which is not a verb", table, p)
		}
	}
	for _, p := range []string{"rota worker gate", "rota worker train", "rota spike add", "rota init", "rota update", "rota migrate config", "rota config fill"} {
		if seen[p] != "supportFenced" {
			t.Errorf("%s is not fenced", p)
		}
	}
}
