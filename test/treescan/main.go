// treescan keeps tests that inspect other repository files in the fast tier.
// It uses only the standard library so fixture modules can run it unchanged.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// registry holds only what discovery cannot know: why a discovered test is
// exempt from the fast tier. The tier itself is derived by scans.
type registry struct {
	Fixtures map[string]string `json:"fixtures"`
}

type inventory struct {
	Tests      map[string]bool
	Candidates map[string]bool
}

type function struct {
	scans    bool
	reads    bool
	executes bool
	rootHint bool
	calls    []string
	test     string
}

func main() {
	check := flag.Bool("check", false, "validate registry without running tests")
	list := flag.Bool("list", false, "validate registry and print the planned test command")
	flag.Parse()
	if flag.NArg() != 0 {
		fatal(fmt.Errorf("unexpected arguments: %v", flag.Args()))
	}
	root, err := os.Getwd()
	if err != nil {
		fatal(err)
	}
	r, err := readRegistry(filepath.Join(root, "test", "treescan", "registry.json"))
	if err != nil {
		fatal(err)
	}
	inv, err := discover(root)
	if err != nil {
		fatal(err)
	}
	if err := validate(r, inv); err != nil {
		fatal(err)
	}
	args := command(scans(r, inv))
	if *list && len(args) > 0 {
		fmt.Printf("go test -count=1 -run %s %s\n", strconv.Quote(args[3]), strings.Join(args[4:], " "))
		return
	}
	if *check || len(args) == 0 {
		return
	}
	cmd := exec.Command("go", args...)
	cmd.Dir, cmd.Stdout, cmd.Stderr = root, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		fatal(err)
	}
}

func fatal(err error) { fmt.Fprintln(os.Stderr, "treescan:", err); os.Exit(1) }

func readRegistry(name string) (registry, error) {
	var r registry
	raw, err := os.ReadFile(name)
	if err != nil {
		return r, err
	}
	// encoding/json normally accepts duplicate object keys, hiding stale entries.
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := uniqueJSON(dec); err != nil {
		return r, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return r, fmt.Errorf("registry must contain one JSON value")
	}
	dec = json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return r, err
	}
	if r.Fixtures == nil {
		return r, fmt.Errorf("registry requires a fixtures object")
	}
	return r, nil
}

func uniqueJSON(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	seen := map[string]bool{}
	for dec.More() {
		if delim == '{' {
			key, err := dec.Token()
			if err != nil {
				return err
			}
			s, ok := key.(string)
			if !ok {
				return fmt.Errorf("invalid object key")
			}
			if seen[s] {
				return fmt.Errorf("duplicate registry key %q", s)
			}
			seen[s] = true
		}
		if err := uniqueJSON(dec); err != nil {
			return err
		}
	}
	_, err = dec.Token()
	return err
}

func testName(name string) bool {
	if name == "TestMain" || !strings.HasPrefix(name, "Test") {
		return false
	}
	for _, r := range strings.TrimPrefix(name, "Test") {
		return !unicode.IsLower(r)
	}
	return true
}

