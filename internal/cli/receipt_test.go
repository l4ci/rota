package cli

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/receipt"
	"github.com/l4ci/rota/internal/round"
)

// fakeVerb counts its runs; fail, when set, is returned after the work.
type fakeVerb struct {
	runs int
	fail error
	exit int
}

func (f *fakeVerb) verb(fs *flag.FlagSet) RunFunc {
	fs.String("agent", "", "")
	fs.Bool("check-only", false, "")
	return func(c *Ctx, args []string) (Result, error) {
		f.runs++
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
	run := receipted("round assign", f.verb)(fs)
	if err := fs.Parse(args); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	res, err := run(&Ctx{Path: "rota round assign", Stderr: &stderr, dashAt: -1}, fs.Args())
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
	if id, _ := field(t, res, "requestId").(string); len(id) < 8 {
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
	if _, found, _ := receipt.Lookup(dir, "c1", "round assign", ""); found {
		t.Error("check-only left a receipt")
	}
}
