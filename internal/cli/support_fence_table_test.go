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
	for _, path := range supportAllowed {
		t.Run(path, func(t *testing.T) {
			_, env, _ := rotaRun(t, append([]string{"--json", "-C", root}, strings.Fields(path)[1:]...)...)
			if got := envObj(env, "data")["blockedBy"]; got == "role" {
				t.Errorf("%s hit the support fence", path)
			}
		})
	}
}

// Every verb of a merge or tracked-write group is classified: a new verb has
// to land in supportFenced or supportAllowed, so it cannot ship unfenced.
func TestSupportTablesClassifyEveryVerb(t *testing.T) {
	groups := []string{"rota knowledge", "rota glossary", "rota map", "rota qa", "rota milestone", "rota release", "rota ship"}
	fenced, allowed := map[string]bool{}, map[string]bool{}
	for _, p := range supportFenced {
		fenced[p] = true
	}
	for _, p := range supportAllowed {
		if fenced[p] {
			t.Errorf("%s is in both tables", p)
		}
		allowed[p] = true
	}
	leaves := treeLeaves()
	for leaf := range leaves {
		if pathUnder(leaf, groups) && !fenced[leaf] && !allowed[leaf] {
			t.Errorf("%s is in neither supportFenced nor supportAllowed", leaf)
		}
	}
	for _, p := range []string{"rota worker gate", "rota worker train"} {
		if !fenced[p] {
			t.Errorf("%s is not fenced", p)
		}
	}
	for p := range fenced {
		if !leaves[p] {
			t.Errorf("supportFenced names %s, which is not a verb", p)
		}
	}
	for p := range allowed {
		if !leaves[p] {
			t.Errorf("supportAllowed names %s, which is not a verb", p)
		}
	}
}