// discover parses every Go file, including build-tagged files. Test helpers
// are joined across files in the same Go package; recursive calls terminate.
func discover(root string) (inventory, error) {
	inv := inventory{Tests: map[string]bool{}, Candidates: map[string]bool{}}
	groups := map[string]map[string][]function{}
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if name != root {
				switch entry.Name() {
				case ".git", ".worktrees", "vendor", "node_modules", "testdata":
					return filepath.SkipDir
				}
				if _, err := os.Stat(filepath.Join(name, ".git")); err == nil {
					return filepath.SkipDir
				} else if !os.IsNotExist(err) {
					return err
				}
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, filepath.Dir(name))
		if err != nil {
			return err
		}
		pkg := "./" + filepath.ToSlash(rel)
		if rel == "." {
			pkg = "."
		}
		group := pkg + ":" + file.Name.Name
		if groups[group] == nil {
			groups[group] = map[string][]function{}
		}
		imports := map[string]string{}
		for _, imp := range file.Imports {
			p, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				return err
			}
			alias := path.Base(p)
			if imp.Name != nil {
				alias = imp.Name.Name
			}
			imports[alias] = p
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			f := function{}
			if !strings.HasSuffix(name, "_test.go") {
				// Production helpers contribute direct reads only. Following
				// application call graphs would classify fixture tests as guards.
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					if sel, ok := n.(*ast.SelectorExpr); ok {
						if id, ok := sel.X.(*ast.Ident); ok && diskReader(imports[id.Name], sel.Sel.Name) {
							f.reads = true
						}
					}
					return true
				})
				groups[group][fn.Name.Name] = append(groups[group][fn.Name.Name], f)
				continue
			}
			sourceHint := false
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if lit, ok := n.(*ast.BasicLit); ok && sourcePath([]ast.Expr{lit}) {
					sourceHint = true
				}
				return true
			})
			if fn.Recv == nil && testName(fn.Name.Name) {
				f.test = pkg + "/" + fn.Name.Name
				inv.Tests[f.test] = true
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				// References count too: a helper passed to t.Run, or a
				// directory function assigned to a local variable, can scan.
				if id, ok := n.(*ast.Ident); ok {
					f.calls = append(f.calls, id.Name)
				}
				if sel, ok := n.(*ast.SelectorExpr); ok {
					if id, ok := sel.X.(*ast.Ident); ok {
						if scanCall(imports[id.Name], sel.Sel.Name, nil) {
							f.scans = true
						}
						if imports[id.Name] == "os/exec" && (sel.Sel.Name == "Command" || sel.Sel.Name == "CommandContext") {
							f.executes = true
						}
						if diskReader(imports[id.Name], sel.Sel.Name) {
							f.reads = true
						}
					}
				}
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				switch callee := call.Fun.(type) {
				case *ast.Ident:
					f.calls = append(f.calls, callee.Name)
					if p := imports["."]; p != "" {
						if scanCall(p, callee.Name, call.Args) {
							f.scans = true
						}
						if diskReader(p, callee.Name) {
							f.reads = true
						}
					}
				case *ast.SelectorExpr:
					if id, ok := callee.X.(*ast.Ident); ok {
						if p, imported := imports[id.Name]; imported {
							if p == "path/filepath" && (callee.Sel.Name == "Abs" || callee.Sel.Name == "Join") {
								for _, arg := range call.Args {
									if lit, ok := arg.(*ast.BasicLit); ok && lit.Kind == token.STRING {
										value, _ := strconv.Unquote(lit.Value)
										if value == ".." || value == "../.." {
											f.rootHint = true
										}
									}
								}
							}
							if scanCall(p, callee.Sel.Name, call.Args) {
								f.scans = true
							}
							break
						}
					}
					// Local methods can contain the scan too. Resolve conservatively by name.
					f.calls = append(f.calls, callee.Sel.Name)
				}
				return true
			})
			// A path assembled before the read still depends on source/docs.
			// Keep these hints local; helper reachability is handled below.
			f.scans = f.scans || (f.reads || f.executes) && sourceHint
			groups[group][fn.Name.Name] = append(groups[group][fn.Name.Name], f)
		}
		return nil
	})
	if err != nil {
		return inv, err
	}
	for _, funcs := range groups {
		var reachesScan func(string, map[string]bool) function
		reachesScan = func(name string, seen map[string]bool) function {
			if seen[name] {
				return function{}
			}
			seen[name] = true
			var result function
			for _, f := range funcs[name] {
				result.scans = result.scans || f.scans
				result.reads = result.reads || f.reads
				result.rootHint = result.rootHint || f.rootHint
				for _, next := range f.calls {
					reached := reachesScan(next, seen)
					result.scans = result.scans || reached.scans
					result.reads = result.reads || reached.reads
					result.rootHint = result.rootHint || reached.rootHint
				}
			}
			return result
		}
		for name, variants := range funcs {
			for _, f := range variants {
				if f.test == "" {
					continue
				}
				reached := reachesScan(name, map[string]bool{})
				if reached.scans || reached.reads && reached.rootHint {
					inv.Candidates[f.test] = true
				}
			}
		}
	}
	return inv, nil
}

func diskReader(pkg, name string) bool {
	return pkg == "os" && (name == "ReadFile" || name == "Open") || pkg == "io/ioutil" && name == "ReadFile"
}

