package host

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every adapter classifies an unreadable brief file the same way: nothing was
// sent, so the class is ErrNotSubmitted and never ErrMaybeSubmitted.
func TestSendBriefReadFailureIsNotSubmittedOnEveryAdapter(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent.md")
	hosts := map[string]func() (Host, string){
		"tmux": func() (Host, string) {
			return New(Tmux, deps(&fake{handler: func(string, []string) Result { return Result{} }}, nil, &clock{})), "rota:w1"
		},
		"herdr": func() (Host, string) {
			return New(Herdr, deps(&fake{handler: func(string, []string) Result { return Result{} }}, herdrEnv, &clock{})), "w9:t7"
		},
		"orca": func() (Host, string) {
			f := newOrcaFake(t)
			return New(Orca, f.deps()), f.handle(t)
		},
		"core": func() (Host, string) {
			h, _ := coreFixture(t)
			return h, "unused"
		},
	}
	for name, mk := range hosts {
		t.Run(name, func(t *testing.T) {
			h, handle := mk()
			err := h.Send(context.Background(), "w1", handle, missing)
			if !errors.Is(err, ErrNotSubmitted) || errors.Is(err, ErrMaybeSubmitted) {
				t.Fatalf("Send = %v, want ErrNotSubmitted only", err)
			}
		})
	}
}

// A failure with no stated class is classified as maybe-submitted, with its
// message intact; a stated class is left alone.
func TestClassifySend(t *testing.T) {
	boom := errors.New("boom")
	got := classifySend(boom)
	if !errors.Is(got, ErrMaybeSubmitted) || !errors.Is(got, boom) || got.Error() != "boom" {
		t.Fatalf("classifySend(boom) = %v", got)
	}
	for _, e := range []error{nil, ErrNotSubmitted, ErrQueued, ErrSubmitted, ErrDialogOpen, ErrDraftOnPrompt, got} {
		if out := classifySend(e); out != e {
			t.Errorf("classifySend(%v) rewrapped to %v", e, out)
		}
	}
}

// A backend that returns its send result without classifySend would let a plain
// error reach dispatch unclassified. Every Host.Send in the package, one per
// backend, must be a single `return classifySend(...)`.
func TestEverySendReturnsThroughClassifySend(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool { return !strings.HasSuffix(fi.Name(), "_test.go") }, 0)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, pkg := range pkgs {
		for _, f := range pkg.Files {
			for _, d := range f.Decls {
				fn, ok := d.(*ast.FuncDecl)
				if !ok || fn.Recv == nil || fn.Name.Name != "Send" || fn.Type.Params.NumFields() != 4 {
					continue
				}
				recv := types.ExprString(fn.Recv.List[0].Type)
				seen[recv] = true
				if !returnsClassifySend(fn) {
					t.Errorf("(%s).Send does not return through classifySend", recv)
				}
			}
		}
	}
	for _, want := range []string{"*herdr", "*tmux", "*orca", "*coreHost", "refused"} {
		if !seen[want] {
			t.Errorf("no Send found for %s: update this test with the new backend", want)
		}
	}
	if len(seen) != len(PaneKinds)+1 { // + refused, the Solo/unknown host
		t.Errorf("Send implementations %v do not match PaneKinds %v", seen, PaneKinds)
	}
}

func returnsClassifySend(fn *ast.FuncDecl) bool {
	if len(fn.Body.List) == 0 {
		return false
	}
	ret, ok := fn.Body.List[len(fn.Body.List)-1].(*ast.ReturnStmt)
	if !ok || len(ret.Results) != 1 {
		return false
	}
	call, ok := ret.Results[0].(*ast.CallExpr)
	if !ok {
		return false
	}
	id, isID := call.Fun.(*ast.Ident)
	return isID && id.Name == "classifySend"
}

// Solo and unknown kinds drive nothing: Send says nothing was sent.
func TestRefusedSendIsNotSubmitted(t *testing.T) {
	err := New(Solo, Deps{}).Send(context.Background(), "w1", "", "x")
	if !errors.Is(err, ErrNotSubmitted) || errors.Is(err, ErrMaybeSubmitted) || !errors.Is(err, ErrUnknownKind) {
		t.Fatalf("Send = %v", err)
	}
}
