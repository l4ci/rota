package testaudit

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func fixture(t *testing.T, files map[string]string) (string, []string) {
	t.Helper()
	root := t.TempDir()
	var names []string
	for name, body := range files {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	return root, names
}
func TestInventoryPlaywrightMatrix(t *testing.T) {
	root, files := fixture(t, map[string]string{"playwright.visual.config.mts": "", "tests/playwright.config.ts": "", "tests/README.md": "1 scenarios\n2 refs\n"})
	r := Inventory(root, files, nil, func(config string) ([]byte, error) {
		if config == "tests/playwright.config.ts" {
			return nil, errors.New("local runner missing")
		}
		return []byte(`{"suites":[{"file":"tests/a.spec.ts","specs":[{"title":"works","line":3,"column":1,"tests":[{"projectName":"chrome"},{"projectName":"webkit"}]}]}]}`), nil
	})
	if len(r.Playwright) != 2 {
		t.Fatalf("configs: %+v", r)
	}
	p := r.Playwright[0]
	if p.Config != "playwright.visual.config.mts" || p.Scenarios != 1 || p.Runs != 2 || p.Projects["webkit"] != 1 {
		t.Fatalf("matrix: %+v", p)
	}
	if r.Complete || r.Playwright[1].Error == "" {
		t.Fatalf("missing runner must remain incomplete: %+v", r)
	}
	if len(r.DocCounts) != 2 || r.DocCounts[0].Actual != nil {
		t.Fatalf("failed scoped listing cannot produce drift: %+v", r.DocCounts)
	}
}

func TestInventoryStaticRelationships(t *testing.T) {
	root, files := fixture(t, map[string]string{
		"src/widget.ts": "export const widget=1", "src/other.ts": "",
		"tests/widget.test.ts": "function setup() {}", "tests/widget.integration.spec.ts": "import {widget} from '@/widget'; const setup = () => {};", "packages/ui/test/other.test.ts": "import {other} from '../../../src/other.js'; function setup() {}",
		"package.json":              `{"scripts":{"test":"npm run test:unit && npm --prefix packages/api run test:int","test:unit":"vitest","test:unused":"echo unused"}}`,
		"packages/api/package.json": `{"scripts":{"test:int":"vitest","test:accounts":"vitest"}}`,
	})
	r := Inventory(root, files, []string{"npm test"}, nil)
	if len(r.Modules) != 2 || r.Modules[1].Module != "src/widget.ts" || len(r.Modules[1].Tests) != 2 {
		t.Fatalf("module candidates must include conventional filename and imports: %+v", r.Modules)
	}
	if len(r.Helpers) != 1 || r.Helpers[0].Name != "setup" || len(r.Helpers[0].Paths) != 3 {
		t.Fatalf("duplicate helper candidates: %+v", r.Helpers)
	}
	if len(r.UnwiredScripts) != 2 || r.UnwiredScripts[0].Name != "test:unused" || r.UnwiredScripts[1].Path != "packages/api/package.json" || r.UnwiredScripts[1].Name != "test:accounts" {
		t.Fatalf("transitive script reachability: %+v", r.UnwiredScripts)
	}
}

func TestInventoryDocExplicitScope(t *testing.T) {
	root, files := fixture(t, map[string]string{"tests/a/playwright.config.ts": "", "tests/b/playwright.config.ts": "", "tests/README.md": "tests/a/playwright.config.ts: 2 scenarios, 4 refs\n99 scenarios\n"})
	r := Inventory(root, files, nil, func(string) ([]byte, error) {
		return []byte(`{"suites":[{"file":"tests/a.spec.ts","specs":[{"title":"one","line":1,"tests":[{"projectName":"chrome"},{"projectName":"webkit"}]}]}]}`), nil
	})
	if len(r.DocCounts) != 3 || r.DocCounts[0].Status != "drift" || r.DocCounts[0].Actual == nil || *r.DocCounts[0].Actual != 1 || r.DocCounts[0].Config != "tests/a/playwright.config.ts" {
		t.Fatalf("explicit config scenario scope: %+v", r.DocCounts)
	}
	if r.DocCounts[1].Actual != nil || r.DocCounts[2].Actual != nil {
		t.Fatalf("refs and ambiguous config counts must stay unavailable: %+v", r.DocCounts)
	}
}

func TestInventoryStoredReferenceDrift(t *testing.T) {
	root, files := fixture(t, map[string]string{"playwright.config.ts": "", "tests/README.md": "2 refs\n", "tests/visual.spec.ts-snapshots/home-chrome.png": "", "tests/other.spec.ts-snapshots/unrelated.png": ""})
	r := Inventory(root, files, nil, func(string) ([]byte, error) {
		return []byte(`{"suites":[{"file":"tests/visual.spec.ts","specs":[{"title":"home","line":1,"tests":[{"projectName":"chrome"},{"projectName":"webkit"}]}]}]}`), nil
	})
	if len(r.DocCounts) != 1 || r.DocCounts[0].Status != "drift" || r.DocCounts[0].Actual == nil || *r.DocCounts[0].Actual != 1 {
		t.Fatalf("tracked references differ from both claimed refs and expanded runs: %+v", r.DocCounts)
	}
}
