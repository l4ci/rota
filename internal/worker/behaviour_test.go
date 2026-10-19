package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/tracker"
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
	w.forge("issueBody", body)
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
	w.setConfig(`{"issues":{"source":"file"},"test":{"full":["true"]}}`)
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

// #899 trust: steps run only when the issue's author and last editor can push.

const tryTouch = "## Try it\n\n- touch ran.txt && echo ran\n"

func ranMarker(w *world) bool { return exists(filepath.Join(w.dir, "ran.txt")) }

func TestGateBehaviourTrustSkips(t *testing.T) {
	for name, tc := range map[string]struct {
		set  map[string]string
		want string
	}{
		"author": {map[string]string{"author": "mallory", "readers": "mallory"},
			"BEHAVIOUR-SKIP w1 — author mallory of #899 is not a collaborator with write access, so its Try it steps did not run"},
		"last editor": {map[string]string{"editor": "eve", "readers": "eve"},
			"BEHAVIOUR-SKIP w1 — last editor eve of #899 is not a collaborator with write access, so its Try it steps did not run"},
		"unknown editor": {map[string]string{"edited": "1"},
			"BEHAVIOUR-SKIP w1 — the last editor of #899 is unknown, so its Try it steps did not run"},
		"unknown author": {map[string]string{"authorUnknown": "1"},
			"BEHAVIOUR-SKIP w1 — the author of #899 is unknown, so its Try it steps did not run"},
	} {
		t.Run(name, func(t *testing.T) {
			w, gate := behaviourWorld(t, tryTouch)
			for k, v := range tc.set {
				w.forge(k, v)
			}
			res, err := gate(GateOpts{})
			if err != nil || res.Verdict != GatePass || !hasNote(res, tc.want) {
				t.Fatalf("%+v %v\nwant note %q", res, err, tc.want)
			}
			if ranMarker(w) || strings.Contains(w.logText(), "AddMRNote") {
				t.Error("a step ran or a comment posted")
			}
			if _, ok := verdict.Latest(verdict.Load(w.dir).Branches["w1"], verdict.Behaviour); ok {
				t.Error("a behaviour verdict was recorded")
			}
			if _, err := os.Stat(filepath.Join(w.dir, ".rota", "qa-runs", "behaviour")); err == nil {
				t.Error("a transcript was written")
			}
		})
	}
}

func TestGateBehaviourTrustedAuthorAndEditorRun(t *testing.T) {
	w, gate := behaviourWorld(t, tryTouch)
	w.forge("editor", "bob")
	res, err := gate(GateOpts{})
	if err != nil || res.Verdict != GatePass || !hasNote(res, "BEHAVIOUR w1 — PASS") {
		t.Fatalf("%+v %v", res, err)
	}
	log := w.logText()
	if !strings.Contains(log, "CanPush alice") || !strings.Contains(log, "CanPush bob") {
		t.Errorf("both were not checked:\n%s", log)
	}
}

func TestGateBehaviourSameEditorIsCheckedOnce(t *testing.T) {
	w, gate := behaviourWorld(t, tryTouch)
	w.forge("editor", "alice")
	if _, err := gate(GateOpts{}); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(w.logText(), "CanPush alice"); n != 1 {
		t.Errorf("CanPush alice asked %d times", n)
	}
}

func TestGateBehaviourTrustReadFailureBreaksTheCheck(t *testing.T) {
	for name, word := range map[string]string{"editors": "editorsErr", "canpush": "canPushErr"} {
		t.Run(name, func(t *testing.T) {
			w, gate := behaviourWorld(t, tryTouch)
			w.forge(word, "forge down")
			res, err := gate(GateOpts{})
			if err != nil || res.Verdict != GateCheckBroke || !strings.Contains(res.Err, "cannot read who wrote #899 to trust its ## Try it steps") || !strings.Contains(res.Err, "forge down") {
				t.Fatalf("%+v %v", res, err)
			}
			if ranMarker(w) {
				t.Error("a step ran")
			}
		})
	}
}

// bareIssues is an issue source that cannot say who wrote an issue.
type bareIssues struct{ f *fakeForge }

