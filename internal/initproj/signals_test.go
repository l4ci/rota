package initproj

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for f, body := range files {
		p := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTestCommandsPerManifest(t *testing.T) {
	ci := ".github/workflows/ci.yml"
	for name, tc := range map[string]struct {
		files      map[string]string
		fast, full string
		evidence   string
	}{
		"package.json":                       {map[string]string{"package.json": `{"scripts":{"test":"jest"}}`}, `["npm test"]`, `["npm test"]`, "package.json"},
		"package.json without a test script": {map[string]string{"package.json": `{"scripts":{"build":"tsc"}}`}, "", "", ""},
		"pyproject":                          {map[string]string{"pyproject.toml": "[tool.pytest.ini_options]\n"}, `["pytest"]`, `["pytest"]`, "pyproject.toml"},
		"cargo":                              {map[string]string{"Cargo.toml": "[package]\n"}, `["cargo test"]`, `["cargo test"]`, "Cargo.toml"},
		"makefile only":                      {map[string]string{"Makefile": "test:\n\techo hi\n"}, `["make test"]`, `["make test"]`, "Makefile"},
		"go beats package.json":              {map[string]string{"go.mod": "module x\n", "package.json": `{"scripts":{"test":"x"}}`}, `["go test ./..."]`, `["go test ./..."]`, "go.mod"},
		"makefile makes full":                {map[string]string{"go.mod": "module x\n", "Makefile": "test:\n\tgo test ./...\n"}, `["go test ./..."]`, `["make test"]`, "go.mod"},
		"ci line makes full":                 {map[string]string{"go.mod": "module x\n", "Makefile": "test:\n", ci: "jobs:\n  t:\n    steps:\n      - run: go test -race ./...\n"}, `["go test ./..."]`, `["go test -race ./..."]`, "go.mod"},
		"ci line without run key":            {map[string]string{"Cargo.toml": "x", ".gitlab-ci.yml": "test:\n  script:\n    - cargo test --all\n"}, `["cargo test"]`, `["cargo test --all"]`, "Cargo.toml"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeTree(t, dir, tc.files)
			sigs, ok := TestCommands(dir)
			if ok != (tc.fast != "") {
				t.Fatalf("ok = %v, want %v", ok, tc.fast != "")
			}
			if sigs[0].Value != tc.fast {
				t.Errorf("fast = %q, want %q", sigs[0].Value, tc.fast)
			}
			if !strings.Contains(sigs[0].Evidence, tc.evidence) {
				t.Errorf("fast evidence %q lacks %q", sigs[0].Evidence, tc.evidence)
			}
			if sigs[1].Value != tc.full {
				t.Errorf("full = %q, want %q", sigs[1].Value, tc.full)
			} else if strings.Contains(name, "ci line") && !strings.Contains(sigs[1].Evidence, ":") {
				t.Errorf("full evidence %q should name file:line", sigs[1].Evidence)
			}
		})
	}
}

func TestDepSignalsPerManifest(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files []string
		setup string
		dirs  string
	}{
		{"npm lock", []string{"package.json", "package-lock.json"}, "npm ci", `["node_modules"]`},
		{"pnpm lock", []string{"package.json", "pnpm-lock.yaml"}, "pnpm install --frozen-lockfile", `["node_modules"]`},
		{"yarn lock", []string{"package.json", "yarn.lock"}, "yarn install --frozen-lockfile", `["node_modules"]`},
		{"no lock", []string{"package.json"}, "npm install", `["node_modules"]`},
		{"python", []string{"pyproject.toml", "uv.lock"}, "uv sync", `[".venv"]`},
		{"cargo", []string{"Cargo.toml"}, "", ""},
		{"go", []string{"go.mod"}, "", ""},
	} {
		dir := t.TempDir()
		files := map[string]string{}
		for _, f := range tc.files {
			files[f] = "{}"
		}
		writeTree(t, dir, files)
		setup, shared, ok := DepSignals(dir)
		if (tc.setup != "") != ok || setup.Value != tc.setup || shared.Value != tc.dirs {
			t.Errorf("%s: got %q %q ok=%v, want %q %q", tc.name, setup.Value, shared.Value, ok, tc.setup, tc.dirs)
		}
		if ok && (setup.Evidence == "" || shared.Evidence == "") {
			t.Errorf("%s: a signal carries no evidence", tc.name)
		}
	}
}
