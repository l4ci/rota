package cli

import (
	"bytes"
	"errors"
	"flag"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/receipt"
	"github.com/l4ci/rota/internal/round"
)

// fakeVerb counts its runs; fail, when set, is returned after the work.
type fakeVerb struct {
	runs  int
	fail  error
	stdin string // fed to the verb as c.Stdin
	heads func() []string
	// body is what the verb read from --body-file -.
	body string
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
		in = append(in, func(string, []string) []string { return f.heads() })
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
	if _, found, err := receipt.Lookup(dir, "c1", "round assign", fp); found || err != nil {
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
	if _, found, _ := receipt.Lookup(dir, "c2", "round assign", fingerprint(&Ctx{}, fs, []string{"7"})); !found {
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

func TestBranchHeadsFollowsPushes(t *testing.T) {
	dir := workerProject(t, `{}`)
	git := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	git("branch", "ben/1-x")
	reg := `{"slots":[{"name":"ben","branch":"ben/1-x","pr":"https://example.test/o/r/pull/7"}]}`
	os.WriteFile(filepath.Join(dir, ".rota", "workers.json"), []byte(reg), 0o644)
	before := strings.Join(branchHeads(dir, []string{"ben", "7"}), "|")
	if strings.Contains(before, "=?") {
		t.Fatalf("slot and PR number must both resolve: %s", before)
	}
	git("switch", "-q", "ben/1-x")
	git("-c", "user.email=a@b", "-c", "user.name=n", "commit", "-q", "--allow-empty", "-m", "push")
	if after := strings.Join(branchHeads(dir, []string{"ben", "7"}), "|"); after == before {
		t.Errorf("a new commit must change the heads: %s", after)
	}
	if got := branchHeads(dir, []string{"zed"}); got[0] != "head:zed=?" {
		t.Errorf("unknown token: %v", got)
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