func (b bareIssues) Get(ctx context.Context, k tracker.IssueKey, c bool) (tracker.Issue, error) {
	return b.f.Get(ctx, k, c)
}
func (b bareIssues) RemoveLabels(ctx context.Context, k tracker.IssueKey, l []string) error {
	return b.f.RemoveLabels(ctx, k, l)
}
func (b bareIssues) ClosesIssue(k tracker.IssueKey, c tracker.ClosingSource) bool {
	return b.f.ClosesIssue(k, c)
}

func TestGateBehaviourSourceWithoutTrustSkips(t *testing.T) {
	w, _ := behaviourWorld(t, tryTouch)
	w.setConfig(`{"issues":{"source":"jira"},"test":{"full":["true"]}}`)
	e := w.env(false)
	e.Issues = func(string, tracker.Settings) (Issues, error) {
		return bareIssues{&fakeForge{w: w, provider: "jira"}}, nil
	}
	o := GateOpts{Slot: "w1", Base: "main", ItemBody: func(string) (string, bool, error) { return tryTouch, true, nil }}
	res, err := e.Gate(bg, w.dir, o)
	if err != nil || res.Verdict != GatePass ||
		!hasNote(res, "BEHAVIOUR-SKIP w1 — the jira issue source cannot say whether #899's author is a collaborator, so its Try it steps did not run") {
		t.Fatalf("%+v %v", res, err)
	}
	if ranMarker(w) {
		t.Error("a step ran")
	}
}

func TestGateBehaviourFileSourceRunsUnchecked(t *testing.T) {
	w, gate := behaviourWorld(t, tryTouch)
	w.setConfig(`{"issues":{"source":"file"},"test":{"full":["true"]}}`)
	res, err := gate(GateOpts{})
	if err != nil || res.Verdict != GatePass || !hasNote(res, "BEHAVIOUR w1 — PASS") {
		t.Fatalf("%+v %v", res, err)
	}
	if log := w.logText(); strings.Contains(log, "IssueAuthorship") || strings.Contains(log, "CanPush") {
		t.Errorf("trust was asked on a file source:\n%s", log)
	}
}

func TestGateBehaviourStepEnvironIsScrubbed(t *testing.T) {
	w, _ := behaviourWorld(t, "## Try it\n\n- echo hi\n")
	var got []string
	var homeDuring bool
	e := w.env(false)
	e.Getenv = func(k string) string {
		return map[string]string{"ROTA_GATE_SHA_WAIT": "0", "PATH": "/p", "HOME": "/h-real", "GH_TOKEN": "secret", "XDG_CONFIG_HOME": "/x"}[k]
	}
	e.Run = func(_ context.Context, name string, args, _ []string) (host.Result, error) {
		if name != "go" || !slices.Equal(args, []string{"env", "GOCACHE", "GOMODCACHE", "GOPATH"}) {
			return host.Result{ExitCode: 1}, nil
		}
		return host.Result{Stdout: "/gc\n\n/gp\n"}, nil // GOMODCACHE is empty: left out
	}
	e.ShellEnv = func(_ context.Context, _, _ string, environ []string) (string, int) {
		got = append([]string(nil), environ...)
		for _, kv := range environ {
			if h, ok := strings.CutPrefix(kv, "HOME="); ok {
				_, err := os.Stat(h)
				homeDuring = err == nil
			}
		}
		return "hi\n", 0
	}
	o := GateOpts{Slot: "w1", Base: "main", ItemBody: func(string) (string, bool, error) { return "", false, nil }}
	if res, err := e.Gate(bg, w.dir, o); err != nil || res.Verdict != GatePass {
		t.Fatalf("%+v %v", res, err)
	}
	env := map[string]string{}
	for _, kv := range got {
		k, v, _ := strings.Cut(kv, "=")
		env[k] = v
	}
	home := env["HOME"]
	parent := filepath.Join(w.dir, ".rota", "qa-runs", "behaviour") + string(filepath.Separator)
	if home == "/h-real" || !strings.HasPrefix(home, parent+"home-") {
		t.Fatalf("HOME %q is not a temp dir under %s", home, parent)
	}
	if !homeDuring {
		t.Error("the HOME did not exist while the step ran")
	}
	if exists(home) {
		t.Error("the HOME outlived the gate")
	}
	for k, want := range map[string]string{"XDG_CONFIG_HOME": home + "/.config", "XDG_DATA_HOME": home + "/.local/share", "XDG_CACHE_HOME": home + "/.cache", "GH_CONFIG_DIR": home + "/.config/gh"} {
		if env[k] != want {
			t.Errorf("%s=%q, want %q", k, env[k], want)
		}
	}
	slices.Sort(got)
	var names []string
	for _, kv := range got {
		names = append(names, strings.SplitN(kv, "=", 2)[0])
	}
	want := []string{"GH_CONFIG_DIR", "GOCACHE", "GOPATH", "HOME", "PATH", "ROTA_DB_SUFFIX", "ROTA_PORT_BASE", "ROTA_PORT_BLOCK", "XDG_CACHE_HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME"}
	if !slices.Equal(names, want) || env["PATH"] != "/p" || env["GOCACHE"] != "/gc" || env["GOPATH"] != "/gp" || env["ROTA_PORT_BASE"] != "20100" {
		t.Errorf("environ %q, want names %q", got, want)
	}
}

