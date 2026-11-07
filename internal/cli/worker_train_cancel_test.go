package cli

import (
	"strings"
	"testing"
)

// An id no running train holds cancels nothing and exits non-zero with a
// message that says why: unknown is exit 3, a settled call exit 4 (#2751, AC-1).
func TestWorkerTrainCancelRefusesAnIDThatIsNotRunning(t *testing.T) {
	dir := writeReceipts(t, `{"receipts": [
 {"id": "c-1", "verb": "worker train", "fingerprint": "f", "status": "cancelled", "attempts": 1, "exit": 1, "message": "CANCELLED train", "at": "2026-01-02T03:04:05Z"},
 {"id": "d-1", "verb": "worker train", "fingerprint": "f", "status": "done", "attempts": 1, "at": "2026-01-02T03:04:05Z"},
 {"id": "a-1", "verb": "round assign", "fingerprint": "f", "status": "done", "attempts": 1, "at": "2026-01-02T03:04:05Z"}
]}`)
	for _, tc := range []struct {
		id   string
		code int
		want string
	}{
		{"never-used", 3, "no running train holds request id never-used"},
		{"c-1", 4, "already settled as cancelled"},
		{"d-1", 4, "already settled as done"},
		{"a-1", 4, "belongs to round assign, not a train"},
	} {
		code, out, errOut := rotaIn(t, dir, "--json", "worker", "train", "cancel", "--request-id", tc.id)
		if code != tc.code || !strings.Contains(out+errOut, tc.want) {
			t.Errorf("%s: want exit %d with %q, got %d\n%s%s", tc.id, tc.code, tc.want, code, out, errOut)
		}
		if d := data(t, out); tc.code == 4 && (d["changed"] != false || d["requestId"] != tc.id) {
			t.Errorf("%s: a refused cancel changes nothing: %v", tc.id, d)
		}
	}
	if code, _, errOut := rotaIn(t, dir, "worker", "train", "cancel"); code != 2 || !strings.Contains(errOut, "--request-id is required") {
		t.Errorf("missing id: exit %d %s", code, errOut)
	}
}
