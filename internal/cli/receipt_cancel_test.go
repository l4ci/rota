package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/receipt"
	"github.com/l4ci/rota/internal/worker"
)

// A cancelled train settles its receipt once as cancelled, not as done or
// failed, and the same id replays it rather than running the train again
// (#2751, AC-4).
func TestReceiptCancelledVerdictSettlesAsCancelled(t *testing.T) {
	dir := workerProject(t, `{}`)
	f := &fakeVerb{verdict: worker.GateCancelled, fail: Failed("CANCELLED train")}
	if _, err, _ := callReceipted(t, dir, f, "--request-id", "r1", "42"); err == nil {
		t.Fatal("a cancelled train is not a success")
	}
	e, found, err := receipt.New(dir).Show("r1")
	if err != nil || !found || e.Status != receipt.Cancelled || e.Attempts != 1 {
		t.Fatalf("receipt %+v found=%v err=%v; want status %q", e, found, err, receipt.Cancelled)
	}
	_, err, stderr := callReceipted(t, dir, f, "--request-id", "r1", "42")
	var ae *Error
	if f.runs != 1 || !errors.As(asError(err), &ae) || ae.Exit != ExitFailed || !strings.Contains(stderr, "already applied") {
		t.Errorf("the same id must replay the cancel: runs=%d err=%v stderr=%q", f.runs, err, stderr)
	}
	if e, _, _ := receipt.New(dir).Show("r1"); e.Attempts != 1 {
		t.Errorf("a replay must not settle it again: attempts=%d", e.Attempts)
	}
}

// receipt show names a cancelled train as cancelled, with its stored result
// (#2751, AC-7).
func TestReceiptShowReportsCancelled(t *testing.T) {
	dir := writeReceipts(t, `{"receipts": [
 {"id": "c-1", "verb": "worker train", "fingerprint": "f", "status": "cancelled", "attempts": 1, "exit": 1, "message": "CANCELLED train — stopped before it finished; nothing landed", "text": "", "data": {"verdict": "cancelled", "landed": [], "log": "/tmp/v.log"}, "at": "2026-01-02T03:04:05Z"}
]}`)
	code, out, errOut := rotaIn(t, dir, "--json", "receipt", "show", "c-1")
	d := data(t, out)
	res, _ := d["result"].(map[string]any)
	if code != 0 || d["state"] != "cancelled" || res["verdict"] != "cancelled" || res["log"] != "/tmp/v.log" {
		t.Errorf("want state cancelled with the stored result, got exit=%d data=%v stderr=%s", code, d, errOut)
	}
	_, text, _ := rotaIn(t, dir, "receipt", "show", "c-1")
	if !strings.HasPrefix(text, "cancelled:") || strings.Contains(text, "pending") {
		t.Errorf("text %q", text)
	}
}
