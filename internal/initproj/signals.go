package initproj

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/l4ci/rota/internal/jsonx"
)

// Signal is a value read from a project directory, with where it was read.
type Signal struct {
	Value    string
	Evidence string
}

// TestCommands is [test.fast, test.full] read from the project's manifests in
// dir: the first manifest that names a test command is the fast one; the full
// one is a command a CI file runs, else a Makefile test target, else the fast
// one. Nothing is run; the evidence is a file.
func TestCommands(dir string) ([2]Signal, bool) {
	var fast Signal
	for _, m := range manifestTests {
		if cmd, ev, ok := m(dir); ok {
			fast = Signal{jsonList(cmd), ev}
			break
		}
	}
	if fast.Value == "" {
		return [2]Signal{}, false
	}
	full := fast
	if cmd, ev, ok := ciTest(dir); ok {
		full = Signal{jsonList(cmd), ev}
	} else if ok := fileHasLine(filepath.Join(dir, "Makefile"), regexp.MustCompile(`^test:`)); ok && fast.Value != jsonList("make test") {
		full = Signal{jsonList("make test"), "Makefile has a test target"}
	}
	return [2]Signal{fast, full}, true
}

func jsonList(cmd string) string {
	b, _ := jsonx.MarshalCompact([]any{cmd})
	return string(b)
}

// manifestTests read one manifest each, in precedence order.
var manifestTests = []func(dir string) (cmd, evidence string, ok bool){
	func(dir string) (string, string, bool) {
		return "go test ./...", "go.mod", hasFile(dir, "go.mod")
	},
	func(dir string) (string, string, bool) {
		raw, err := os.ReadFile(filepath.Join(dir, "package.json"))
		if err != nil {
			return "", "", false
		}
		v, err := jsonx.Decode(raw)
		obj, _ := v.(*jsonx.Object)
		if err != nil || obj == nil {
			return "", "", false
		}
		scripts, _ := obj.Get("scripts")
		if so, ok := scripts.(*jsonx.Object); ok {
			if _, has := so.Get("test"); has {
				return "npm test", `package.json has a "test" script`, true
			}
		}
		return "", "", false
	},
	func(dir string) (string, string, bool) {
		raw, err := os.ReadFile(filepath.Join(dir, "pyproject.toml"))
		if err == nil && strings.Contains(string(raw), "pytest") {
			return "pytest", "pyproject.toml configures pytest", true
		}
		return "", "", false
	},
	func(dir string) (string, string, bool) {
		return "cargo test", "Cargo.toml", hasFile(dir, "Cargo.toml")
	},
	func(dir string) (string, string, bool) {
		ok := fileHasLine(filepath.Join(dir, "Makefile"), regexp.MustCompile(`^test:`))
		return "make test", "Makefile has a test target", ok
	},
}

var ciTestRun = regexp.MustCompile(`^\s*-?\s*(?:run:\s*)?((?:go test|npm test|npm run test|pytest|cargo test|make test)\b.*)$`)

// ciTest is the first test command a CI file runs, with file:line as evidence.
func ciTest(dir string) (cmd, evidence string, ok bool) {
	files, _ := filepath.Glob(filepath.Join(dir, ".github", "workflows", "*.y*ml"))
	files = append(files, filepath.Join(dir, ".gitlab-ci.yml"))
	for _, f := range files {
		fh, err := os.Open(f)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(fh)
		for n := 1; sc.Scan(); n++ {
			if m := ciTestRun.FindStringSubmatch(sc.Text()); m != nil {
				fh.Close()
				rel, _ := filepath.Rel(dir, f)
				return strings.TrimSpace(m[1]), rel + ":" + strconv.Itoa(n) + " runs it", true
			}
		}
		fh.Close()
	}
	return "", "", false
}

func hasFile(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}

func fileHasLine(path string, re *regexp.Regexp) bool {
	fh, err := os.Open(path)
	if err != nil {
		return false
	}
	defer fh.Close()
	sc := bufio.NewScanner(fh)
	for sc.Scan() {
		if re.MatchString(sc.Text()) {
			return true
		}
	}
	return false
}

// DepSignals is [round.envSetup, round.sharedDirs] read from the dependency
// manifests: the first manifest that installs into a directory in the tree
// wins. Cargo.toml gives none, on purpose: setup only suggests config keys,
// and the cargo choice is the CARGO_TARGET_DIR environment variable, which
// has no key. It is documented in docs/contributing/rounds.md and
// docs/usage/configuration.md, not surfaced by setup (#1872).
func DepSignals(dir string) (setup, shared Signal, ok bool) {
	if hasFile(dir, "package.json") {
		cmd, ev := "npm install", "package.json"
		switch {
		case hasFile(dir, "pnpm-lock.yaml"):
			cmd, ev = "pnpm install --frozen-lockfile", "pnpm-lock.yaml"
		case hasFile(dir, "yarn.lock"):
			cmd, ev = "yarn install --frozen-lockfile", "yarn.lock"
		case hasFile(dir, "package-lock.json"):
			cmd, ev = "npm ci", "package-lock.json"
		}
		b, _ := jsonx.MarshalCompact([]any{"node_modules"})
		return Signal{cmd, ev}, Signal{string(b), ev + " installs into node_modules (a slot with another lockfile installs its own)"}, true
	}
	if hasFile(dir, "pyproject.toml") {
		b, _ := jsonx.MarshalCompact([]any{".venv"})
		ev := "pyproject.toml"
		if hasFile(dir, "uv.lock") {
			ev = "pyproject.toml and uv.lock"
		}
		return Signal{"uv sync", ev}, Signal{string(b), ev + " installs into .venv (a slot with another lockfile installs its own)"}, true
	}
	return Signal{}, Signal{}, false
}
