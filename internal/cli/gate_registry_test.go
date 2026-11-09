package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/gate"
)

// gateCallerVerbs names the verb each function that clears a manual gate
// serves. A function missing here fails the test below, so a new enforcing verb
// cannot skip the registry: register it in gate.Registry and list it here.
var gateCallerVerbs = map[string]string{
	"shipMerge":      "ship merge",
	"shipPRMerge":    "ship pr-merge",
	"prepareGate":    "worker gate",
	"prepareTrain":   "worker train",
	"brFile":         "bugreport file",
	"trSuggest":      "tracker suggest-upstream",
	"debugReset":     "debug reset",
	"workFixReset":   "debug work-fix reset",
	"releasePush":    "release push",
	"releasePublish": "release publish",
}

// gateConsts maps the gate constants a clearGate call names to their values.
var gateConsts = map[string]string{
	"TagPush": gate.TagPush, "ReleasePublish": gate.ReleasePublish, "PublicFiling": gate.PublicFiling,
	"MergeApproval": gate.MergeApproval, "DebugReset": gate.DebugReset, "BugreportFile": gate.BugreportFile,
	"FreezeClear": gate.FreezeClear,
}

// TestRegistryListsEveryGateClearingVerb scans the non-test sources for calls
// to clearGate, clearMerge and mergeApproval and requires each calling verb
// to appear under its gate in gate.Registry, and every registered verb of an
// enforced gate to have such a call.
func TestRegistryListsEveryGateClearingVerb(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	called := map[string]map[string]bool{} // gate -> verbs found calling it
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range file.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil || fn.Name.Name == "clearMerge" || fn.Name.Name == "mergeApproval" || fn.Name.Name == "clearGate" {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				id, ok := call.Fun.(*ast.Ident)
				if !ok {
					return true
				}
				var name string
				switch id.Name {
				case "clearMerge", "mergeApproval":
					name = gate.MergeApproval
				case "clearGate":
					sel, ok := call.Args[1].(*ast.SelectorExpr)
					if !ok {
						t.Errorf("%s: clearGate gate argument is not a gate constant", fn.Name.Name)
						return true
					}
					name = gateConsts[sel.Sel.Name]
				default:
					return true
				}
				verb, ok := gateCallerVerbs[fn.Name.Name]
				if !ok {
					t.Errorf("%s clears gate %q but is not in gateCallerVerbs", fn.Name.Name, name)
					return true
				}
				if called[name] == nil {
					called[name] = map[string]bool{}
				}
				called[name][verb] = true
				return true
			})
		}
	}
	for _, g := range gate.Registry {
		var want []string
		for v := range called[g.Name] {
			want = append(want, v)
		}
		sort.Strings(want)
		got := append([]string(nil), g.Verbs...)
		sort.Strings(got)
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("gate %s: registry verbs %v, verbs that clear it %v", g.Name, got, want)
		}
	}
}
