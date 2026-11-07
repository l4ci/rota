package testaudit

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type InventoryReport struct {
	Complete       bool                  `json:"complete"`
	Playwright     []PlaywrightInventory `json:"playwright"`
	Modules        []ModuleInventory     `json:"modules"`
	Helpers        []HelperInventory     `json:"helpers"`
	UnwiredScripts []ScriptInventory     `json:"unwired_scripts"`
	DocCounts      []DocCount            `json:"doc_counts"`
	Diagnostics    []string              `json:"diagnostics"`
}
type PlaywrightInventory struct {
	Config    string          `json:"config"`
	Scenarios int             `json:"scenarios"`
	Runs      int             `json:"runs"`
	Projects  map[string]int  `json:"projects"`
	Specs     []SpecInventory `json:"specs"`
	Error     string          `json:"error,omitempty"`
}
type SpecInventory struct {
	Path      string `json:"path"`
	Scenarios int    `json:"scenarios"`
	Runs      int    `json:"runs"`
}
type ModuleInventory struct {
	Module string   `json:"module"`
	Tests  []string `json:"tests"`
}
type HelperInventory struct {
	Name  string   `json:"name"`
	Paths []string `json:"paths"`
}
type ScriptInventory struct {
	Path    string `json:"path"`
	Name    string `json:"name"`
	Command string `json:"command"`
}
type DocCount struct {
	Source  string `json:"source,omitempty"`
	Path    string `json:"path"`
	Config  string `json:"config,omitempty"`
	Unit    string `json:"unit"`
	Claimed int    `json:"claimed"`
	Actual  *int   `json:"actual,omitempty"`
	Status  string `json:"status"`
	Detail  string `json:"detail,omitempty"`
}

// Inventory reads only the caller's tracked/unignored file inventory. Static
// findings are candidates: arbitrary wrappers, generated imports and tsconfig
// aliases other than @/ cannot be resolved without executing project code.
func Inventory(root string, files []string, commands []string, list func(string) ([]byte, error)) InventoryReport {
	r := InventoryReport{Complete: true}
	sort.Strings(files)
	bodies := map[string]string{}
	known := map[string]bool{}
	for _, f := range files {
		f = filepath.ToSlash(filepath.Clean(f))
		if filepath.IsAbs(f) || f == ".." || strings.HasPrefix(f, "../") {
			r.Complete = false
			r.Diagnostics = append(r.Diagnostics, "unsafe inventory path: "+f)
			continue
		}
		known[f] = true
		if inventorySource(f) || strings.HasSuffix(f, "package.json") || strings.EqualFold(filepath.Base(f), "README.md") {
			b, e := os.ReadFile(filepath.Join(root, f))
			if e != nil {
				r.Complete = false
				r.Diagnostics = append(r.Diagnostics, f+": "+e.Error())
			} else {
				bodies[f] = string(b)
			}
		}
		if configName.MatchString(filepath.Base(f)) {
			p := PlaywrightInventory{Config: f, Projects: map[string]int{}}
			if list == nil {
				p.Error = "list runner unavailable"
			} else {
				b, e := list(f)
				if e != nil {
					p.Error = e.Error()
				} else {
					p = parseListing(root, f, b)
				}
			}
			if p.Error != "" {
				r.Complete = false
				r.Diagnostics = append(r.Diagnostics, f+": "+p.Error)
			}
			r.Playwright = append(r.Playwright, p)
		}
	}
	modules := map[string]map[string]bool{}
	helpers := map[string]map[string]bool{}
	for _, f := range files {
		if !inventoryTest(f) {
			continue
		}
		body := bodies[f]
		// A unique conventional basename is a candidate even without a direct
		// import (for example a browser integration test). Ambiguity is retained.
		stem := strings.Split(path.Base(f), ".")[0]
		var named []string
		for candidate := range known {
			if strings.Contains("/"+candidate, "/src/") && inventorySource(candidate) && !inventoryTest(candidate) && strings.TrimSuffix(path.Base(candidate), path.Ext(candidate)) == stem {
				named = append(named, candidate)
			}
		}
		if len(named) == 1 {
			if modules[named[0]] == nil {
				modules[named[0]] = map[string]bool{}
			}
			modules[named[0]][f] = true
		} else if len(named) > 1 {
			r.Diagnostics = append(r.Diagnostics, f+": ambiguous source basename "+stem)
		}
		for _, m := range importName.FindAllStringSubmatch(body, -1) {
			target := m[1]
			var base string
			if strings.HasPrefix(target, ".") {
				base = path.Clean(path.Join(path.Dir(f), target))
			} else if strings.HasPrefix(target, "@/") {
				base = "src/" + strings.TrimPrefix(target, "@/")
			} else {
				continue
			}
			var matches []string
			for _, candidate := range resolveCandidates(base) {
				if known[candidate] && strings.Contains("/"+candidate, "/src/") {
					matches = append(matches, candidate)
				}
			}
			if len(matches) == 1 {
				if modules[matches[0]] == nil {
					modules[matches[0]] = map[string]bool{}
				}
				modules[matches[0]][f] = true
			} else if len(matches) > 1 {
				r.Diagnostics = append(r.Diagnostics, f+": ambiguous import "+target)
			}
		}
		if helperLocation(f) {
			for _, m := range helperName.FindAllStringSubmatch(body, -1) {
				name := m[1]
				if name == "" {
					name = m[2]
				}
				if helpers[name] == nil {
					helpers[name] = map[string]bool{}
				}
				helpers[name][f] = true
			}
		}
	}
	for _, module := range sortedKeys(modules) {
		r.Modules = append(r.Modules, ModuleInventory{Module: module, Tests: sortedKeys(modules[module])})
	}
	for _, name := range sortedKeys(helpers) {
		if len(helpers[name]) > 1 {
			r.Helpers = append(r.Helpers, HelperInventory{Name: name, Paths: sortedKeys(helpers[name])})
		}
	}
	r.UnwiredScripts = unwiredScripts(bodies, commands)
	r.DocCounts = docCounts(files, bodies, r.Playwright)
	r.Diagnostics = append(r.Diagnostics, "Static module, helper and script findings are advisory; dynamic imports, custom aliases and opaque shell wrappers may be unresolved.")
	return r
}

