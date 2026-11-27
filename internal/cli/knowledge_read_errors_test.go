package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBlockReportsUnreadableSourceWithoutWriting(t *testing.T) {
	for _, fault := range []string{"directory", "permission", "symlink"} {
		for _, tc := range []struct {
			name, source, target string
			args                 []string
		}{
			{"knowledge", ".rota/KNOWLEDGE.md", "AGENTS.md", []string{"block", "knowledge"}},
			{"decisions", ".rota/DECISIONS.md", "AGENTS.md", []string{"block", "decisions"}},
			{"umbrella source", ".rota/KNOWLEDGE.md", "web/AGENTS.md", []string{"block", "knowledge", "--repo", "web"}},
			{"sub-repo source", ".rota/knowledge/web/KNOWLEDGE.md", "web/AGENTS.md", []string{"block", "knowledge", "--repo", "web"}},
		} {
			t.Run(tc.name+"/"+fault, func(t *testing.T) {
				dir := knProject(t, true)
				const before = "# Instructions\n\n<!-- rota-knowledge-start -->\nKeep existing topics.\n<!-- rota-knowledge-end -->\n"
				target := filepath.Join(dir, tc.target)
				knWrite(t, target, before)
				source := filepath.Join(dir, tc.source)
				if err := os.Remove(source); err != nil && !os.IsNotExist(err) {
					t.Fatal(err)
				}
				switch fault {
				case "directory":
					// This read failure also holds for a privileged runner.
					if err := os.Mkdir(source, 0o755); err != nil {
						t.Fatal(err)
					}
				case "symlink":
					if err := os.Symlink(filepath.Base(source), source); err != nil {
						t.Fatal(err)
					}
				case "permission":
					knWrite(t, source, "## Hidden topic\n")
					if err := os.Chmod(source, 0); err != nil {
						t.Fatal(err)
					}
					if _, err := os.ReadFile(source); err == nil {
						t.Skip("runner bypasses file permissions")
					}
				}
				got := knNew(t, dir, "", tc.args...)
				if got.rc == 0 || !strings.Contains(got.stderr, tc.source) {
					t.Errorf("expected error naming source %s: %+v", tc.source, got)
				}
				after, err := os.ReadFile(target)
				if err != nil || string(after) != before {
					t.Errorf("instructions changed after failed read: %q (%v)", after, err)
				}
			})
		}
	}
}

func TestMapAndQAReportUnreadableSources(t *testing.T) {
	for _, args := range [][]string{
		{"map", "index"}, {"qa", "index"}, {"map", "stats"}, {"map", "stats", "--cap"}, {"backlog", "stale", "--kind", "map"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			for _, fault := range []string{"file", "directory"} {
				t.Run(fault, func(t *testing.T) {
					dir := knProject(t, false)
					const before = "# Instructions\n\nPreserve this file.\n"
					knWrite(t, filepath.Join(dir, "AGENTS.md"), before)
					group := args[0]
					if group == "backlog" {
						group = "map"
					}
					source := filepath.Join(dir, ".rota", group)
					if fault == "file" {
						knWrite(t, filepath.Join(source, "alpha.md"), "---\nsummary: Readable sibling\n---\n")
						source = filepath.Join(source, "broken.md")
					} else if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
						t.Fatal(err)
					}
					// A loop is a persistent read/list error, not an absent source.
					if err := os.Symlink(filepath.Base(source), source); err != nil {
						t.Fatal(err)
					}
					got := knNew(t, dir, "", args...)
					if got.rc == 0 || !strings.Contains(got.stderr, source) {
						t.Errorf("expected read error naming %s: %+v", source, got)
					}
					after, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
					if err != nil || string(after) != before {
						t.Errorf("instructions changed: %q (%v)", after, err)
					}
				})
			}
		})
	}
}

func TestExternalTopicsReportUnreadableFiles(t *testing.T) {
	for _, rel := range []string{"topics/go.md", "topics/git/README.md", "topics/git/hooks.md"} {
		t.Run(rel, func(t *testing.T) {
			project, clone := extWalkFixture(t)
			if strings.HasSuffix(rel, "hooks.md") {
				knWrite(t, filepath.Join(clone, "topics/git/README.md"), "# Git\n\n- [Hooks](hooks.md) — checks\n")
			}
			source := filepath.Join(clone, rel)
			if err := os.Remove(source); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if err := os.Mkdir(source, 0o755); err != nil {
				t.Fatal(err)
			}
			got := knNew(t, project, "", "knowledge", "topics", "--external")
			if got.rc == 0 || !strings.Contains(got.stderr, rel) {
				t.Fatalf("expected error naming %s: %+v", rel, got)
			}
		})
	}
}

func TestMissingKnowledgeAndMapSourcesStayEmpty(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"block", "knowledge"}, "no topics yet"},
		{[]string{"block", "decisions"}, "no decisions yet"},
		{[]string{"map", "index"}, "no subsystems yet"},
		{[]string{"qa", "index"}, "no QA strategy yet"},
		{[]string{"map", "stats", "--json"}, `"subsystems": [], "count": 0`},
		{[]string{"backlog", "stale", "--kind", "map"}, ""},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			dir := knProject(t, false)
			if err := os.Remove(filepath.Join(dir, ".rota", "KNOWLEDGE.md")); err != nil {
				t.Fatal(err)
			}
			got := knNew(t, dir, "", tc.args...)
			if got.rc != 0 {
				t.Fatalf("missing source: %+v", got)
			}
			if tc.args[0] == "block" || tc.args[1] == "index" {
				raw, err := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
				if err != nil || !strings.Contains(string(raw), tc.want) {
					t.Fatalf("empty block: %q (%v)", raw, err)
				}
			} else if (tc.want == "" && got.stdout != "") || !strings.Contains(got.stdout, tc.want) {
				t.Fatalf("empty output: %+v", got)
			}
		})
	}
}