func TestGateBehaviourHomeGoneAfterAFailedStep(t *testing.T) {
	w, gate := behaviourWorld(t, "## Try it\n\n- touch \"$HOME/mark\"; exit 4\n")
	if res, err := gate(GateOpts{}); err != nil || res.Verdict != GateVerifyFailed {
		t.Fatalf("%+v %v", res, err)
	}
	ents, _ := filepath.Glob(filepath.Join(w.dir, ".rota", "qa-runs", "behaviour", "home-*"))
	if len(ents) != 0 {
		t.Errorf("homes left: %q", ents)
	}
}

func TestGateBehaviourStepSeesNoRealHome(t *testing.T) {
	w, gate := behaviourWorld(t, "## Try it\n\n- test \"$HOME\" != \"$REAL\" && ls -A \"$HOME\" | wc -l\n  expect: 0\n")
	t.Setenv("REAL", os.Getenv("HOME"))
	_ = w
	if res, err := gate(GateOpts{}); err != nil || res.Verdict != GatePass {
		t.Fatalf("%+v %v", res, err)
	}
}

// An untrusted issue whose Try it section is malformed skips; it must not fail
// the gate with the issue's text.
func TestGateBehaviourMalformedSectionOfAnUntrustedAuthorSkips(t *testing.T) {
	w, gate := behaviourWorld(t, "## Try it\n\n- echo a\n  expect:\n")
	w.forge("author", "mallory")
	w.forge("readers", "mallory")
	res, err := gate(GateOpts{})
	if err != nil || res.Verdict != GatePass || !hasNote(res, "author mallory of #899 is not a collaborator") {
		t.Fatalf("%+v %v", res, err)
	}
}

// The steps that run are the body the trust check read, not what ItemBody says.
func TestGateBehaviourRunsTheVouchedBody(t *testing.T) {
	w, _ := behaviourWorld(t, tryTouch)
	o := GateOpts{Slot: "w1", Base: "main", ItemBody: func(string) (string, bool, error) {
		return "## Try it\n\n- touch evil.txt\n", true, nil
	}}
	if res, err := w.env(false).Gate(bg, w.dir, o); err != nil || res.Verdict != GatePass {
		t.Fatalf("%+v %v", res, err)
	}
	tr, _ := os.ReadFile(filepath.Join(w.dir, behaviourRecord(t, w).Transcript))
	if !strings.Contains(string(tr), "$ touch ran.txt") || strings.Contains(string(tr), "evil") {
		t.Errorf("ran the ItemBody text instead of the vouched body:\n%s", tr)
	}
}

func TestGateBehaviourNoSectionAsksNoCollaboratorQuestion(t *testing.T) {
	w, gate := behaviourWorld(t, "## Goal\n\nx\n")
	res, err := gate(GateOpts{})
	if err != nil || res.Verdict != GatePass || !hasNote(res, "BEHAVIOUR-SKIP w1 — #899 has no ## Try it section; no behaviour run") {
		t.Fatalf("%+v %v", res, err)
	}
	if strings.Contains(w.logText(), "CanPush") {
		t.Errorf("CanPush asked:\n%s", w.logText())
	}
}

