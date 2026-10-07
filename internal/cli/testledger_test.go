package cli

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestTestLedgerCheck(t *testing.T) {
	root := tierProject(t, `{}`)
	check := func() (int, map[string]any, string) {
		o := trRun(t, root, "", "test", "ledger", "check", "--json")
		var data map[string]any
		if o.stdout != "" {
			data, _ = envelope(t, o.stdout)["data"].(map[string]any)
		}
		return o.code, data, o.stderr
	}
	if code, data, _ := check(); code != 0 || data["verdict"] != "ok" {
		t.Fatalf("a missing ledger is ok: %d %v", code, data)
	}
	ledger := filepath.Join(root, ".rota", "test-ledger.json")
	write(t, ledger, `[{"test":"TestA","owner":"dana","receipt":"#1","expires":"2999-01-01"}]`)
	if code, data, _ := check(); code != 0 || data["entries"] != float64(1) {
		t.Fatalf("an unexpired entry is ok: %d %v", code, data)
	}
	write(t, ledger, `[{"test":"TestA","owner":"dana","receipt":"#1","expires":"2000-01-01"}]`)
	code, data, _ := check()
	exp, _ := data["expired"].([]any)
	if code != 1 || data["verdict"] != "expired" || len(exp) != 1 || !reflect.DeepEqual(exp[0].(map[string]any)["owner"], "dana") {
		t.Fatalf("an expired entry is exit 1 and named: %d %v", code, data)
	}
	write(t, ledger, `[{"test":"TestA","owner":"dana"}]`)
	code, data, stderr := check()
	if code != 1 || data["verdict"] != "malformed" || !strings.Contains(stderr, "entry 1 (TestA)") {
		t.Fatalf("a malformed entry is exit 1 and named: %d %v %s", code, data, stderr)
	}
}
