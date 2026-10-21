package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/bugreport"
	"github.com/l4ci/rota/internal/gate"
)

var brOK = []string{"--confirm", "--confirm-note", "yes, file it"}

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
	recs, _ := bugreport.Load(dir)
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
		if open, _ := bugreport.Open(dir); len(open) != 1 {
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
	recs, _ := bugreport.Load(dir)
	if recs[0].Status != bugreport.StatusFiled || recs[0].Issue != "https://github.com/l4ci/rota/issues/9" {
		t.Errorf("record %+v", recs[0])
	}
	audit := gateAudit(t, dir)
	if len(audit) != 1 || audit[0]["gate"] != gate.BugreportFile || audit[0]["note"] != "yes, file it" {
		t.Errorf("audit %+v", audit)
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
	recs, _ := bugreport.Load(dir)
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
	if open, _ := bugreport.Open(dir); len(open) != 0 {
		t.Errorf("still open: %+v", open)
	}
}

func recordingDeps() *Deps {
	d := testDeps()
	d.RecordMistakes = true
	return d
}

func TestInternalErrorsAndPanicsAreRecorded(t *testing.T) {
	for _, verb := range []string{"plain", "panic"} {
		dir := trProject(t, "{}")
		wd, _ := os.Getwd()
		os.Chdir(dir)
		var so, se bytes.Buffer
		code := run(testTree(), recordingDeps(), []string{"fail", verb}, strings.NewReader(""), &so, &se)
		os.Chdir(wd)
		if code != 70 {
			t.Fatalf("%s: exit %d", verb, code)
		}
		recs, _ := bugreport.Load(dir)
		if len(recs) != 1 || recs[0].Source != bugreport.SourceAuto || recs[0].Verb != "fail" || recs[0].Exit != 70 ||
			!reflectArgs(recs[0].Args, verb) || recs[0].Error == "" {
			t.Errorf("%s: records %+v", verb, recs)
		}
	}
}

func reflectArgs(got []string, want ...string) bool {
	return strings.Join(got, " ") == strings.Join(want, " ")
}

func TestOtherExitCodesAreNotRecorded(t *testing.T) {
	dir := trProject(t, "{}")
	wd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(wd)
	var so, se bytes.Buffer
	for _, verb := range []string{"refused", "lock", "leak"} {
		run(testTree(), recordingDeps(), []string{"fail", verb}, strings.NewReader(""), &so, &se)
	}
	if recs, _ := bugreport.Load(dir); len(recs) != 0 {
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

func TestHookStopOffersNewRecordsOnce(t *testing.T) {
	t.Setenv("ROTA_SLOT", "")
	dir := orchProject(t, false)
	if code, out, _ := rotaStdin(t, dir, stopPayload(dir, false), "hook", "stop"); code != 0 || out != "" {
		t.Fatalf("no records: %d %q", code, out)
	}
	if _, err := bugreport.Add(dir, bugreport.Record{Source: bugreport.SourceAgent, Summary: "wrong refusal"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	code, out, _ := rotaStdin(t, dir, stopPayload(dir, false), "hook", "stop")
	var m map[string]string
	if err := json.Unmarshal([]byte(out), &m); code != 0 || err != nil || !strings.Contains(m["systemMessage"], "/rota-bugreport") || m["decision"] != "" {
		t.Fatalf("offer: %d %q %v", code, out, err)
	}
	if code, out, _ := rotaStdin(t, dir, stopPayload(dir, false), "hook", "stop"); code != 0 || out != "" {
		t.Errorf("offered twice: %d %q", code, out)
	}
	if open, _ := bugreport.Open(dir); len(open) != 1 {
		t.Errorf("the offer closed the record: %+v", open)
	}
}

func TestHookStopSkipsTheOfferForARoundWorker(t *testing.T) {
	dir := orchProject(t, false)
	bugreport.Add(dir, bugreport.Record{Source: bugreport.SourceAgent, Summary: "x"}, time.Now())
	t.Setenv("ROTA_SLOT", "dana")
	if code, out, _ := rotaStdin(t, dir, stopPayload(dir, false), "hook", "stop"); code != 0 || out != "" {
		t.Errorf("%d %q", code, out)
	}
}
