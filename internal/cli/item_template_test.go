package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/backlog/trackertest"
	"github.com/l4ci/rota/internal/golden"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/tracker"
)

func TestItemReadyRequiresFeatureSections(t *testing.T) {
	for _, backend := range []string{"file", "issues"} {
		for _, tc := range []struct {
			name, typ, body, missing string
			decision, note           bool
		}{
			{"missing boundary", "F", "## Acceptance\n- [ ] Works\n", "## Out of scope", false, false},
			{"checkbox without heading", "F", "- [ ] Works\n## Out of scope\n", "## Acceptance", false, false},
			{"design without headings", "F", "", "## Acceptance", false, true},
			{"complete feature", "F", "## Acceptance\n## Out of scope\n", "", false, false},
			{"research exemption", "F", "## Acceptance\n", "", true, false},
			{"bug unchanged", "B", "## Acceptance\n", "", false, false},
			{"task unchanged", "T", "## Acceptance\n", "", false, false},
		} {
			t.Run(backend+"/"+tc.name, func(t *testing.T) {
				cfg := ""
				deps := testDeps()
				id := tc.typ + "01"
				if backend == "issues" {
					cfg = `{"forge":{"provider":"github"},"issues":{"source":"github"}}`
					id = "1"
					labels := []string{map[string]string{"F": "type:feature", "B": "type:bug", "T": "type:task"}[tc.typ]}
					if tc.decision {
						labels = append(labels, "decision:research")
					}
					is := tracker.Issue{Key: tracker.NumKey(1), Title: "Ready template example", State: "open", Body: tc.body, Labels: labels}
					if tc.note {
						is.Comments = []tracker.Comment{{ID: "1", Body: "<!-- rota:design -->\nDesign already exists"}}
					}
					deps = withTracker(t, &trackertest.Fake{Issues: []tracker.Issue{is}})
				}
				root := trackerProject(t, cfg)
				if backend == "file" {
					kind := map[string]string{"F": "features", "B": "bugs", "T": "tasks"}[tc.typ]
					field := ""
					if tc.decision {
						field = " Decision: research"
					}
					knWrite(t, filepath.Join(root, ".rota", "BACKLOG.md"), "# TODO\n\n## "+strings.ToUpper(kind[:1])+kind[1:]+"\n- **["+id+"] Ready template example.**"+field+"\n\n## Completed\n")
					knWrite(t, filepath.Join(root, ".rota", kind, id+".md"), tc.body)
					if tc.note {
						knWrite(t, filepath.Join(root, ".rota", "designs", id+".md"), "Design already exists")
					}
				}
				code, env, stderr := rotaRunWith(t, deps, "--json", "-C", root, "item", "ready", id)
				data := env["data"].(*jsonx.Object)
				ready, _ := data.Get("ready")
				reasons, _ := data.Get("reasons")
				if tc.missing != "" {
					if code != ExitFailed || ready != false || !strings.Contains(fmt.Sprint(reasons), tc.missing) {
						t.Fatalf("item ready must name missing %s: exit=%d data=%v stderr=%s", tc.missing, code, data, stderr)
					}
				} else if code != 0 || ready != true {
					t.Fatalf("item should be ready: exit=%d data=%v stderr=%s", code, data, stderr)
				}
			})
		}
	}
}

