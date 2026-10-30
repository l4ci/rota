package cli

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/receipt"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/tracker"
)

// fakeVerb counts its runs; fail, when set, is returned after the work.
type fakeVerb struct {
	runs  int
	fail  error
	stdin string // fed to the verb as c.Stdin
	heads func() []string
	// body is what the verb read from --body-file -.
	body string
	// verdict, when set, is carried in the failure's data like a gate verdict.
	verdict string
}

func (f *fakeVerb) verb(fs *flag.FlagSet) RunFunc {
	fs.String("agent", "", "")
	fs.Bool("check-only", false, "")
	fs.String("body-file", "", "")
	return func(c *Ctx, args []string) (Result, error) {
		f.runs++
		if c.Stdin != nil {
			b, _ := io.ReadAll(c.Stdin)
			f.body = string(b)
		}
		d := jsonx.NewObject()
		d.Set("id", args[0])
		d.Set("changed", true)
		if f.verdict != "" {
			d.Set("verdict", f.verdict)
		}
		if f.fail != nil {
			return Result{Data: d}, f.fail
		}
		return Result{Data: d, Text: "did " + args[0]}, nil
	}
}

// callReceipted runs the wrapped verb in dir with args, as the CLI would.
func callReceipted(t *testing.T, dir string, f *fakeVerb, args ...string) (Result, error, string) {
	t.Helper()
	old, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(old) })
	fs := flag.NewFlagSet("x", flag.ContinueOnError)
	var in []inputsFunc
	if f.heads != nil {
		in = append(in, func(*Ctx, string, []string) []string { return f.heads() })
	}
	run := receipted("round assign", f.verb, in...)(fs)
	if err := fs.Parse(args); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	res, err := run(&Ctx{Path: "rota round assign", Stderr: &stderr, Stdin: strings.NewReader(f.stdin), dashAt: -1}, fs.Args())
	return res, err, stderr.String()
}

func field(t *testing.T, r Result, key string) any {
	t.Helper()
	o, ok := r.Data.(*jsonx.Object)
	if !ok {
		t.Fatalf("data is %T", r.Data)
	}
	v, _ := o.Get(key)
	return v
}

func TestReceiptMintsAnIDAndReplaysTheFirstResult(t *testing.T) {
	dir := workerProject(t, `{}`)
	f := &fakeVerb{}
	res, err, _ := callReceipted(t, dir, f, "--request-id", "r1", "42")
	if err != nil || field(t, res, "requestId") != "r1" || f.runs != 1 {
		t.Fatalf("first call: %v %v runs=%d", res, err, f.runs)
	}
	res, err, _ = callReceipted(t, dir, f, "--request-id", "r1", "42")
	if err != nil || f.runs != 1 {
		t.Fatalf("retry reran the verb: err=%v runs=%d", err, f.runs)
	}
	if field(t, res, "replayed") != true || field(t, res, "changed") != false || field(t, res, "id") != "42" || res.Text != "did 42" {
		t.Errorf("replay: %+v", res.Data)
	}
	res, _, _ = callReceipted(t, dir, f, "43")
	if id, _ := field(t, res, "requestId").(string); !regexp.MustCompile(`^req-[0-9a-f]{12}$`).MatchString(id) {
		t.Errorf("no minted request id: %v", id)
	}
}

func TestReceiptSameIDOtherArgumentsIsRefused(t *testing.T) {
	dir := workerProject(t, `{}`)
	f := &fakeVerb{}
	callReceipted(t, dir, f, "--request-id", "r1", "42")
	for name, args := range map[string][]string{
		"other item": {"--request-id", "r1", "43"},
		"other flag": {"--request-id", "r1", "--agent", "ben", "42"},
	} {
		res, err, _ := callReceipted(t, dir, f, args...)
		var e *Error
		if !errors.As(err, &e) || e.Exit != ExitRefused || field(t, res, "blockedBy") != "request-id" {
			t.Errorf("%s: err=%v data=%v", name, err, res.Data)
		}
	}
	if f.runs != 1 {
		t.Errorf("verb ran %d times", f.runs)
	}
}