func scanCall(pkg, name string, args []ast.Expr) bool {
	switch pkg {
	case "path/filepath":
		return name == "Walk" || name == "WalkDir" || name == "Glob"
	case "os":
		return name == "ReadDir" || (name == "ReadFile" || name == "Open") && sourcePath(args)
	case "io/ioutil":
		return name == "ReadDir" || name == "ReadFile" && sourcePath(args)
	case "io/fs":
		return name == "ReadDir" || name == "WalkDir" || name == "Glob"
	case "os/exec":
		return execScan(name, args)
	case "go/parser":
		if name == "ParseDir" {
			return true
		}
		if name == "ParseFile" && len(args) > 2 {
			id, ok := args[2].(*ast.Ident)
			return ok && id.Name == "nil"
		}
	}
	return false
}

// execScan recognizes external tree enumerators, including aliased imports and
// CommandContext. Other commands qualify through source-path hints in their
// containing helper, for example a repository hook resolved with filepath.Abs.
func execScan(name string, args []ast.Expr) bool {
	if name == "CommandContext" {
		if len(args) == 0 {
			return false
		}
		args = args[1:]
	} else if name != "Command" {
		return false
	}
	if len(args) == 0 {
		return false
	}
	literal := func(expr ast.Expr) string {
		if lit, ok := expr.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			value, _ := strconv.Unquote(lit.Value)
			return value
		}
		return ""
	}
	tool := path.Base(literal(args[0]))
	if tool == "grep" || tool == "egrep" || tool == "fgrep" || tool == "rg" {
		return true
	}
	if tool == "git" {
		for _, arg := range args[1:] {
			if value := literal(arg); value == "grep" || value == "ls-files" {
				return true
			}
		}
	}
	return false
}

// Fixed source/doc reads also depend on files outside the Go build graph.
// Hints and disk reads propagate through local helpers, including repo-root
// helpers; fixture-only callers must explain their exemption in the registry.
func sourcePath(args []ast.Expr) bool {
	if len(args) == 0 {
		return false
	}
	found := false
	ast.Inspect(args[0], func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		value, err := strconv.Unquote(lit.Value)
		if err == nil && (value == ".." || strings.Contains(value, "../") || strings.Contains(value, ".go") || strings.Contains(value, ".md") || value == "VERSION" || strings.HasPrefix(value, "hooks/") || strings.HasPrefix(value, ".github/")) {
			found = true
		}
		return true
	})
	return found
}

func validate(r registry, inv inventory) error {
	var problems []string
	for key, reason := range r.Fixtures {
		if strings.TrimSpace(reason) == "" {
			problems = append(problems, "fixture needs a reason: "+key)
		}
		if !inv.Tests[key] {
			problems = append(problems, "stale fixture "+key)
		}
	}
	sort.Strings(problems)
	if len(problems) > 0 {
		return fmt.Errorf("registry validation failed:\n%s", strings.Join(problems, "\n"))
	}
	return nil
}

func command(tier map[string][]string) []string {
	if len(tier) == 0 {
		return nil
	}
	packages, names := []string{}, []string{}
	seen := map[string]bool{}
	for pkg, tests := range tier {
		packages = append(packages, pkg)
		for _, name := range tests {
			if !seen[name] {
				names = append(names, regexp.QuoteMeta(name))
				seen[name] = true
			}
		}
	}
	sort.Strings(packages)
	sort.Strings(names)
	return append([]string{"test", "-count=1", "-run", "^(" + strings.Join(names, "|") + ")$"}, packages...)
}

// scans is the fast tier: every discovered tree scan not exempted as a fixture,
// grouped by package. Adding a scan test needs no registry edit.
func scans(r registry, inv inventory) map[string][]string {
	tier := map[string][]string{}
	for key := range inv.Candidates {
		if _, exempt := r.Fixtures[key]; exempt {
			continue
		}
		slash := strings.LastIndex(key, "/")
		tier[key[:slash]] = append(tier[key[:slash]], key[slash+1:])
	}
	for _, names := range tier {
		sort.Strings(names)
	}
	return tier
}