var configName = regexp.MustCompile(`^playwright(?:[.-][A-Za-z0-9_-]+)*\.config\.(?:ts|js|mts|mjs|cts|cjs)$`)
var importName = regexp.MustCompile(`(?:from\s*|import\s*\(?\s*|require\s*\(\s*)["']([^"']+)["']`)
var helperName = regexp.MustCompile(`(?:function\s+([A-Za-z_$][\w$]*)\s*\(|(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*=\s*(?:async\s*)?(?:\([^\n;]*\)|[A-Za-z_$][\w$]*)\s*=>)`)

func inventorySource(f string) bool {
	switch path.Ext(f) {
	case ".ts", ".tsx", ".js", ".jsx", ".mts", ".mjs", ".cts", ".cjs":
		return true
	}
	return false
}
func inventoryTest(f string) bool {
	return inventorySource(f) && (strings.Contains("/"+f, "/tests/") || strings.Contains("/"+f, "/test/") || strings.Contains("/"+f, "/__tests__/") || strings.Contains(f, ".test.") || strings.Contains(f, ".spec."))
}
func helperLocation(f string) bool {
	return strings.HasPrefix(f, "tests/") || (strings.HasPrefix(f, "packages/") && (strings.Contains(f, "/test/") || strings.Contains(f, "/tests/")))
}
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
func resolveCandidates(base string) []string {
	result := []string{base}
	ext := path.Ext(base)
	if ext == ".js" || ext == ".mjs" || ext == ".cjs" {
		base = strings.TrimSuffix(base, ext)
	}
	for _, e := range []string{".ts", ".tsx", ".js", ".jsx", ".mts", ".mjs", ".cts", ".cjs"} {
		result = append(result, base+e, path.Join(base, "index"+e))
	}
	return uniqueStrings(result)
}
func uniqueStrings(in []string) []string {
	m := map[string]bool{}
	for _, s := range in {
		m[s] = true
	}
	return sortedKeys(m)
}

