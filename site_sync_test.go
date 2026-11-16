package rota_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// snapshotTree maps every file and directory under dir to its content, so two
// snapshots compare a tree byte for byte.
func snapshotTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	snap := map[string]string{}
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		if info.IsDir() {
			snap[rel+"/"] = ""
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		snap[rel] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func replaceOnce(t *testing.T, path, old, repl string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), old) {
		t.Fatalf("%s: no %q to replace", path, old)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(b), old, repl, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A run that fails validation must leave the site checkout exactly as it was:
// every check runs before the first delete or write.
func TestSiteSyncFailureLeavesSiteUntouched(t *testing.T) {
	cases := []struct {
		name   string
		break_ func(t *testing.T, root string)
		want   string
	}{
		{"unknown skill in a chain", func(t *testing.T, root string) {
			replaceOnce(t, filepath.Join(root, "docs/skills/README.md"), "- skill: rota-capture", "- skill: rota-nope")
		}, "rota-nope"},
		{"dead link in llms.txt", func(t *testing.T, root string) {
			p := filepath.Join(root, "llms.txt")
			b, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			add := "\n- [Gone](https://volkerotto.net/rota/docs/no-such-page/): dead\n"
			if err := os.WriteFile(p, append(b, add...), 0o644); err != nil {
				t.Fatal(err)
			}
		}, "no-such-page"},
		{"bad video catalog", func(t *testing.T, root string) {
			out := filepath.Join(root, "videos/out")
			if err := os.MkdirAll(out, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(out, "manifest.json"), []byte("{"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, "video catalog"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			copyTree(t, root, "docs", "skills", "scripts", "llms.txt", "README.md", "CHANGELOG.md")
			tc.break_(t, root)
			site := t.TempDir()
			script := filepath.Join(root, "scripts/site-sync.py")
			// A previous good output the failing run must not disturb.
			old := map[string]string{
				"rota/docs/index.md":       "old page\n",
				"rota/docs/stale/extra.md": "old extra\n",
				"rota/llms.txt":            "old llms\n",
				"_data/rota/nav.yml":       "old nav\n",
				"_data/rota/skills.json":   "[]\n",
				"_data/rota/chains.json":   "[]\n",
				"_data/rota/unrelated.yml": "keep\n",
			}
			keys := make([]string, 0, len(old))
			for k := range old {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				p := filepath.Join(site, k)
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(old[k]), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			before := snapshotTree(t, site)
			out, err := exec.Command("python3", script, site).CombinedOutput()
			if err == nil {
				t.Fatalf("site-sync passed a broken tree: %s", out)
			}
			if !strings.Contains(string(out), tc.want) {
				t.Fatalf("site-sync failed for another reason, want %q: %s", tc.want, out)
			}
			after := snapshotTree(t, site)
			if len(after) != len(before) {
				t.Errorf("site tree changed: %d entries before, %d after", len(before), len(after))
			}
			for k, v := range before {
				if got, ok := after[k]; !ok || got != v {
					t.Errorf("site entry %s changed (present=%v)", k, ok)
				}
			}
		})
	}
}
