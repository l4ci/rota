package worker

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/verdict"
)

// #899: after test.full and test.e2e pass on the scratch merge, the gate runs
// the item's `## Try it` steps there, records a behaviour verdict and posts the
// transcript on the PR.

// behaviourWorld is a world whose slot w1 holds item 899 with a port block,
// and whose gate reads body as the item's Try it text.
func behaviourWorld(t *testing.T, body string) (*world, func(GateOpts) (GateResult, error)) {
	t.Helper()
	w := newWorld(t, ghURL)
	w.forge("body", "Closes #899")
	os.WriteFile(filepath.Join(w.dir, ".rota", "workers.json"),
		[]byte(fmt.Sprintf(`{"slots":[{"name":"w1","branch":"w1","pr":%q,"task":"899","portBase":20100,"portBlock":100}]}`, ghURL)), 0o644)
	return w, func(o GateOpts) (GateResult, error) {
		o.ItemBody = func(issue string) (string, bool, error) {
			if issue != "899" {
				return "", false, fmt.Errorf("asked for %q", issue)
			}
			return body, true, nil
		}
		return w.gate(false, o)
	}
}

func hasNote(res GateResult, sub string) bool {
	for _, n := range res.Notes {
		if strings.Contains(n, sub) {
			return true
		}
	}
	return false
}

func behaviourRecord(t *testing.T, w *world) verdict.Record {
	t.Helper()
	rec, ok := verdict.Latest(verdict.Load(w.dir).Branches["w1"], verdict.Behaviour)
	if !ok {
		t.Fatal("no behaviour verdict recorded for w1")
	}
	return rec
}

func TestGateBehaviourPassRecordsAndComments(t *testing.T) {
	w, gate := behaviourWorld(t, "## Try it\n\n- test -f work.txt && echo \"port $ROTA_PORT_BASE $ROTA_PORT_BLOCK $ROTA_DB_SUFFIX\"\n  expect: port 20100 100 _w1\n- echo second\n")
	res, err := gate(GateOpts{})
	if err != nil || res.Verdict != GatePass || !res.Changed {
		t.Fatalf("%+v %v", res, err)
	}
	if !hasNote(res, "BEHAVIOUR w1 — PASS, 2 Try it step(s) of #899") {
		t.Errorf("notes %q", res.Notes)
	}
	rec := behaviourRecord(t, w)
	head := w.forgeWord("sha")
	if rec.Verdict != "PASS" || !verdict.AtHead(rec, head) || rec.Branch != "w1" || rec.Summary != "2 step(s) passed" {
		t.Errorf("record %+v", rec)
	}
	if !strings.HasPrefix(rec.Transcript, ".rota/qa-runs/behaviour/w1-"+head[:7]) {
		t.Errorf("transcript path %q", rec.Transcript)
	}
	tr, err := os.ReadFile(filepath.Join(w.dir, rec.Transcript))
	if err != nil || !strings.Contains(string(tr), "port 20100 100 _w1") || !strings.Contains(string(tr), `expect "port 20100 100 _w1": found`) {
		t.Errorf("transcript %v:\n%s", err, tr)
	}
	note := w.forgeWord("note")
	if !strings.HasPrefix(note, "### Behaviour run: PASS at "+head[:7]+"\n2 step(s) from the ## Try it section of #899") || !strings.Contains(note, "```text\n") {
		t.Errorf("comment:\n%s", note)
	}
	if !strings.Contains(w.logText(), "AddMRNote 7") || !w.onOriginMain("work.txt") {
		t.Errorf("not commented or not merged:\n%s", w.logText())
	}
}

func TestGateBehaviourNonZeroExitLandsNothing(t *testing.T) {
	w, gate := behaviourWorld(t, "## Try it\n\n- echo ok\n- echo boom; exit 3\n- echo never\n")
	res, err := gate(GateOpts{})
	if err != nil || res.Verdict != GateVerifyFailed || res.Changed {
		t.Fatalf("%+v %v", res, err)
	}
	for _, want := range []string{"fails Try it step 2 (issue line 4) `echo boom; exit 3`: exit 3; nothing landed", "    boom", "full transcript: .rota/qa-runs/behaviour/"} {
		if !strings.Contains(res.Err, want) {
			t.Errorf("Err lacks %q:\n%s", want, res.Err)
		}
	}
	if !strings.Contains(res.Hint, "send w1 back") || w.onOriginMain("work.txt") || strings.Contains(w.logText(), "PRRequestMerge") {
		t.Errorf("landed or wrong hint %q:\n%s", res.Hint, w.logText())
	}
	rec := behaviourRecord(t, w)
	if rec.Verdict != "FAIL" || rec.Summary != "step 2 `echo boom; exit 3`: exit 3" {
		t.Errorf("record %+v", rec)
	}
	tr, _ := os.ReadFile(filepath.Join(w.dir, rec.Transcript))
	if !strings.Contains(string(tr), "[exit 3]") || !strings.Contains(string(tr), "(not run: an earlier step failed)") {
		t.Errorf("transcript:\n%s", tr)
	}
	if !strings.Contains(w.forgeWord("note"), "### Behaviour run: FAIL at ") {
		t.Errorf("comment %q", w.forgeWord("note"))
	}
}