// A gate that reads no origin refs reads no forge (#817): a forge source skips
// before any issue read; a file source still runs.
func TestGateBehaviourLocalGateReadsNoForge(t *testing.T) {
	local := func(t *testing.T) *world {
		w, _ := behaviourWorld(t, tryTouch)
		gitq(t, w.dir, "fetch", "-q", "origin", "w1:w1")
		os.WriteFile(filepath.Join(w.dir, ".rota", "workers.json"),
			[]byte(`{"slots":[{"name":"w1","branch":"w1","task":"899","portBase":20100,"portBlock":100}]}`), 0o644)
		return w
	}
	o := GateOpts{ItemBody: func(string) (string, bool, error) { return tryTouch, true, nil }}
	t.Run("forge source skips", func(t *testing.T) {
		w := local(t)
		res, err := w.gate(false, o)
		if err != nil || res.Verdict != GatePass ||
			!hasNote(res, "BEHAVIOUR-SKIP w1 — the gate reads no origin refs (no PR), so it reads no forge for #899's Try it steps") {
			t.Fatalf("%+v %v", res, err)
		}
		if log := w.logText(); strings.Contains(log, "IssueAuthorship") || strings.Contains(log, "CanPush") || strings.Contains(log, "Get ") {
			t.Errorf("the forge was read:\n%s", log)
		}
		if _, ok := verdict.Latest(verdict.Load(w.dir).Branches["w1"], verdict.Behaviour); ok {
			t.Error("a behaviour verdict was recorded")
		}
	})
	t.Run("file source runs", func(t *testing.T) {
		w := local(t)
		w.setConfig(`{"issues":{"source":"file"},"test":{"full":["true"]}}`)
		res, err := w.gate(false, o)
		if err != nil || res.Verdict != GatePass || !hasNote(res, "BEHAVIOUR w1 — PASS") {
			t.Fatalf("%+v %v", res, err)
		}
	})
}

func TestGateBehaviourTruncatedOutputShowsInTheTranscript(t *testing.T) {
	w, _ := behaviourWorld(t, "## Try it\n\n- echo big\n")
	e := w.env(false)
	e.ShellEnv = func(context.Context, string, string, []string) (string, int) {
		return "xxxx\n" + outputTruncatedMarker + "\n", 0
	}
	o := GateOpts{Slot: "w1", Base: "main", ItemBody: func(string) (string, bool, error) { return "", false, nil }}
	if res, err := e.Gate(bg, w.dir, o); err != nil || res.Verdict != GatePass {
		t.Fatalf("%+v %v", res, err)
	}
	tr, _ := os.ReadFile(filepath.Join(w.dir, behaviourRecord(t, w).Transcript))
	if !strings.Contains(string(tr), "[output truncated at 4 MiB]") {
		t.Errorf("transcript:\n%s", tr)
	}
}

func TestExecShellEnvCapsOutput(t *testing.T) {
	out, code := execShellEnv(bg, t.TempDir(), `head -c 5000000 /dev/zero | tr '\0' x`, nil)
	if code != 0 || len(out) > behaviourMaxOutput+100 || !strings.HasSuffix(out, "\n"+outputTruncatedMarker+"\n") {
		t.Errorf("%d %d %q", code, len(out), out[max(0, len(out)-60):])
	}
	out, _ = execShellEnv(bg, t.TempDir(), `echo hi`, nil)
	if out != "hi\n" {
		t.Errorf("%q", out)
	}
}

type stubTrust struct {
	a     tracker.Authorship
	err   error
	asked []string
}

func (s *stubTrust) IssueAuthorship(context.Context, tracker.IssueKey) (tracker.Authorship, error) {
	s.asked = append(s.asked, "authorship")
	return s.a, s.err
}
func (s *stubTrust) CanPush(_ context.Context, l string) (bool, error) {
	s.asked = append(s.asked, "canpush "+l)
	return l != "eve", nil
}

