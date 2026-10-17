package worker

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// declaredEventKinds parses the package source and returns the value of every
// string const named Event*: the kinds as declared, not as a hand-kept list.
func declaredEventKinds(t *testing.T) map[string]string {
	t.Helper()
	pkgs, err := parser.ParseDir(token.NewFileSet(), ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{} // const name -> kind
	for _, pkg := range pkgs {
		for _, f := range pkg.Files {
			for _, d := range f.Decls {
				gd, ok := d.(*ast.GenDecl)
				if !ok || gd.Tok != token.CONST {
					continue
				}
				for _, sp := range gd.Specs {
					vs := sp.(*ast.ValueSpec)
					for i, n := range vs.Names {
						if !strings.HasPrefix(n.Name, "Event") || i >= len(vs.Values) {
							continue
						}
						lit, ok := vs.Values[i].(*ast.BasicLit)
						if !ok || lit.Kind != token.STRING {
							continue
						}
						v, err := strconv.Unquote(lit.Value)
						if err != nil {
							t.Fatal(err)
						}
						kinds[n.Name] = v
					}
				}
			}
		}
	}
	if len(kinds) == 0 {
		t.Fatal("found no Event* consts: the source scan is broken")
	}
	return kinds
}

// Every declared kind has exactly one table row, and the row names only values
// its consumers understand: a kind added to the consts but not the table, or a
// row with a typo, fails here instead of silently doing nothing. The declared
// kinds come from the source, so a forgotten row cannot hide.
func TestEventTableCoversEveryKind(t *testing.T) {
	declared := declaredEventKinds(t)
	rows := map[string]bool{}
	for _, e := range Events {
		rows[e.Kind] = true
	}
	declaredKinds := map[string]bool{}
	for name, kind := range declared {
		declaredKinds[kind] = true
		if !rows[kind] {
			t.Errorf("%s (%q) is declared but has no row in Events", name, kind)
		}
	}
	for _, e := range Events {
		if !declaredKinds[e.Kind] {
			t.Errorf("row %q is not a declared Event* const", e.Kind)
		}
	}
	if len(EventKinds) != len(Events) {
		t.Errorf("EventKinds = %v, want one per row", EventKinds)
	}
	seen := map[string]bool{}
	for _, e := range Events {
		if seen[e.Kind] {
			t.Errorf("kind %q has two rows", e.Kind)
		}
		seen[e.Kind] = true
		if !slices.Contains([]string{"", "blocked", "done"}, e.State) {
			t.Errorf("%s: state %q is not a slot state an event records", e.Kind, e.State)
		}
		if !slices.Contains([]string{"", StateBlocked, StateDone}, e.Poll) {
			t.Errorf("%s: poll state %q", e.Kind, e.Poll)
		}
		if e.EveryRow && e.Ledger == "" {
			t.Errorf("%s: EveryRow without a ledger kind", e.Kind)
		}
		if e.Send && e.Help == "" {
			t.Errorf("%s: a send kind needs its flag help", e.Kind)
		}
		if e.SendBody && !e.Send {
			t.Errorf("%s: SendBody on a kind send does not take", e.Kind)
		}
	}
}

// Poll reads the table: a live event decides the row exactly when its kind
// names a poll state, and a heartbeat never does.
func TestDecideFollowsTheEventTable(t *testing.T) {
	base := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	idle := PollRow{"w1", StateIdle, "static pane"}
	for _, spec := range Events {
		t.Run(spec.Kind, func(t *testing.T) {
			ev := &Event{Seq: 1, Kind: spec.Kind, Body: "b", TS: stamp(base)}
			got, decided := decide(ev, idle, base)
			if decided != (spec.Poll != "") {
				t.Fatalf("decided = %v with poll state %q", decided, spec.Poll)
			}
			if decided && got.State != spec.Poll {
				t.Errorf("row state = %s, want %s", got.State, spec.Poll)
			}
		})
	}
}