func TestGateBehaviourMissingExpectFails(t *testing.T) {
	w, gate := behaviourWorld(t, "## Try it\n\n- echo hello\n  expect: hello\n  expect: goodbye\n")
	res, err := gate(GateOpts{})
	if err != nil || res.Verdict != GateVerifyFailed || !strings.Contains(res.Err, `missing expected line "goodbye"`) {
		t.Fatalf("%+v %v", res, err)
	}
	if rec := behaviourRecord(t, w); rec.Verdict != "FAIL" || !strings.Contains(rec.Summary, `missing expected line "goodbye"`) {
		t.Errorf("record %+v", rec)
	}
	tr, _ := os.ReadFile(filepath.Join(w.dir, behaviourRecord(t, w).Transcript))
	if !strings.Contains(string(tr), `expect "goodbye": MISSING`) {
		t.Errorf("transcript:\n%s", tr)
	}
}

func TestGateBehaviourNoSectionSkips(t *testing.T) {
	w, gate := behaviourWorld(t, "## Acceptance\n\n- [ ] x\n")
	res, err := gate(GateOpts{})
	if err != nil || res.Verdict != GatePass {
		t.Fatalf("%+v %v", res, err)
	}
	n := 0
	for _, note := range res.Notes {
		if strings.HasPrefix(note, "BEHAVIOUR-SKIP w1 — #899 has no ## Try it section; no behaviour run") {
			n++
		}
	}
	if n != 1 || strings.Contains(w.logText(), "AddMRNote") {
		t.Errorf("want one skip note and no comment, notes %q", res.Notes)
	}
	if _, ok := verdict.Latest(verdict.Load(w.dir).Branches["w1"], verdict.Behaviour); ok {
		t.Error("a behaviour verdict was recorded")
	}
}

func TestGateBehaviourEmptySectionSkips(t *testing.T) {
	_, gate := behaviourWorld(t, "## Try it\n\nnothing yet\n")
	res, err := gate(GateOpts{})
	if err != nil || res.Verdict != GatePass || !hasNote(res, "the ## Try it section of #899 lists no steps") {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestGateBehaviourMalformedSectionFails(t *testing.T) {
	w, gate := behaviourWorld(t, "## Try it\n\n- echo a\n  expect:\n")
	res, err := gate(GateOpts{})
	if err != nil || res.Verdict != GateVerifyFailed || res.Changed ||
		!strings.Contains(res.Err, "the ## Try it section of #899 is malformed:") || !strings.Contains(res.Err, "nothing landed") || res.Hint != "fix the issue body, then re-gate" {
		t.Fatalf("%+v %v", res, err)
	}
	if w.onOriginMain("work.txt") {
		t.Error("landed")
	}
}

func TestGateBehaviourUnreadableItemBreaksTheCheck(t *testing.T) {
	w, _ := behaviourWorld(t, "")
	res, err := w.gate(false, GateOpts{ItemBody: func(string) (string, bool, error) { return "", false, fmt.Errorf("backend down") }})
	if err != nil || res.Verdict != GateCheckBroke || !strings.Contains(res.Err, "#899") || !strings.Contains(res.Err, "backend down") {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestGateBehaviourNoIssueSkips(t *testing.T) {
	w := newWorld(t, ghURL)
	res, err := w.gate(false, GateOpts{ItemBody: func(string) (string, bool, error) { t.Error("read an item"); return "", false, nil }})
	if err != nil || res.Verdict != GatePass || !hasNote(res, "BEHAVIOUR-SKIP w1 — the gate cannot tell the item") {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestGateBehaviourLongTranscriptIsTruncated(t *testing.T) {
	w, gate := behaviourWorld(t, "## Try it\n\n- echo '```'; seq 1 500\n")
	res, err := gate(GateOpts{})
	if err != nil || res.Verdict != GatePass {
		t.Fatalf("%+v %v", res, err)
	}
	note := w.forgeWord("note")
	rec := behaviourRecord(t, w)
	if !strings.Contains(note, "Truncated; the full transcript is "+rec.Transcript+" on the gate host.") || strings.Contains(note, "\n300\n") {
		t.Errorf("comment not truncated:\n%s", note)
	}
	if !strings.Contains(note, "````text\n") {
		t.Errorf("fence not longer than the transcript's backticks:\n%.200s", note)
	}
	tr, _ := os.ReadFile(filepath.Join(w.dir, rec.Transcript))
	if !strings.Contains(string(tr), "\n500\n") {
		t.Error("the file transcript is cut")
	}
}

func TestGateBehaviourCommentFailureIsAWarning(t *testing.T) {
	w, gate := behaviourWorld(t, "## Try it\n\n- echo ok\n")
	w.forge("noteErr", "forge says no")
	res, err := gate(GateOpts{})
	if err != nil || res.Verdict != GatePass || len(res.Warnings) != 1 || !strings.HasPrefix(res.Warnings[0], "BEHAVIOUR-COMMENT") {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestGateBehaviourSkippedWhenNothingVerifies(t *testing.T) {
	for name, o := range map[string]GateOpts{"check-only": {CheckOnly: true}, "no-verify": {NoVerify: true}} {
		t.Run(name, func(t *testing.T) {
			w, gate := behaviourWorld(t, "## Try it\n\n- touch ran.txt\n")
			if _, err := gate(o); err != nil {
				t.Fatal(err)
			}
			if _, ok := verdict.Latest(verdict.Load(w.dir).Branches["w1"], verdict.Behaviour); ok || strings.Contains(w.logText(), "AddMRNote") {
				t.Error("a behaviour run happened")
			}
		})
	}
}
