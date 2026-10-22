package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/bugreport"
	"github.com/l4ci/rota/internal/gate"
	"github.com/l4ci/rota/internal/git"
)

var brOK = []string{"--confirm", "--confirm-note", "yes, file it"}

func TestBugreportUsesDepsGitForSharedLog(t *testing.T) {
	root, main := trProject(t, "{}"), trProject(t, "{}")
	d := testDeps()
	d.Git = func(context.Context, string, ...string) (git.Result, error) {
		return git.Result{Stdout: filepath.Join(main, ".git")}, nil
	}
	if o := trRunWith(t, d, root, "", "bugreport", "add", "--summary", "shared mistake"); o.code != 0 {
		t.Fatalf("add: %+v", o)
	}
	shared := filepath.Join(main, ".rota", "bugreports.jsonl")
	if b, err := os.ReadFile(shared); err != nil || !strings.Contains(string(b), "shared mistake") {
		t.Fatalf("shared log = %q, %v; want record under injected common dir", b, err)
	}
	if o := trRunWith(t, d, root, "", "bugreport", "list"); o.code != 0 || !strings.Contains(o.stdout, "shared mistake") {
		t.Fatalf("list: %+v", o)
	}
	if o := trRunWith(t, d, root, "", "bugreport", "dismiss", "bug-1"); o.code != 0 {
		t.Fatalf("dismiss: %+v", o)
	}
	if o := trRunWith(t, d, root, "", "bugreport", "list"); o.code != 0 || strings.Contains(o.stdout, "shared mistake") {
		t.Fatalf("list after dismiss: %+v", o)
	}
	if _, err := os.Stat(filepath.Join(root, ".rota", "bugreports.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("worker-local log exists: %v", err)
	}
}

func addRecord(t *testing.T, dir string) {
	t.Helper()
	o := trRun(t, dir, "", "bugreport", "add", "--summary", "worker done refused a valid attempt, mail me at me@example.com", "--expected", "it should accept it", "--verb", "worker done")
	if o.code != 0 {
		t.Fatalf("add: %+v", o)
	}
}

func TestBugreportAddListDraft(t *testing.T) {
	dir := trProject(t, "{}")
	addRecord(t, dir)
	recs, _ := bugreport.Load(context.Background(), git.Exec, dir)
	if len(recs) != 1 || recs[0].Source != bugreport.SourceAgent || recs[0].Verb != "worker done" || recs[0].Expected != "it should accept it" {
		t.Fatalf("records %+v", recs)
	}
	if o := trRun(t, dir, "", "bugreport", "list"); o.code != 0 || !strings.Contains(o.stdout, "bug-1") {
		t.Fatalf("list %+v", o)
	}
	o := trRun(t, dir, "", "bugreport", "draft", "bug-1", "--harness", "claude", "--model", "sonnet")
	if o.code != 0 {
		t.Fatalf("draft %+v", o)
	}
	for _, want := range []string{"## Environment", "- harness: claude", "- model: sonnet", "- OS/arch: ", "- rota: ", "<email>"} {
		if !strings.Contains(o.stdout, want) {
			t.Errorf("draft lacks %q:\n%s", want, o.stdout)
		}
	}
	if strings.Contains(o.stdout, "me@example.com") {
		t.Errorf("draft leaks the email:\n%s", o.stdout)
	}
	if o := trRun(t, dir, "", "bugreport", "draft", "bug-9"); o.code != 3 {
		t.Errorf("unknown id: %+v", o)
	}
}

func TestBugreportDraftTakesHarnessAndModelFromTheSlot(t *testing.T) {
	dir := trProject(t, "{}")
	addRecord(t, dir)
	reg := `{"slots":[{"name":"dana","kind":"claude","model":"sonnet-5"}]}`
	if err := os.WriteFile(filepath.Join(dir, ".rota", "workers.json"), []byte(reg), 0o644); err != nil {
		t.Fatal(err)
	}
	d := testDeps()
	env := d.Getenv
	d.Getenv = func(k string) string {
		if k == "ROTA_SLOT" {
			return "dana"
		}
		return env(k)
	}
	o := trRunWith(t, d, dir, "", "bugreport", "draft", "bug-1")
	if !strings.Contains(o.stdout, "- harness: claude") || !strings.Contains(o.stdout, "- model: sonnet-5") {
		t.Fatalf("%+v", o)
	}
}

func TestBugreportFileIsAManualGate(t *testing.T) {
	for _, level := range []string{"off", "auto"} {
		dir := trProject(t, gateConfig(t, "", level, nil))
		addRecord(t, dir)
		f := &forge{answer: func(string, []string) (string, string, int) { return "https://github.com/l4ci/rota/issues/9\n", "", 0 }}
		d := useForge(t, f)
		o := trRunWith(t, d, dir, "", "bugreport", "file", "bug-1")
		if o.code != 4 || len(f.calls) != 0 {
			t.Fatalf("%s: unconfirmed filing: %+v calls %q", level, o, f.calls)
		}
		if open, _ := bugreport.Open(context.Background(), git.Exec, dir); len(open) != 1 {
			t.Errorf("%s: refusal changed the record: %+v", level, open)
		}
		if len(gateAudit(t, dir)) != 0 {
			t.Errorf("%s: refusal audited", level)
		}
	}
}

func TestBugreportFileCreatesTheIssueAndMarksFiled(t *testing.T) {
	dir := trProject(t, gateConfig(t, "", "auto", nil))
	addRecord(t, dir)
	f := &forge{answer: func(_ string, args []string) (string, string, int) {
		if args[0] == "issue" {
			return "https://github.com/l4ci/rota/issues/9\n", "", 0
		}
		return "", "", 0
	}}
	d := useForge(t, f)
	t.Setenv("ROTA_UPSTREAM_REPO", "")
	o := trRunWith(t, d, dir, "", append([]string{"bugreport", "file", "bug-1", "--harness", "claude", "--model", "sonnet"}, brOK...)...)
	if o.code != 0 || !strings.Contains(o.stdout, "https://github.com/l4ci/rota/issues/9") {
		t.Fatalf("%+v", o)
	}
	if len(f.calls) != 2 || !strings.HasPrefix(f.calls[1], "gh issue create -R l4ci/rota -t rota worker done: ") {
		t.Fatalf("calls %q", f.calls)
	}
	if body := f.stdins[1]; !strings.Contains(body, "## Environment") || strings.Contains(body, "me@example.com") {
		t.Errorf("filed body:\n%s", body)
	}
	recs, _ := bugreport.Load(context.Background(), git.Exec, dir)
	if recs[0].Status != bugreport.StatusFiled || recs[0].Issue != "https://github.com/l4ci/rota/issues/9" {
		t.Errorf("record %+v", recs[0])
	}
	audit := gateAudit(t, dir)
	if len(audit) != 1 || audit[0]["gate"] != gate.BugreportFile || audit[0]["note"] != "yes, file it" {
		t.Errorf("audit %+v", audit)
	}
}

func TestBugreportFileSurfacesTheURLWhenTheStatusUpdateFails(t *testing.T) {
	dir := trProject(t, gateConfig(t, "", "auto", nil))
	addRecord(t, dir)
	f := &forge{answer: func(_ string, args []string) (string, string, int) {
		if args[0] == "issue" {
			// The issue is created; the bug log then becomes unreadable so the status update fails.
			log := bugreport.Path(context.Background(), git.Exec, dir)
			_ = os.Remove(log)
			_ = os.MkdirAll(log, 0o755)
			return "https://github.com/l4ci/rota/issues/9\n", "", 0
		}
		return "", "", 0
	}}
	d := useForge(t, f)
	t.Setenv("ROTA_UPSTREAM_REPO", "")
	o := trRunWith(t, d, dir, "", append([]string{"bugreport", "file", "bug-1", "--harness", "claude", "--model", "sonnet"}, brOK...)...)
	if !strings.Contains(o.stdout+o.stderr, "https://github.com/l4ci/rota/issues/9") {
		t.Fatalf("the created URL is not surfaced: %+v", o)
	}
	if !strings.Contains(o.stdout+o.stderr, "bug-1") || !strings.Contains(o.stdout+o.stderr, "dismiss") {
		t.Errorf("no hint to close the record by hand: %+v", o)
	}
}

func TestBugreportFileCommentsOnADuplicate(t *testing.T) {
	dir := trProject(t, gateConfig(t, "", "off", nil))
	addRecord(t, dir)
	f := &forge{answer: func(string, []string) (string, string, int) {
		return "https://github.com/l4ci/rota/issues/7#issuecomment-1\n", "", 0
	}}
	d := useForge(t, f)
	t.Setenv("ROTA_UPSTREAM_REPO", "")
	o := trRunWith(t, d, dir, "", append([]string{"bugreport", "file", "bug-1", "--comment", "7"}, brOK...)...)
	if o.code != 0 {
		t.Fatalf("%+v", o)
	}
	if len(f.calls) != 2 || f.calls[1] != "gh issue comment 7 -R l4ci/rota -F -" {
		t.Fatalf("calls %q", f.calls)
	}
	recs, _ := bugreport.Load(context.Background(), git.Exec, dir)
	if recs[0].Status != bugreport.StatusFiled || !strings.Contains(recs[0].Issue, "issues/7") {
		t.Errorf("record %+v", recs[0])
	}
}

func TestBugreportDuplicatesSearchesOpenIssues(t *testing.T) {
	dir := trProject(t, "{}")
	addRecord(t, dir)
	f := &forge{answer: func(_ string, args []string) (string, string, int) {
		if args[0] == "issue" {
			return `[{"number":7,"title":"worker done refuses","url":"https://github.com/l4ci/rota/issues/7"}]`, "", 0
		}
		return "", "", 0
	}}
	d := useForge(t, f)
	t.Setenv("ROTA_UPSTREAM_REPO", "")
	o := trRunWith(t, d, dir, "", "bugreport", "duplicates", "bug-1")
	if o.code != 0 || !strings.Contains(o.stdout, "#7") {
		t.Fatalf("%+v", o)
	}
	if len(f.calls) != 2 || !strings.HasPrefix(f.calls[1], "gh issue list -R l4ci/rota --state open --search ") {
		t.Fatalf("calls %q", f.calls)
	}
	if strings.Contains(f.calls[1], "me@example.com") {
		t.Errorf("search query leaks: %q", f.calls[1])
	}
}

func TestBugreportDismiss(t *testing.T) {
	dir := trProject(t, "{}")
	addRecord(t, dir)
	if o := trRun(t, dir, "", "bugreport", "dismiss", "bug-1"); o.code != 0 {
		t.Fatalf("%+v", o)
	}
	if open, _ := bugreport.Open(context.Background(), git.Exec, dir); len(open) != 0 {
		t.Errorf("still open: %+v", open)
	}
}

func recordingDeps() *Deps {
	d := testDeps()
	d.RecordMistakes = true
	return d
}

// chdirForTest enters dir and restores the old directory when the test ends, even on t.Fatalf.
// t.Chdir needs go 1.24; go.mod is at 1.22.
func chdirForTest(t *testing.T, dir string) {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(wd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
}

func TestInternalErrorsAndPanicsAreRecorded(t *testing.T) {
	for _, verb := range []string{"plain", "panic"} {
		dir := trProject(t, "{}")
		chdirForTest(t, dir)
		var so, se bytes.Buffer
		code := run(testTree(), recordingDeps(), []string{"fail", verb}, strings.NewReader(""), &so, &se)
		if code != 70 {
			t.Fatalf("%s: exit %d", verb, code)
		}
		recs, _ := bugreport.Load(context.Background(), git.Exec, dir)
		if len(recs) != 1 || recs[0].Source != bugreport.SourceAuto || recs[0].Verb != "fail" || recs[0].Exit != 70 ||
			!reflectArgs(recs[0].Args, verb) || recs[0].Error == "" {
			t.Errorf("%s: records %+v", verb, recs)
		}
	}
}

func TestRecordedArgsDropGlobalFlags(t *testing.T) {
	dir := trProject(t, "{}")
	chdirForTest(t, ".") // -C changes the process directory; restore it
	var so, se bytes.Buffer
	code := run(testTree(), recordingDeps(), []string{"--json", "-C", dir, "fail", "plain", "--json", "--cwd", dir}, strings.NewReader(""), &so, &se)
	if code != 70 {
		t.Fatalf("exit %d", code)
	}
	recs, _ := bugreport.Load(context.Background(), git.Exec, dir)
	if len(recs) != 1 || !reflectArgs(recs[0].Args, "plain") {
		t.Errorf("records %+v", recs)
	}
}

func reflectArgs(got []string, want ...string) bool {
	return strings.Join(got, " ") == strings.Join(want, " ")
}

func TestOtherExitCodesAreNotRecorded(t *testing.T) {
	dir := trProject(t, "{}")
	chdirForTest(t, dir)
	var so, se bytes.Buffer
	for _, verb := range []string{"refused", "lock", "leak"} {
		run(testTree(), recordingDeps(), []string{"fail", verb}, strings.NewReader(""), &so, &se)
	}
	if recs, _ := bugreport.Load(context.Background(), git.Exec, dir); len(recs) != 0 {
		t.Errorf("recorded %+v", recs)
	}
}

func TestWindDownOffersOpenRecords(t *testing.T) {
	if got := bugreportOffer(nil); got != "" {
		t.Errorf("offer for nothing: %q", got)
	}
	got := bugreportOffer([]bugreport.Record{{ID: "bug-1"}, {ID: "bug-2"}})
	if !strings.Contains(got, "2 rota mistakes") || !strings.Contains(got, "/rota-bugreport") {
		t.Errorf("offer %q", got)
	}
}

func TestHookStopOffersOncePerSession(t *testing.T) {
	t.Setenv("ROTA_SLOT", "")
	dir := orchProject(t, false)
	if code, out, _ := rotaStdin(t, dir, stopPayload(dir, false), "hook", "stop"); code != 0 || out != "" {
		t.Fatalf("no records: %d %q", code, out)
	}
	if _, err := bugreport.Add(context.Background(), git.Exec, dir, bugreport.Record{Source: bugreport.SourceAgent, Summary: "wrong refusal"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	code, out, _ := rotaStdin(t, dir, stopPayload(dir, false), "hook", "stop")
	var m map[string]string
	if err := json.Unmarshal([]byte(out), &m); code != 0 || err != nil || !strings.Contains(m["systemMessage"], "/rota-bugreport") || m["decision"] != "" {
		t.Fatalf("offer: %d %q %v", code, out, err)
	}
	if code, out, _ := rotaStdin(t, dir, stopPayload(dir, false), "hook", "stop"); code != 0 || out != "" {
		t.Errorf("offered twice in one session: %d %q", code, out)
	}
	// nobody answered: the next session offers it again
	next := strings.Replace(stopPayload(dir, false), `"s1"`, `"s2"`, 1)
	if _, out, _ := rotaStdin(t, dir, next, "hook", "stop"); !strings.Contains(out, "/rota-bugreport") {
		t.Errorf("a new session did not re-offer: %q", out)
	}
	if open, _ := bugreport.Open(context.Background(), git.Exec, dir); len(open) != 1 {
		t.Errorf("the offer closed the record: %+v", open)
	}
}

func TestHookStopSkipsTheOfferForARoundWorker(t *testing.T) {
	dir := orchProject(t, false)
	bugreport.Add(context.Background(), git.Exec, dir, bugreport.Record{Source: bugreport.SourceAgent, Summary: "x"}, time.Now())
	t.Setenv("ROTA_SLOT", "dana")
	if code, out, _ := rotaStdin(t, dir, stopPayload(dir, false), "hook", "stop"); code != 0 || out != "" {
		t.Errorf("%d %q", code, out)
	}
}

func TestBugreportListRedactsLikeDraft(t *testing.T) {
	dir := trProject(t, "{}")
	if _, err := bugreport.Add(context.Background(), git.Exec, dir, bugreport.Record{Source: bugreport.SourceAgent, Verb: "worker done",
		Summary: "broke in " + filepath.Base(dir)}, time.Now()); err != nil {
		t.Fatal(err)
	}
	o := trRun(t, dir, "", "bugreport", "list")
	if o.code != 0 || strings.Contains(o.stdout, filepath.Base(dir)) || !strings.Contains(o.stdout, "<repo>") {
		t.Errorf("list leaks the repo name: %+v", o)
	}
	j := trRun(t, dir, "", "--json", "bugreport", "list")
	if strings.Contains(j.stdout, filepath.Base(dir)) {
		t.Errorf("json list leaks the repo name: %+v", j)
	}
}