func TestItemCreateWarnsForMissingFeatureSections(t *testing.T) {
	root := trackerProject(t, "")
	if err := os.WriteFile(filepath.Join(root, ".rota", "BACKLOG.md"), []byte("# TODO\n\n## Features\n\n## Completed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, env, stderr := rotaRun(t, "--json", "-C", root, "item", "create", "--kind", "features", "--tag", "Major", "--title", "Add item templates")
	if code != 0 {
		t.Fatalf("create must succeed: exit %d: %s", code, stderr)
	}
	data := env["data"].(*jsonx.Object)
	warnings, _ := data.Get("warnings")
	id, _ := data.Get("id")
	assertCreateWarningsIdentifyItemAndFix(t, id, warnings)
	for _, section := range []string{"## Acceptance", "## Out of scope", "## Touches"} {
		if !strings.Contains(stderr, section) || !strings.Contains(fmt.Sprint(warnings), section) {
			t.Errorf("missing warning for %s: data=%v stderr=%q", section, warnings, stderr)
		}
	}
}

func assertCreateWarningsIdentifyItemAndFix(t *testing.T, id, warnings any) {
	t.Helper()
	list, ok := warnings.([]any)
	if !ok || len(list) == 0 {
		t.Fatalf("expected warning messages, got %v", warnings)
	}
	for _, warning := range list {
		message := fmt.Sprint(warning)
		for _, want := range []string{fmt.Sprint(id) + ":", "add it to the item body", "rota item template --kind features"} {
			if !strings.Contains(message, want) {
				t.Errorf("warning must identify its item and fix step; missing %q in %q", want, message)
			}
		}
	}
}

func TestItemTemplatePrintable(t *testing.T) {
	got := knNew(t, t.TempDir(), "", "item", "template", "--kind", "features", "--tag", "Major")
	if got.rc != 0 {
		t.Fatalf("template must print without a repository: exit %d: %s", got.rc, got.stderr)
	}
	for _, want := range []string{"## Acceptance\n\n<!-- required -->", "## Out of scope\n\n<!-- required -->", "## Touches\n\n<!-- recommended -->"} {
		if !strings.Contains(got.stdout, want) {
			t.Errorf("template missing %q: %s", want, got.stdout)
		}
	}
	golden.Check(t, "item template --kind features --tag Major", got.stdout)
}

func TestItemCreateWarnsForBatchAndRaw(t *testing.T) {
	for _, tc := range []struct {
		name, flag, input string
	}{
		{"batch", "--batch-file", `[{"kind":"features","tag":"Major","title":"Add item templates"}]`},
		{"raw", "--raw-file", "- **[F77] [Major] Add item templates.**\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := trackerProject(t, "")
			if err := os.WriteFile(filepath.Join(root, ".rota", "BACKLOG.md"), []byte("# TODO\n\n## Features\n\n## Completed\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			input := filepath.Join(t.TempDir(), "input")
			if err := os.WriteFile(input, []byte(tc.input), 0o644); err != nil {
				t.Fatal(err)
			}
			args := []string{"--json", "-C", root, "item", "create", tc.flag, input}
			if tc.name == "raw" {
				args = append(args, "--kind", "features")
			}
			code, env, stderr := rotaRun(t, args...)
			if code != 0 {
				t.Fatalf("capture must succeed: exit %d: %s", code, stderr)
			}
			data := env["data"].(*jsonx.Object)
			warnings, _ := data.Get("warnings")
			for _, section := range []string{"## Acceptance", "## Out of scope", "## Touches"} {
				if !strings.Contains(stderr, section) || !strings.Contains(fmt.Sprint(warnings), section) {
					t.Errorf("missing warning for %s: data=%v stderr=%q", section, warnings, stderr)
				}
			}
			if tc.name == "batch" {
				items, _ := data.Get("items")
				row := items.([]any)[0].(*jsonx.Object)
				id, _ := row.Get("id")
				warnings, _ := row.Get("warnings")
				assertCreateWarningsIdentifyItemAndFix(t, id, warnings)
				if !strings.Contains(fmt.Sprint(warnings), "## Out of scope") {
					t.Errorf("batch warning must identify its item: %v", items)
				}
			} else {
				id, _ := data.Get("id")
				assertCreateWarningsIdentifyItemAndFix(t, id, warnings)
			}
		})
	}
}

func TestItemCreateTemplateWarningsAcrossBackends(t *testing.T) {
	for _, backend := range []string{"file", "issues"} {
		for _, tc := range []struct {
			name, kind, desc, body string
			warnings               []string
		}{
			{"incomplete bug", "bugs", "It crashes", "", []string{"## Repro", "## Expected", "## Actual"}},
			{"bug described inline", "bugs", "Repro: save. Expected: saved. Actual: crashes.", "", nil},
			{"plain task", "tasks", "Clean up the helper", "", nil},
			{"complete feature", "features", "A bounded change", "## Acceptance\n\n## Out of scope\n", nil},
		} {
			t.Run(backend+"/"+tc.name, func(t *testing.T) {
				cfg := ""
				deps := testDeps()
				if backend == "issues" {
					cfg = `{"forge":{"provider":"github"},"issues":{"source":"github"}}`
					deps = withTracker(t, &trackertest.Fake{})
				}
				root := trackerProject(t, cfg)
				if err := os.WriteFile(filepath.Join(root, ".rota", "BACKLOG.md"), []byte("# TODO\n\n## Bugs\n\n## Features\n\n## Tasks\n\n## Completed\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				body := filepath.Join(t.TempDir(), "body.md")
				if err := os.WriteFile(body, []byte(tc.body), 0o644); err != nil {
					t.Fatal(err)
				}
				code, env, stderr := rotaRunWith(t, deps, "--json", "-C", root, "item", "create", "--kind", tc.kind, "--title", "Capture template example", "--desc", tc.desc, "--body-file", body)
				if code != 0 {
					t.Fatalf("capture must succeed: exit %d: %s", code, stderr)
				}
				data := env["data"].(*jsonx.Object)
				warnings, found := data.Get("warnings")
				if len(tc.warnings) == 0 {
					if found || stderr != "" {
						t.Errorf("unexpected warnings: %v %s", warnings, stderr)
					}
					return
				}
				for _, want := range tc.warnings {
					if !strings.Contains(fmt.Sprint(warnings), want) || !strings.Contains(stderr, want) {
						t.Errorf("missing %s warning: %v %s", want, warnings, stderr)
					}
				}
			})
		}
	}
}