func TestReceiptFailureNamesTheStageAndRetryResumes(t *testing.T) {
	dir := workerProject(t, `{}`)
	f := &fakeVerb{fail: &round.StageError{Stage: "claim", Residual: []string{"branch"}, Err: errors.New("gh: 502")}}
	res, err, _ := callReceipted(t, dir, f, "--request-id", "r1", "42")
	var e *Error
	if !errors.As(asError(err), &e) || !strings.Contains(e.Hint, "retry with --request-id r1") {
		t.Fatalf("hint: %v", err)
	}
	if field(t, res, "failedStage") != "claim" {
		t.Errorf("failure data: %v", res.Data)
	}
	f.fail = nil
	res, err, _ = callReceipted(t, dir, f, "--request-id", "r1", "42")
	if err != nil || f.runs != 2 || field(t, res, "resumedFrom") != "claim" {
		t.Errorf("retry: err=%v runs=%d data=%v", err, f.runs, res.Data)
	}
	if _, _, _ = callReceipted(t, dir, f, "--request-id", "r1", "42"); f.runs != 2 {
		t.Errorf("a done receipt must replay, runs=%d", f.runs)
	}
}

func TestReceiptVerdictFailureIsSettledNotPending(t *testing.T) {
	dir := workerProject(t, `{}`)
	f := &fakeVerb{verdict: "verify-failed", fail: Failed("verify FAILED")}
	_, err, _ := callReceipted(t, dir, f, "--request-id", "r1", "42")
	var e *Error
	if !errors.As(asError(err), &e) || e.Exit != ExitFailed {
		t.Fatalf("first call: %v", err)
	}
	if _, found, _ := receipt.New(dir).Pending("round assign", callTarget(&Ctx{}, []string{"42"})); found {
		t.Errorf("a verdict is an answer, not an interrupted run: receipt must not be pending")
	}
	_, _, stderr := callReceipted(t, dir, f, "42")
	if strings.Contains(stderr, "stopped part way") {
		t.Errorf("rerun warned about a finished call: %q", stderr)
	}
	f.runs = 1
	_, err, _ = callReceipted(t, dir, f, "--request-id", "r1", "42")
	if f.runs != 1 || !errors.As(asError(err), &e) || e.Exit != ExitFailed {
		t.Errorf("replay must keep the verdict's exit 1 and not rerun: runs=%d err=%v", f.runs, err)
	}
}

func TestReceiptSettledVerdictHintDoesNotSuggestRetryWithID(t *testing.T) {
	dir := workerProject(t, `{}`)
	f := &fakeVerb{verdict: "verify-failed", fail: Failed("verify FAILED")}
	_, err, _ := callReceipted(t, dir, f, "--request-id", "r1", "42")
	var e *Error
	if !errors.As(asError(err), &e) {
		t.Fatalf("first call: %v", err)
	}
	if strings.Contains(e.Hint, "retry with --request-id") {
		t.Errorf("a settled verdict replays on the same id, hint must not suggest it: %q", e.Hint)
	}
	if !strings.Contains(e.Hint, "without --request-id") {
		t.Errorf("hint must name what reruns the call: %q", e.Hint)
	}
}

