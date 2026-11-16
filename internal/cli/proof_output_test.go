package cli

import (
	"strings"
	"testing"
)

// A failing proof shows the tail of what the command printed and the cwd it
// ran in, so the failure is diagnosable without rerunning it by hand.
func TestProofRecordFailShowsOutputTailAndCwd(t *testing.T) {
	needNice(t)
	dir := doneProject(t, "true")
	code, out, errOut := rotaIn(t, dir, "proof", "record", "B07", "--", "echo boom-$((1+1)); exit 3")
	if code != 1 {
		t.Fatalf("exit %d: %s %s", code, out, errOut)
	}
	all := out + errOut
	if !strings.Contains(all, "boom-2") {
		t.Errorf("fail output lacks the command's output tail: %s", all)
	}
	if !strings.Contains(all, "cwd: "+dir) {
		t.Errorf("fail output lacks the cwd line: %s", all)
	}
	_, js, _ := rotaIn(t, dir, "proof", "record", "B07", "--json", "--", "echo boom-$((1+1)); exit 3")
	d := data(t, js)
	if d["cwd"] != dir || !strings.Contains(d["outputTail"].(string), "boom-2") {
		t.Errorf("json data lacks cwd/outputTail: %s", js)
	}
}

func TestProofRecordPassKeepsShortOutputPlusCwd(t *testing.T) {
	needNice(t)
	dir := doneProject(t, "true")
	code, out, errOut := rotaIn(t, dir, "proof", "record", "B07", "--", "echo quiet-$((1+1))")
	if code != 0 {
		t.Fatalf("exit %d: %s %s", code, out, errOut)
	}
	if strings.Contains(out+errOut, "quiet-2") {
		t.Errorf("a passing proof must not print the command output: %s", out)
	}
	if !strings.Contains(out, "cwd: "+dir) {
		t.Errorf("pass output lacks the cwd line: %s", out)
	}
}

func TestWorkerDoneProofFailShowsTailAndCwd(t *testing.T) {
	needNice(t)
	dir := doneProject(t, "true")
	code, out, errOut := rotaIn(t, dir, "worker", "done", "ben", "--proof", "echo boom-$((1+1)); exit 2")
	if code != 4 {
		t.Fatalf("exit %d: %s %s", code, out, errOut)
	}
	all := out + errOut
	if !strings.Contains(all, "boom-2") || !strings.Contains(all, "cwd: ") {
		t.Errorf("refusal lacks output tail or cwd: %s", all)
	}
}

func TestWorkerDoneProofPassStatesCwd(t *testing.T) {
	needNice(t)
	dir := doneProject(t, "false")
	code, out, errOut := rotaIn(t, dir, "worker", "done", "ben", "--proof", "echo quiet-$((1+1))")
	if code != 0 {
		t.Fatalf("exit %d: %s %s", code, out, errOut)
	}
	if strings.Contains(out+errOut, "quiet-2") || !strings.Contains(out, "cwd: ") {
		t.Errorf("pass output wrong: %s", out)
	}
}
