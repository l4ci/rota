package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeSource(t *testing.T, root, name, source string) {
	t.Helper()
	file := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestRegistry(t *testing.T) {
	root := filepath.Join("..", "..")
	r, err := readRegistry(filepath.Join(root, "test", "treescan", "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	inv, err := discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := validate(r, inv); err != nil {
		t.Fatal(err)
	}
}

func TestMissingRegistrationThroughHelpersAndAliases(t *testing.T) {
	root := t.TempDir()
	writeSource(t, root, "scan_test.go", `package scan
import "testing"
func TestGuard(t *testing.T) { first() }
func first() { second() }
func cycle() { first() }
`)
	writeSource(t, root, "helper_test.go", `package scan
import (p "path/filepath"; f "io/fs")
func second() { cycle(); p.WalkDir(".", nil); f.ReadDir(nil, ".") }
`)
	inv, err := discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if !inv.Candidates["./TestGuard"] {
		t.Fatal("helper chain with import aliases was missed")
	}
	if err := validate(registry{}, inv); err == nil || !strings.Contains(err.Error(), "unregistered tree scan ./TestGuard") {
		t.Fatalf("missing registration: %v", err)
	}
	r := registry{Fixtures: map[string]string{"./TestGuard": "Only enumerates a temporary fixture."}}
	if err := validate(r, inv); err != nil {
		t.Fatal(err)
	}
}

func TestParserReadsAndPackageIsolation(t *testing.T) {
	root := t.TempDir()
	writeSource(t, root, "scan_test.go", `package scan
import ("go/parser"; "testing")
func TestFile(t *testing.T) { parser.ParseFile(nil, "x.go", nil, 0) }
func TestDir(t *testing.T) { parser.ParseDir(nil, ".", nil, 0) }
func TestBytes(t *testing.T) { parser.ParseFile(nil, "x.go", "package x", 0) }
func TestLocal(t *testing.T) { helper() }
func helper() {}
`)
	writeSource(t, root, "external_test.go", `package scan_test
import ("os"; "testing")
func TestExternal(t *testing.T) { helper() }
func helper() { os.ReadDir(".") }
`)
	inv, err := discover(root)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"./TestFile": true, "./TestDir": true, "./TestExternal": true}
	if !reflect.DeepEqual(inv.Candidates, want) {
		t.Fatalf("candidates = %v, want %v", inv.Candidates, want)
	}
}

func TestIgnoredNestedCheckoutsAndFixtureTrees(t *testing.T) {
	root := t.TempDir()
	source := `package scan; import ("os"; "testing"); func TestGuard(t *testing.T) { os.ReadDir(".") }`
	writeSource(t, root, "own/scan_test.go", source)
	for _, dir := range []string{".git", ".worktrees/worker", "vendor", "node_modules", "testdata", "nested"} {
		writeSource(t, root, dir+"/scan_test.go", source)
	}
	writeSource(t, root, "nested/.git", "gitdir: somewhere")
	writeSource(t, root, "checkout/.git/config", "")
	writeSource(t, root, "checkout/scan_test.go", "this is deliberately not valid Go")
	inv, err := discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Tests) != 1 || !inv.Candidates["./own/TestGuard"] {
		t.Fatalf("nested checkout or ignored directory leaked: %+v", inv)
	}
}

func TestStaleDuplicateAndOverlappingRegistrations(t *testing.T) {
	inv := inventory{Tests: map[string]bool{"./p/TestGuard": true}}
	for name, r := range map[string]registry{
		"stale scan":    {Scans: map[string][]string{"./p": {"TestMissing"}}},
		"stale fixture": {Fixtures: map[string]string{"./p/TestMissing": "Temporary fixture."}},
		"duplicate":     {Scans: map[string][]string{"./p": {"TestGuard", "TestGuard"}}},
		"overlap":       {Scans: map[string][]string{"./p": {"TestGuard"}}, Fixtures: map[string]string{"./p/TestGuard": "Fixture."}},
		"empty reason":  {Fixtures: map[string]string{"./p/TestGuard": " "}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validate(r, inv); err == nil {
				t.Fatal("invalid registry accepted")
			}
		})
	}
}

func TestRegistryRejectsDuplicateJSONKeys(t *testing.T) {
	file := filepath.Join(t.TempDir(), "registry.json")
	if err := os.WriteFile(file, []byte(`{"scans":{"./p":["TestA"],"./p":["TestB"]},"fixtures":{}}`), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := readRegistry(file); err == nil || !strings.Contains(err.Error(), "duplicate registry key") {
		t.Fatalf("duplicate key accepted: %v", err)
	}
}

func TestCommandForcesFreshRunsAndExactNames(t *testing.T) {
	got := command(registry{Scans: map[string][]string{"./z": {"TestZ"}, "./a": {"TestA"}}})
	want := []string{"test", "-count=1", "-run", "^(TestA|TestZ)$", "./a", "./z"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("command = %q, want %q", got, want)
	}
}

func TestDirectSourceReadsThroughHelpers(t *testing.T) {
	root := t.TempDir()
	writeSource(t, root, "source_test.go", `package scan
import (o "os"; "path/filepath"; old "io/ioutil"; "testing")
func TestDoc(t *testing.T) { readDoc() }
func readDoc() { o.ReadFile(filepath.Join("..", "docs", "contract.md")) }
func TestSource(t *testing.T) { o.Open("guard.go") }
func TestOldRead(t *testing.T) { old.ReadFile("../../other/source.go") }
func TestOrdinaryRead(t *testing.T) { o.ReadFile("fixture.txt") }
`)
	inv, err := discover(root)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"./TestDoc": true, "./TestSource": true, "./TestOldRead": true}
	if !reflect.DeepEqual(inv.Candidates, want) {
		t.Fatalf("fixed source reads = %v, want %v", inv.Candidates, want)
	}
}

func TestFunctionReferencesCannotHideDirectoryScans(t *testing.T) {
	root := t.TempDir()
	writeSource(t, root, "scan_test.go", `package scan
import (p "path/filepath"; "os"; "testing")
func TestAssignedWalk(t *testing.T) { walk := p.WalkDir; walk(".", nil) }
func TestCallback(t *testing.T) { t.Run("scan", helper) }
func helper(t *testing.T) { os.ReadDir(".") }
`)
	inv, err := discover(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"TestAssignedWalk", "TestCallback"} {
		if !inv.Candidates["./"+name] {
			t.Errorf("function reference escaped discovery: %s", name)
		}
	}
}

func TestVariableSourcePathsAcrossHelpers(t *testing.T) {
	root := t.TempDir()
	writeSource(t, root, "scan_test.go", `package scan
import ("os"; "path/filepath"; "testing")
func TestVariable(t *testing.T) { src := filepath.Join("..", "other", "x.go"); os.ReadFile(src) }
func TestHelperVariable(t *testing.T) { helper() }
func helper() { src := filepath.Join("..", "other", "x.go"); os.ReadFile(src) }
func TestOrdinaryVariable(t *testing.T) { src := "fixture.txt"; os.ReadFile(src) }
`)
	inv, err := discover(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"TestVariable", "TestHelperVariable"} {
		if !inv.Candidates["./"+name] {
			t.Errorf("variable source path escaped discovery: %s", name)
		}
	}
	if inv.Candidates["./TestOrdinaryVariable"] {
		t.Error("ordinary fixture path classified as source read")
	}
}