func TestReceiptLeavesNothingForARefusalOrCheckOnly(t *testing.T) {
	dir := workerProject(t, `{}`)
	f := &fakeVerb{fail: Refused("not ready")}
	callReceipted(t, dir, f, "--request-id", "r1", "42")
	f.fail = nil
	if _, err, _ := callReceipted(t, dir, f, "--request-id", "r1", "42"); err != nil || f.runs != 2 {
		t.Fatalf("a refusal must not be remembered: err=%v runs=%d", err, f.runs)
	}
	res, _, _ := callReceipted(t, dir, f, "--check-only", "--request-id", "c1", "7")
	if field(t, res, "requestId") != nil {
		t.Error("--check-only must bypass the receipt")
	}
	fs := flag.NewFlagSet("x", flag.ContinueOnError)
	fs.Bool("check-only", false, "")
	fs.String("request-id", "", "")
	fs.Parse([]string{"--check-only", "--request-id", "c1"})
	fp := fingerprint(&Ctx{}, fs, []string{"7"})
	if _, found, err := receipt.New(dir).Lookup(receipt.Call{ID: "c1", Verb: "round assign", Fingerprint: fp}); found || err != nil {
		t.Errorf("check-only left a receipt: found=%v err=%v", found, err)
	}
	// The same lookup does find a receipt a real call left, so the check above can fail.
	callReceipted(t, dir, f, "--request-id", "c2", "7")
	fs = flag.NewFlagSet("x", flag.ContinueOnError)
	fs.String("request-id", "", "")
	fs.String("agent", "", "")
	fs.Bool("check-only", false, "")
	fs.String("body-file", "", "")
	fs.Parse([]string{"--request-id", "c2"})
	if _, found, _ := receipt.New(dir).Lookup(receipt.Call{ID: "c2", Verb: "round assign", Fingerprint: fingerprint(&Ctx{}, fs, []string{"7"})}); !found {
		t.Error("the control lookup must find the receipt of a real call")
	}
}

func TestReceiptChangedStdinUnderTheSameIDIsRefused(t *testing.T) {
	dir := workerProject(t, `{}`)
	f := &fakeVerb{stdin: "brief one"}
	callReceipted(t, dir, f, "--request-id", "r1", "--body-file", "-", "42")
	if f.body != "brief one" {
		t.Fatalf("the verb must still read stdin after the fingerprint did: %q", f.body)
	}
	f.stdin = "brief one"
	if _, err, _ := callReceipted(t, dir, f, "--request-id", "r1", "--body-file", "-", "42"); err != nil || f.runs != 1 {
		t.Fatalf("same stdin must replay: err=%v runs=%d", err, f.runs)
	}
	f.stdin = "brief two"
	res, err, _ := callReceipted(t, dir, f, "--request-id", "r1", "--body-file", "-", "42")
	var e *Error
	if !errors.As(err, &e) || e.Exit != ExitRefused || field(t, res, "blockedBy") != "request-id" || f.runs != 1 {
		t.Errorf("changed stdin: err=%v data=%v runs=%d", err, res.Data, f.runs)
	}
}

func TestReceiptChangedBodyFileContentIsRefused(t *testing.T) {
	dir := workerProject(t, `{}`)
	p := filepath.Join(t.TempDir(), "brief.md")
	os.WriteFile(p, []byte("one"), 0o644)
	f := &fakeVerb{}
	callReceipted(t, dir, f, "--request-id", "r1", "--body-file", p, "42")
	os.WriteFile(p, []byte("two"), 0o644)
	if _, err, _ := callReceipted(t, dir, f, "--request-id", "r1", "--body-file", p, "42"); err == nil || f.runs != 1 {
		t.Errorf("an edited brief under the same path must not replay: err=%v runs=%d", err, f.runs)
	}
}

func TestReceiptChangedInputsUnderTheSameIDAreRefused(t *testing.T) {
	dir := workerProject(t, `{}`)
	head := "aaa"
	f := &fakeVerb{heads: func() []string { return []string{"head:ben=" + head} }}
	callReceipted(t, dir, f, "--request-id", "r1", "42")
	head = "bbb" // a new push
	res, err, _ := callReceipted(t, dir, f, "--request-id", "r1", "42")
	var e *Error
	if !errors.As(err, &e) || e.Exit != ExitRefused || field(t, res, "blockedBy") != "request-id" || f.runs != 1 {
		t.Errorf("a gate after a new push must not replay: err=%v data=%v runs=%d", err, res.Data, f.runs)
	}
}

