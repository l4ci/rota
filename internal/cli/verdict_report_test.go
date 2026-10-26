package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/backlog/trackertest"
	"github.com/l4ci/rota/internal/tracker"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/verdict"
)

func TestVerdictReportPendingKeepsRecord(t *testing.T) {
	dir := verdictRepo(t)
	code, out, errs := rotaIn(t, dir, "verdict", "add", "--kind", "qa", "--verdict", "PASS", "--json")
	if code != 0 {
		t.Fatalf("record: %d %s", code, errs)
	}
	d := data(t, out)
	if d["report"] != "pending" || !strings.Contains(d["reportReason"].(string), "linked item") {
		t.Fatalf("want pending report with reason, got %v", d)
	}
	if _, ok := verdict.Latest(verdict.Load(dir).Branches["feat/x"], verdict.QA); !ok {
		t.Fatal("pending report lost local verdict")
	}
}

func TestVerdictReportFileUpsertAndRetry(t *testing.T) {
	dir := fileDeferredRepo(t)
	write(t, filepath.Join(dir, ".rota", "BACKLOG.md"), "# TODO\n\n## Tasks\n\n- **[T01] [Minor] Report target.** Evidence\n\n## Completed\n")
	gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "Implement [T01]")
	artifact := ".rota/qa-runs/run/check/output.txt"
	write(t, filepath.Join(dir, artifact), "check passed\n")
	body := writeBody(t, t.TempDir(), `{"summary":"go test passed", "findings":[{"severity":"info","title":"go test ./internal/verdict", "detail":"PASS: .rota/qa-runs/run/check/output.txt"}]}`)
	for _, result := range []string{"PASS", "CONCERNS"} {
		code, out, errs := rotaIn(t, dir, "verdict", "add", "--kind", "qa", "--verdict", result, "--body-file", body, "--json")
		if code != 0 || data(t, out)["report"] != "posted" {
			t.Fatalf("record: %d %s %s", code, out, errs)
		}
	}
	text := readTask(t, dir, "T01")
	if strings.Count(text, "<!-- rota:report qa sha=") != 1 || !strings.Contains(text, "CONCERNS") || !strings.Contains(text, "### Artifacts\n\n- `"+artifact+"`") {
		t.Fatalf("want one updated report citing existing artifact:\n%s", text)
	}
	gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "More [T01]")
	if code, _, errs := rotaIn(t, dir, "verdict", "add", "--kind", "qa", "--verdict", "PASS"); code != 0 {
		t.Fatal(errs)
	}
	if strings.Count(readTask(t, dir, "T01"), "<!-- rota:report qa sha=") != 2 {
		t.Fatal("new sha did not append a report")
	}
	if code, out, errs := rotaIn(t, dir, "verdict", "post", "--json"); code != 0 || data(t, out)["report"] != "posted" {
		t.Fatalf("retry: %d %s %s", code, out, errs)
	}
	if strings.Count(readTask(t, dir, "T01"), "<!-- rota:report qa sha=") != 2 {
		t.Fatal("retry duplicated report")
	}
}

