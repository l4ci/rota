package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/bugreport"
	"github.com/l4ci/rota/internal/git"
)

func runCancelled(t *testing.T, ctx context.Context, dir string, args ...string) (int, string, string) {
	t.Helper()
	chdirForTest(t, dir)
	var so, se bytes.Buffer
	code := runWithContext(ctx, testTree(), recordingDeps(), args, strings.NewReader(""), &so, &se)
	return code, so.String(), se.String()
}

// AC-1, AC-2: a verb stopped by the signal context exits 130 `interrupted`,
// carries no bug hint and leaves the bug-report log empty.
func TestInterruptedVerbExits130(t *testing.T) {
	dir := trProject(t, "{}")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	code, so, se := runCancelled(t, ctx, dir, "--json", "fail", "canceled")
	if code != 130 {
		t.Fatalf("exit %d, want 130; stdout %q stderr %q", code, so, se)
	}
	if !strings.Contains(so, `"code": "interrupted"`) {
		t.Errorf("envelope %q lacks the interrupted code", so)
	}
	if strings.Contains(se, "bug") || strings.Contains(so, "report") {
		t.Errorf("an interruption is no bug: %q %q", so, se)
	}
	if recs, _ := bugreport.Load(context.Background(), git.Exec, dir); len(recs) != 0 {
		t.Errorf("interrupted verb recorded %+v", recs)
	}
}

// AC-3: a cancellation the signal context did not cause stays an internal error.
func TestInternalCancelStaysInternal(t *testing.T) {
	dir := trProject(t, "{}")
	code, so, se := runCancelled(t, context.Background(), dir, "fail", "canceled")
	if code != 70 {
		t.Fatalf("exit %d, want 70; %q %q", code, so, se)
	}
}

// AC-4 (cli half): an interrupted verb still returns the data it knew.
func TestInterruptedVerbKeepsItsAnswer(t *testing.T) {
	dir := trProject(t, "{}")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	code, so, _ := runCancelled(t, ctx, dir, "--json", "fail", "canceled-answer")
	if code != 130 || !strings.Contains(so, `"changed": true`) {
		t.Fatalf("exit %d, stdout %q; want 130 with data", code, so)
	}
}