// gateHeadsRepo is a project with the branch ben/1-x and the given registry,
// plus a helper that commits on that branch (a push).
func gateHeadsRepo(t *testing.T, registry string) (dir string, push func()) {
	t.Helper()
	dir = workerProject(t, `{}`)
	gittest.Run(t, dir, "branch", "ben/1-x")
	os.WriteFile(filepath.Join(dir, ".rota", "workers.json"), []byte(registry), 0o644)
	return dir, func() {
		gittest.Run(t, dir, "switch", "-q", "ben/1-x")
		gittest.Run(t, dir, "commit", "-q", "--allow-empty", "-m", "push")
	}
}

func TestGateHeadsFollowTargetResolution(t *testing.T) {
	const pr = "https://example.test/o/r/pull/7"
	cases := []struct {
		name, registry string
		args           []string
	}{
		{"slot by name and PR number", `{"slots":[{"name":"ben","branch":"ben/1-x","pr":"` + pr + `"}]}`, []string{"ben", "7"}},
		{"queued PR whose slot moved on", `{"slots":[{"name":"ben","branch":"park/ben"}],"prs":[{"issue":"1","branch":"ben/1-x","pr":"` + pr + `","base":"main","from":"ben"}]}`, []string{"#7", "7"}},
		{"adopted external slot", `{"slots":[{"name":"ext","branch":"ben/1-x","pr":"` + pr + `","external":true}]}`, []string{"ext", "#7"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, push := gateHeadsRepo(t, tc.registry)
			before := strings.Join(gateHeads(&Ctx{}, dir, tc.args), "|")
			if strings.Contains(before, "=?") {
				t.Fatalf("every argument must resolve to a head: %s", before)
			}
			push()
			if after := strings.Join(gateHeads(&Ctx{}, dir, tc.args), "|"); after == before {
				t.Errorf("a push must change the heads: %s", after)
			}
		})
	}
}

// A push to a queued PR's branch is a different gate call: the stored done
// receipt does not replay it under the same request id.
func TestQueuedPRPushUnderTheSameIDDoesNotReplay(t *testing.T) {
	const pr = "https://example.test/o/r/pull/7"
	dir, push := gateHeadsRepo(t, `{"slots":[{"name":"ben","branch":"park/ben"}],"prs":[{"issue":"1","branch":"ben/1-x","pr":"`+pr+`","base":"main","from":"ben"}]}`)
	f := &fakeVerb{heads: func() []string { return gateHeads(&Ctx{}, dir, []string{"#7"}) }}
	callReceipted(t, dir, f, "--request-id", "r1", "#7")
	if _, err, _ := callReceipted(t, dir, f, "--request-id", "r1", "#7"); err != nil || f.runs != 1 {
		t.Fatalf("an unchanged queued PR must replay: err=%v runs=%d", err, f.runs)
	}
	push()
	res, err, _ := callReceipted(t, dir, f, "--request-id", "r1", "#7")
	var e *Error
	if !errors.As(err, &e) || e.Exit != ExitRefused || field(t, res, "blockedBy") != "request-id" || f.runs != 1 {
		t.Errorf("a push must not replay the stored receipt: err=%v data=%v runs=%d", err, res.Data, f.runs)
	}
}

func TestGateHeadsUnknownTokenIsUnresolved(t *testing.T) {
	dir, _ := gateHeadsRepo(t, `{}`)
	if got := gateHeads(&Ctx{}, dir, []string{"zed"}); got[0] != "head:zed=?" {
		t.Errorf("unknown slot: %v", got)
	}
}

