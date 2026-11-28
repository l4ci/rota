package roundtick

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// notFromAssign are the kinds the scanned packages raise from other verbs only.
var notFromAssign = map[string]bool{
	"Config": true, "Role": true, "Held": true, "Registered": true, // support offer and return, adopt
	"NoSlotHolds": true, "NoIssues": true, "FindingMissing": true, "NoGist": true, // accept
	"BestOf": true, "NotBestOf": true, "NotAttempt": true, "AlreadyPicked": true, "AttemptRunning": true, // pick, switch
	"NotYourSlot": true, "SameSlot": true, "Healthy": true, "LiveAgent": true, "Unverifiable": true, "MergeInProgress": true, // move, switch
	"State": true, "Handle": true, "MaxBounces": true, // review, follow-up
	"ApprovalPending": true, "ApprovalDeclined": true, // worker approval
}

// #4465 AC-3: every blockedBy kind the packages on the assign path name is
// classified, or listed above as another verb's.
func TestRefusalKindsAreClassified(t *testing.T) {
	values := map[string]string{} // const name -> kind
	decl, err := parser.ParseFile(token.NewFileSet(), "../blockedby/blockedby.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	ast.Inspect(decl, func(n ast.Node) bool {
		if v, ok := n.(*ast.ValueSpec); ok && len(v.Values) == len(v.Names) {
			for i, name := range v.Names {
				if lit, ok := v.Values[i].(*ast.BasicLit); ok {
					values[name.Name], _ = strconv.Unquote(lit.Value)
				}
			}
		}
		return true
	})
	seen := 0
	for _, dir := range []string{"../round", "../worker", "../harness"} {
		files, err := filepath.Glob(dir + "/*.go")
		if err != nil || len(files) == 0 {
			t.Fatalf("no sources in %s: %v", dir, err)
		}
		for _, path := range files {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			f, err := parser.ParseFile(token.NewFileSet(), path, src, 0)
			if err != nil {
				t.Fatal(err)
			}
			ast.Inspect(f, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkg, ok := sel.X.(*ast.Ident)
				if !ok || pkg.Name != "blockedby" && (pkg.Name != "landing" || sel.Sel.Name != "GateRunning") {
					return true
				}
				seen++
				kind, declared := values[sel.Sel.Name]
				if !declared {
					t.Errorf("%s: %s.%s is not a declared kind", path, pkg.Name, sel.Sel.Name)
				} else if _, ok := refusalScopes[kind]; !ok && !notFromAssign[sel.Sel.Name] {
					t.Errorf("%s raises blockedby.%s: classify it in refusalScopes (refusal.go)", path, sel.Sel.Name)
				}
				return true
			})
		}
	}
	if seen == 0 {
		t.Fatal("the scan matched no blockedby selector")
	}
}