type listSpec struct {
	Title  string `json:"title"`
	File   string `json:"file"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
	Tests  []struct {
		ProjectName string `json:"projectName"`
	} `json:"tests"`
}
type listSuite struct {
	Title  string      `json:"title"`
	File   string      `json:"file"`
	Specs  []listSpec  `json:"specs"`
	Suites []listSuite `json:"suites"`
}

func parseListing(root, config string, b []byte) PlaywrightInventory {
	p := PlaywrightInventory{Config: config, Projects: map[string]int{}}
	var data struct {
		Suites []listSuite       `json:"suites"`
		Errors []json.RawMessage `json:"errors"`
	}
	if e := json.Unmarshal(b, &data); e != nil {
		p.Error = "invalid list JSON: " + e.Error()
		return p
	}
	if data.Suites == nil {
		p.Error = "list JSON has no suites"
		return p
	}
	if len(data.Errors) > 0 {
		p.Error = "Playwright listing reported errors"
		return p
	}
	scenarios := map[string]bool{}
	specs := map[string]*SpecInventory{}
	var visit func(listSuite, string, string)
	visit = func(s listSuite, file, ancestry string) {
		if s.File != "" {
			file = s.File
		}
		ancestry += "/" + s.Title
		for _, sp := range s.Specs {
			f := file
			if sp.File != "" {
				f = sp.File
			}
			if filepath.IsAbs(f) {
				if rel, e := filepath.Rel(root, f); e == nil {
					f = filepath.ToSlash(rel)
				}
			}
			if f == "" {
				p.Error = "list spec has no file"
				continue
			}
			if specs[f] == nil {
				specs[f] = &SpecInventory{Path: f}
			}
			key := fmt.Sprintf("%s:%d:%d:%s:%s", f, sp.Line, sp.Column, ancestry, sp.Title)
			if !scenarios[key] {
				scenarios[key] = true
				p.Scenarios++
				specs[f].Scenarios++
			}
			for _, test := range sp.Tests {
				p.Runs++
				specs[f].Runs++
				name := test.ProjectName
				if name == "" {
					name = "(default)"
				}
				p.Projects[name]++
			}
		}
		for _, child := range s.Suites {
			visit(child, file, ancestry)
		}
	}
	for _, s := range data.Suites {
		visit(s, "", "")
	}
	for _, f := range sortedKeys(specs) {
		p.Specs = append(p.Specs, *specs[f])
	}
	return p
}

// Script reachability follows explicit npm/pnpm/yarn/bun calls, including
// package-local script chains. Wildcard workspace selection stays advisory.
func unwiredScripts(bodies map[string]string, commands []string) []ScriptInventory {
	packages := map[string]map[string]string{}
	names := map[string]string{}
	for f, b := range bodies {
		if path.Base(f) != "package.json" {
			continue
		}
		var pkg struct {
			Name    string            `json:"name"`
			Scripts map[string]string `json:"scripts"`
		}
		if json.Unmarshal([]byte(b), &pkg) == nil {
			dir := path.Dir(f)
			packages[dir] = pkg.Scripts
			if pkg.Name != "" {
				names[pkg.Name] = dir
			}
		}
	}
	reached := map[string]bool{}
	var follow func(string, string)
	follow = func(command, dir string) {
		for _, chunk := range shellSeparator.Split(command, -1) {
			tokens := shellToken.FindAllString(chunk, -1)
			for i := range tokens {
				tokens[i] = strings.Trim(tokens[i], "\"'")
			}
			if len(tokens) == 0 {
				continue
			}
			if tokens[0] == "cd" && len(tokens) > 1 {
				dir = path.Clean(path.Join(dir, tokens[1]))
				continue
			}
			for i, token := range tokens {
				if token != "npm" && token != "pnpm" && token != "yarn" && token != "bun" {
					continue
				}
				target := dir
				j := i + 1
				for j < len(tokens) {
					if (tokens[j] == "--prefix" || tokens[j] == "--cwd" || tokens[j] == "-C" || tokens[j] == "--dir") && j+1 < len(tokens) {
						target = path.Clean(path.Join(dir, tokens[j+1]))
						j += 2
						continue
					}
					if (tokens[j] == "--workspace" || tokens[j] == "-w" || tokens[j] == "--filter") && j+1 < len(tokens) {
						selected := tokens[j+1]
						target = names[selected]
						if target == "" {
							target = path.Clean(selected)
						}
						j += 2
						continue
					}
					break
				}
				if j < len(tokens) && (tokens[j] == "run" || tokens[j] == "run-script") {
					j++
				}
				if j >= len(tokens) {
					continue
				}
				name := tokens[j]
				if body, ok := packages[target][name]; ok {
					key := target + ":" + name
					if !reached[key] {
						reached[key] = true
						follow(body, target)
					}
				}
			}
		}
	}
	for _, command := range commands {
		follow(command, ".")
	}
	var result []ScriptInventory
	for _, dir := range sortedKeys(packages) {
		for _, name := range sortedKeys(packages[dir]) {
			if (name == "test" || strings.HasPrefix(name, "test:") || strings.HasPrefix(name, "test-")) && !reached[dir+":"+name] {
				result = append(result, ScriptInventory{Path: path.Join(dir, "package.json"), Name: name, Command: packages[dir][name]})
			}
		}
	}
	return result
}

var shellToken = regexp.MustCompile(`"[^"]*"|'[^']*'|[^\s]+`)
var shellSeparator = regexp.MustCompile(`\s*(?:&&|\|\||;)\s*`)
var docNumber = regexp.MustCompile(`(?i)\b([0-9][0-9,]*)\s+(scenarios?|refs?|references?)\b`)

