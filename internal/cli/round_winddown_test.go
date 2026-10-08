package cli

import (
	"strings"
	"testing"
)

func TestWindDownPrintsSummary(t *testing.T) {
	dir := workerProject(t, `{}`)
	seedLedger(t, dir)
	obj, text, ok := windDownSummary(dir, 2)
	if !ok || !strings.Contains(text, "Gate audit") || !strings.Contains(text, "ben") {
		t.Fatalf("ok=%v text:\n%s", ok, text)
	}
	if obj == nil {
		t.Fatal("no summary object")
	}
	if _, _, ok := windDownSummary(workerProject(t, `{}`), 2); ok {
		t.Error("a project with no ledger must add nothing")
	}
}