func TestTrustedTryIt(t *testing.T) {
	const body = "## Try it\n- echo a\n"
	file := func() (string, bool, error) { return body, true, nil }
	for name, tc := range map[string]struct {
		s          TryItSource
		wantBody   string
		wantSkip   string
		wantErr    string
		wantAsked  []string
		wantNoSect bool
	}{
		"file":            {s: TryItSource{Source: "file", Issue: "B07", FileBody: file}, wantBody: body},
		"empty is file":   {s: TryItSource{Issue: "B07", FileBody: file}, wantBody: body},
		"file not found":  {s: TryItSource{Source: "file", Issue: "9", FileBody: func() (string, bool, error) { return "", false, nil }}, wantSkip: "#9 is not in the backlog"},
		"file read fails": {s: TryItSource{Source: "file", Issue: "9", FileBody: func() (string, bool, error) { return "", false, errors.New("io") }}, wantErr: "cannot read #9 to look for its ## Try it steps: io"},
		"file no section": {s: TryItSource{Source: "file", Issue: "9", FileBody: func() (string, bool, error) { return "## Goal\n", true, nil }}, wantSkip: "#9 has no ## Try it section", wantNoSect: true},
		"repo-qualified":  {s: TryItSource{Source: "github", Issue: "o/r#5", Trust: &stubTrust{}}, wantSkip: "o/r#5 is a repo-qualified key, and the trust check reads only the root repo"},
		"no trust":        {s: TryItSource{Source: "jira", Issue: "9"}, wantSkip: "the jira issue source cannot say whether #9's author is a collaborator"},
		"read fails":      {s: TryItSource{Source: "github", Issue: "9", Trust: &stubTrust{err: errors.New("down")}}, wantErr: "cannot read who wrote #9 to trust its ## Try it steps: down", wantAsked: []string{"authorship"}},
		"no section":      {s: TryItSource{Source: "github", Issue: "9", Trust: &stubTrust{a: tracker.Authorship{Author: "eve", Body: "x"}}}, wantSkip: "#9 has no ## Try it section", wantNoSect: true, wantAsked: []string{"authorship"}},
		"trusted":         {s: TryItSource{Source: "github", Issue: "9", Trust: &stubTrust{a: tracker.Authorship{Author: "ann", Body: body}}}, wantBody: body, wantAsked: []string{"authorship", "canpush ann"}},
		"editor":          {s: TryItSource{Source: "github", Issue: "9", Trust: &stubTrust{a: tracker.Authorship{Author: "ann", Editor: "eve", Edited: true, Body: body}}}, wantSkip: "last editor eve of #9 is not a collaborator with write access", wantAsked: []string{"authorship", "canpush ann", "canpush eve"}},
		"unknown author":  {s: TryItSource{Source: "github", Issue: "9", Trust: &stubTrust{a: tracker.Authorship{Body: body}}}, wantSkip: "the author of #9 is unknown", wantAsked: []string{"authorship"}},
		"unknown editor":  {s: TryItSource{Source: "github", Issue: "9", Trust: &stubTrust{a: tracker.Authorship{Author: "ann", Edited: true, Body: body}}}, wantSkip: "the last editor of #9 is unknown", wantAsked: []string{"authorship"}},
	} {
		t.Run(name, func(t *testing.T) {
			b, skip, err := TrustedTryIt(bg, tc.s)
			if skip != tc.wantSkip || b != tc.wantBody || NoTryIt(skip) != tc.wantNoSect || (err != nil) != (tc.wantErr != "") || (err != nil && err.Error() != tc.wantErr) {
				t.Errorf("body %q skip %q err %v", b, skip, err)
			}
			if st, ok := tc.s.Trust.(*stubTrust); ok && !slices.Equal(st.asked, tc.wantAsked) {
				t.Errorf("asked %q, want %q", st.asked, tc.wantAsked)
			}
		})
	}
}

func TestExecShellEnvHidesTheCallersEnvironment(t *testing.T) {
	t.Setenv("GH_TOKEN", "secret")
	out, code := execShellEnv(bg, t.TempDir(), `echo "tok=${GH_TOKEN-unset} v=${KEEP-unset}"`, []string{"KEEP=1"})
	if code != 0 || strings.TrimSpace(out) != "tok=unset v=1" {
		t.Errorf("%d %q", code, out)
	}
	out, _ = execShellEnv(bg, t.TempDir(), `echo "tok=${GH_TOKEN-unset}"`, nil)
	if strings.TrimSpace(out) != "tok=unset" {
		t.Errorf("nil environ leaked: %q", out)
	}
}