func docCounts(files []string, bodies map[string]string, configs []PlaywrightInventory) []DocCount {
	var out []DocCount
	for _, f := range files {
		if !strings.EqualFold(path.Base(f), "README.md") || (!strings.Contains(strings.ToLower(path.Dir(f)), "test") && !strings.Contains(strings.ToLower(path.Dir(f)), "e2e")) {
			continue
		}
		for _, line := range strings.Split(bodies[f], "\n") {
			for _, match := range docNumber.FindAllStringSubmatch(line, -1) {
				n, _ := strconv.Atoi(strings.ReplaceAll(match[1], ",", ""))
				unit := "scenarios"
				if !strings.HasPrefix(strings.ToLower(match[2]), "scenario") {
					unit = "refs"
				}
				d := DocCount{Path: f, Unit: unit, Claimed: n, Status: "unavailable"}
				var candidates []PlaywrightInventory
				for _, c := range configs {
					if strings.Contains(line, c.Config) {
						candidates = append(candidates, c)
					}
				}
				if len(candidates) == 0 {
					for _, c := range configs {
						if strings.Contains(line, path.Base(c.Config)) {
							candidates = append(candidates, c)
						}
					}
				}
				if len(candidates) == 0 {
					best := -1
					for _, c := range configs {
						dir := path.Dir(c.Config)
						if dir == "." || path.Dir(f) == dir || strings.HasPrefix(f, dir+"/") {
							depth := len(dir)
							if depth > best {
								candidates = nil
								best = depth
							}
							if depth == best {
								candidates = append(candidates, c)
							}
						}
					}
				}
				if len(candidates) != 1 {
					d.Detail = "ambiguous or unavailable Playwright config scope"
				} else {
					c := candidates[0]
					d.Config = c.Config
					if c.Error != "" {
						d.Detail = "scoped listing unavailable"
					} else if unit == "refs" {
						actual, available := storedReferences(files, configs, c)
						if !available {
							d.Detail = "reference layout unavailable or spec shared by multiple configs"
						} else {
							d.Source = "tracked spec snapshot images"
							d.Detail = "advisory: default <spec>-snapshots layout only; custom snapshot templates require manual comparison"
							d.Actual = &actual
							d.Status = "match"
							if n != actual {
								d.Status = "drift"
							}
						}
					} else {
						d.Source = "Playwright unique listed scenarios"
						actual := c.Scenarios
						d.Actual = &actual
						d.Status = "match"
						if n != actual {
							d.Status = "drift"
						}
					}
				}
				out = append(out, d)
			}
		}
	}
	return out
}

// storedReferences never reads image contents. A recognized default-layout
// directory is evidence for a comparison; custom templates remain unavailable.
func storedReferences(files []string, configs []PlaywrightInventory, selected PlaywrightInventory) (int, bool) {
	prefixes := map[string]bool{}
	for _, spec := range selected.Specs {
		for _, other := range configs {
			if other.Config == selected.Config {
				continue
			}
			for _, peer := range other.Specs {
				if peer.Path == spec.Path {
					return 0, false
				}
			}
		}
		prefixes[spec.Path+"-snapshots/"] = true
	}
	count := 0
	for _, f := range files {
		ext := strings.ToLower(path.Ext(f))
		if ext != ".png" && ext != ".jpg" && ext != ".jpeg" && ext != ".webp" {
			continue
		}
		for prefix := range prefixes {
			if strings.HasPrefix(f, prefix) {
				count++
				break
			}
		}
	}
	return count, count > 0
}