// A PR no slot or record owns has no branch of its own: its head is the
// forge's, so a push to it still changes the fingerprint.
func TestGateHeadsExternalPRReadsTheForge(t *testing.T) {
	dir, _ := gateHeadsRepo(t, `{}`)
	os.WriteFile(filepath.Join(dir, ".rota", "config.json"), []byte(`{"forge":{"provider":"github"}}`), 0o644)
	sha := "feedface00000000000000000000000000000001"
	deps := testDeps()
	deps.TrackerOptions = []tracker.Option{tracker.WithExec(func(_ context.Context, _, _ string, args []string, _ []byte) ([]byte, []byte, int, error) {
		if len(args) > 2 && args[0] == "pr" && args[1] == "view" && args[2] == "99" {
			return []byte(`{"headRefName":"fix/forward","headRefOid":"` + sha + `","baseRefName":"main","state":"OPEN"}`), nil, 0, nil
		}
		return nil, []byte("unexpected " + strings.Join(args, " ")), 1, nil
	}, func(n string) (string, error) { return "/fake/" + n, nil })}
	if got := gateHeads(&Ctx{Deps: deps}, dir, []string{"#99"}); got[0] != "head:#99="+sha {
		t.Errorf("got %v, want the forge's head %s", got, sha)
	}
}

func TestReceiptCorruptFileIsRefusedForAMintedID(t *testing.T) {
	dir := workerProject(t, `{}`)
	os.WriteFile(filepath.Join(dir, ".rota", "receipts.json"), []byte("{nope"), 0o644)
	f := &fakeVerb{}
	_, err, _ := callReceipted(t, dir, f, "42")
	var e *Error
	if !errors.As(asError(err), &e) || e.Exit != ExitUnavailable || f.runs != 0 {
		t.Errorf("a corrupt receipts file must stop the verb with exit 5: err=%v runs=%d", err, f.runs)
	}
}

// Every verb the table names is a verb of the tree and takes --request-id; a
// rename that left the table behind would drop a verb's receipt without a word.
func TestReceiptedVerbsAreWiredIntoTheTree(t *testing.T) {
	root := Tree()
	for _, r := range receiptedVerbs {
		cmd := findVerb(root, strings.Fields(r.verb))
		if cmd == nil || cmd.Verb == nil {
			t.Fatalf("%s is not a verb", r.verb)
		}
		fs := flag.NewFlagSet(r.verb, flag.ContinueOnError)
		cmd.Verb(fs)
		if fs.Lookup("request-id") == nil {
			t.Errorf("%s has no --request-id", r.verb)
		}
	}
}

