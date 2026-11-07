package cli

import (
	"context"
	"github.com/l4ci/rota/internal/proc"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestTestAuditUnavailable(t *testing.T) {
	root := tierProject(t, `{}`)
	code, out, errout := rotaInWith(t, testDeps(), root, "test-audit", "--check", "--json")
	if code != 0 {
		t.Fatalf("test-audit must report unavailable evidence without failing: exit %d %s %s", code, out, errout)
	}
	if !strings.Contains(out, "unavailable") {
		t.Fatalf("missing evidence must stay unavailable: %s", out)
	}
}

func TestTestAuditListsOnlyAndCheckDoesNotWrite(t *testing.T) {
	root := tierProject(t, `{"test":{"fast":["DO NOT RUN"],"full":["DO NOT RUN"]}}`)
	write(t, filepath.Join(root, "playwright.config.ts"), "export default {}")
	write(t, filepath.Join(root, "node_modules", ".bin", "playwright"), "fake")
	deps := testDeps()
	calls := 0
	deps.Proc = func(_ context.Context, c proc.Cmd) (proc.Result, error) {
		calls++
		if !reflect.DeepEqual(c.Args, []string{"test", "--list", "--reporter=json", "--config", filepath.Join(root, "playwright.config.ts")}) {
			t.Fatalf("not a listing: %+v", c)
		}
		return proc.Result{Stdout: `{"suites":[{"file":"tests/a.spec.ts","specs":[{"title":"a","file":"tests/a.spec.ts","line":1,"tests":[{"projectName":"chromium"},{"projectName":"webkit"}]}]}]}`}, nil
	}
	code, out, errout := rotaInWith(t, deps, root, "test-audit", "--check", "--json")
	if code != 0 || calls != 0 {
		t.Fatalf("check executes commands: %d %d %s %s", code, calls, out, errout)
	}
	checkpoint := filepath.Join(root, ".git", "rota", "test-audit.json")
	if _, err := os.Stat(checkpoint); !os.IsNotExist(err) {
		t.Fatalf("check wrote checkpoint: %v", err)
	}
	code, out, errout = rotaInWith(t, deps, root, "test-audit", "--json")
	if code != 0 || calls != 1 {
		t.Fatalf("audit: %d %d %s %s", code, calls, out, errout)
	}
	d := data(t, out)
	if d["checkpointRecorded"] != true || d["complete"] != true {
		t.Fatalf("complete audit not recorded: %v", d)
	}
	before, err := os.ReadFile(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	deps.Proc = func(_ context.Context, c proc.Cmd) (proc.Result, error) {
		return proc.Result{ExitCode: 1, Stderr: "listing broke"}, nil
	}
	code, out, errout = rotaInWith(t, deps, root, "test-audit", "--json")
	if code != 0 || data(t, out)["complete"] != false || data(t, out)["checkpointRecorded"] != false {
		t.Fatalf("failed listing presented as complete: %d %s %s", code, out, errout)
	}
	after, _ := os.ReadFile(checkpoint)
	if string(before) != string(after) {
		t.Fatal("failed listing advanced checkpoint")
	}
}