func TestVerdictReportIssueTargetsAndPendingRetry(t *testing.T) {
	dir := verdictRepo(t)
	write(t, filepath.Join(dir, ".rota", "config.json"), `{"issues":{"source":"github"},"forge":{"provider":"github"},"ship":{"review":"none"}}`)
	gitIn(t, dir, "branch", "-m", "ben/7-report")
	items := &trackertest.Fake{Issues: []tracker.Issue{{Key: tracker.NumKey(7), Title: "lead", State: "open"}, {Key: tracker.NumKey(8), Title: "linked", State: "open"}}}
	notes := []tracker.Comment{}
	offline := false
	nextID := 0
	fake := &forge{answer: func(_ string, args []string) (string, string, int) {
		call := strings.Join(args, " ")
		if offline {
			return "", "offline", 1
		}
		if strings.HasPrefix(call, "pr list ") {
			return `[{"number":20,"headRefName":"ben/7-report","body":"Closes #7. Closes #8."}]`, "", 0
		}
		if strings.Contains(call, "issues/20/comments") {
			if slices.Contains(args, "POST") {
				nextID++
				var body string
				for _, arg := range args {
					if strings.HasPrefix(arg, "body=") {
						body = strings.TrimPrefix(arg, "body=")
					}
				}
				notes = append(notes, tracker.Comment{ID: strconv.Itoa(nextID), Body: body})
				return fmt.Sprintf(`{"id":%d}`, nextID), "", 0
			}
			// gh comment reader accepts GitHub's API object shape, not tracker.Comment.
			rows := []map[string]any{}
			for _, n := range notes {
				rows = append(rows, map[string]any{"id": n.ID, "body": n.Body})
			}
			raw, _ := json.Marshal(rows)
			return string(raw), "", 0
		}
		if slices.Contains(args, "PATCH") && strings.Contains(call, "issues/comments/") {
			for _, arg := range args {
				if strings.HasPrefix(arg, "body=") {
					notes[0].Body = strings.TrimPrefix(arg, "body=")
				}
			}
			return `{}`, "", 0
		}
		return "[]", "", 0
	}}
	deps := useForge(t, fake)
	deps.NewTracker = func(context.Context, string, any) (backlog.Tracker, error) { return items, nil }
	run := func(v string) trOut {
		return trRunWith(t, deps, dir, "", "verdict", "add", "--kind", "review-spec", "--verdict", v, "--json")
	}
	for _, v := range []string{"CONCERNS", "PASS"} {
		o := run(v)
		t.Logf("%s report counts: PR=%d item7=%d item8=%d", v, len(notes), len(items.Issues[0].Comments), len(items.Issues[1].Comments))
		if o.code != 0 {
			t.Fatalf("%s: %+v", v, o)
		}
		if data(t, o.stdout)["report"] != "posted" {
			t.Errorf("%s report status: %+v", v, o)
		}
	}
	if len(notes) != 1 || !strings.Contains(notes[0].Body, "**PASS**") {
		t.Fatalf("PR notes: %+v", notes)
	}
	for _, it := range items.Issues {
		if len(it.Comments) != 1 || !strings.Contains(it.Comments[0].Body, "**PASS**") {
			t.Fatalf("item %s comments: %+v", it.Key, it.Comments)
		}
	}
	t.Logf("same kind/sha reports: PR=%d item7=%d item8=%d", len(notes), len(items.Issues[0].Comments), len(items.Issues[1].Comments))
	gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "new head")
	offline = true
	o := run("PASS")
	if o.code != 0 || data(t, o.stdout)["report"] != "pending" {
		t.Fatalf("offline record: %+v", o)
	}
	if len(verdict.Load(dir).Branches["ben/7-report"]) != 3 {
		t.Fatal("offline record was lost")
	}
	offline = false
	o = trRunWith(t, deps, dir, "", "verdict", "post", "--json")
	if o.code != 0 || data(t, o.stdout)["report"] != "posted" {
		t.Fatalf("retry: %+v", o)
	}
	if len(notes) != 2 {
		t.Fatalf("new head reports=%d, want 2", len(notes))
	}
	for _, it := range items.Issues {
		if len(it.Comments) != 2 {
			t.Fatalf("new head item reports=%d, want 2", len(it.Comments))
		}
	}
}

func TestVerdictArtifactsOnlyExistingCitations(t *testing.T) {
	root := t.TempDir()
	existing := ".rota/qa-runs/run/check/stdout.txt"
	write(t, filepath.Join(root, existing), "passed")
	r := verdict.Record{Summary: "checks: `" + existing + "`", Findings: []verdict.Finding{{File: existing, Detail: "missing .rota/qa-runs/run/missing.txt and .rota/qa-runs/../../outside"}}}
	if paths := verdictArtifacts(root, r); len(paths) != 1 || paths[0] != existing {
		t.Fatalf("artifacts=%v, want only existing cited output", paths)
	}
}