// writeReceipts puts a receipts file in a fresh project and returns its dir.
func writeReceipts(t *testing.T, body string) string {
	t.Helper()
	dir := gitRepo(t)
	if body != "" {
		if err := os.WriteFile(filepath.Join(dir, ".rota", "receipts.json"), []byte(body), 0o666); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const showReceipts = `{"receipts": [
 {"id": "done-1", "verb": "round assign", "fingerprint": "f", "status": "done", "attempts": 1, "text": "assigned 42", "data": {"issue": "42", "changed": true}, "at": "2026-01-02T03:04:05Z"},
 {"id": "fail-1", "verb": "worker gate", "fingerprint": "f", "status": "failed", "attempts": 2, "exit": 5, "message": "gh: 502", "failedStage": "merge", "residualResources": ["branch"], "at": "2026-01-02T03:04:06Z"}
]}`

func TestReceiptShowReportsEachState(t *testing.T) {
	dir := writeReceipts(t, showReceipts)
	for id, want := range map[string]string{"done-1": "completed", "fail-1": "pending", "never-used": "absent"} {
		code, out, errOut := rotaIn(t, dir, "--json", "receipt", "show", id)
		d := data(t, out)
		if code != 0 || d["state"] != want || d["requestId"] != id {
			t.Errorf("%s: want %s, got exit=%d data=%v stderr=%s", id, want, code, d, errOut)
		}
	}
}

func TestReceiptShowCompletedCarriesTheStoredResult(t *testing.T) {
	dir := writeReceipts(t, showReceipts)
	_, out, _ := rotaIn(t, dir, "--json", "receipt", "show", "done-1")
	d := data(t, out)
	res, _ := d["result"].(map[string]any)
	if res["issue"] != "42" || d["verb"] != "round assign" || d["text"] != "assigned 42" {
		t.Errorf("stored result not returned: %v", d)
	}
}

func TestReceiptShowPendingNamesTheStageAndTheRetry(t *testing.T) {
	dir := writeReceipts(t, showReceipts)
	_, out, _ := rotaIn(t, dir, "--json", "receipt", "show", "fail-1")
	d := data(t, out)
	if d["failedStage"] != "merge" || d["message"] != "gh: 502" || d["attempts"] != float64(2) {
		t.Errorf("failure detail missing: %v", d)
	}
	if res, _ := d["residualResources"].([]any); len(res) != 1 {
		t.Errorf("residualResources missing: %v", d)
	}
}

func TestReceiptShowAbsentIsNotProofNothingHappened(t *testing.T) {
	for name, body := range map[string]string{"no file": "", "other ids": showReceipts} {
		code, out, _ := rotaIn(t, writeReceipts(t, body), "receipt", "show", "never-used")
		if code != 0 || !strings.Contains(out, "not proof") {
			t.Errorf("%s: absent text must say it is not proof, exit=%d out=%q", name, code, out)
		}
	}
}

func TestReceiptShowRefusals(t *testing.T) {
	if code, _, _ := rotaIn(t, writeReceipts(t, ""), "receipt", "show"); code != 2 {
		t.Errorf("a missing id is a usage error, got exit %d", code)
	}
	if code, _, _ := rotaIn(t, writeReceipts(t, "{not json"), "receipt", "show", "r1"); code != 5 {
		t.Errorf("a corrupt receipts file is exit 5, got %d", code)
	}
}

func TestReceiptRerunWithoutIDWarnsAboutThePendingReceipt(t *testing.T) {
	dir := workerProject(t, `{}`)
	f := &fakeVerb{fail: &round.StageError{Stage: "claim", Err: errors.New("gh: 502")}}
	callReceipted(t, dir, f, "--request-id", "r1", "--agent", "ben", "42")
	f.fail = nil
	_, err, stderr := callReceipted(t, dir, f, "--agent", "ben", "42")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"r1", "--agent=ben", "42", "--request-id r1"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("warning must mention %q, got %q", want, stderr)
		}
	}
	_, _, stderr = callReceipted(t, dir, f, "43")
	if strings.Contains(stderr, "r1") {
		t.Errorf("another target must not warn about r1: %q", stderr)
	}
}

func TestReceiptMismatchRefusalPrintsTheOriginalArguments(t *testing.T) {
	dir := workerProject(t, `{}`)
	f := &fakeVerb{}
	callReceipted(t, dir, f, "--request-id", "r1", "--agent", "ben", "42")
	_, err, _ := callReceipted(t, dir, f, "--request-id", "r1", "43")
	if err == nil || !strings.Contains(err.Error(), "--agent=ben") || !strings.Contains(err.Error(), "42") {
		t.Errorf("refusal must print the original arguments: %v", err)
	}
}

func TestReceiptShowPendingPrintsTheOriginalArguments(t *testing.T) {
	dir := writeReceipts(t, `{"receipts": [
 {"id": "fail-2", "verb": "worker gate", "fingerprint": "f", "args": ["--agent=ben", "42"], "status": "failed", "attempts": 1, "exit": 5, "message": "gh: 502", "at": "2026-01-02T03:04:06Z"}
]}`)
	code, out, _ := rotaIn(t, dir, "--json", "receipt", "show", "fail-2")
	d := data(t, out)
	if args, _ := d["args"].([]any); code != 0 || len(args) != 2 || args[0] != "--agent=ben" {
		t.Errorf("json args: exit=%d %v", code, d)
	}
	_, out, _ = rotaIn(t, dir, "receipt", "show", "fail-2")
	if !strings.Contains(out, "--agent=ben 42") {
		t.Errorf("text must print the arguments: %q", out)
	}
}
