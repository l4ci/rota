package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/worker"
)

func postMail(t *testing.T, dir, text string) {
	t.Helper()
	if _, err := worker.PostMail(dir, "ben", worker.MailReview, text, time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerInboxListsUnreadAndAckMarksThemRead(t *testing.T) {
	dir := doneProject(t, "")
	postMail(t, dir, "REVIEW https://github.com/o/r/pull/9 (1 items)\n> please rename x")

	code, out, errOut := rotaIn(t, dir, "worker", "inbox", "ben", "--json")
	d := data(t, out)
	if code != 0 || d["unread"] != 1.0 || d["acked"] != 0.0 || !strings.Contains(out, "please rename x") {
		t.Fatalf("list: %d %v %s", code, d, errOut)
	}
	// Listing alone leaves the mail unread.
	if code, out, _ := rotaIn(t, dir, "worker", "inbox", "ben", "--json"); code != 0 || data(t, out)["unread"] != 1.0 {
		t.Fatalf("listing consumed the mail: %d %s", code, out)
	}
	code, out, errOut = rotaIn(t, dir, "worker", "inbox", "ben", "--ack", "--json")
	if d := data(t, out); code != 0 || d["acked"] != 1.0 || !strings.Contains(out, "please rename x") {
		t.Fatalf("ack: %d %v %s", code, d, errOut)
	}
	code, out, _ = rotaIn(t, dir, "worker", "inbox", "ben", "--json")
	if d := data(t, out); code != 0 || d["unread"] != 0.0 {
		t.Fatalf("after ack: %d %v", code, d)
	}
}

func TestWorkerInboxEmptyAndUnknownSlot(t *testing.T) {
	dir := doneProject(t, "")
	code, out, _ := rotaIn(t, dir, "worker", "inbox", "ben")
	if code != 0 || !strings.Contains(out, "no unread mail") {
		t.Fatalf("empty: %d %q", code, out)
	}
	if code, _, _ := rotaIn(t, dir, "worker", "inbox", "zed"); code != 3 {
		t.Fatalf("unknown slot: %d, want 3", code)
	}
}

// A relay written after the worker's last test run and before done: done
// refuses, naming the verb to run, and works once the mail is acknowledged.
func TestWorkerDoneRefusesWhileMailIsUnread(t *testing.T) {
	dir := doneProject(t, "")
	postMail(t, dir, "REVIEW https://github.com/o/r/pull/9 (1 items)")

	code, out, _ := rotaIn(t, dir, "worker", "done", "ben", "--json")
	d := data(t, out)
	if code != 4 || d["unread"] != 1.0 || d["changed"] != false || !strings.Contains(out, "rota worker inbox ben --ack") {
		t.Fatalf("done with unread mail: %d %s", code, out)
	}
	if slotState(t, dir) == "done" {
		t.Error("a refusal must not mark the slot done")
	}
	if code, out, _ := rotaIn(t, dir, "worker", "inbox", "ben", "--ack", "--json"); code != 0 {
		t.Fatalf("ack: %d %s", code, out)
	}
	if code, out, _ := rotaIn(t, dir, "worker", "done", "ben", "--json"); code != 0 {
		t.Fatalf("done after ack: %d %s", code, out)
	}
	if slotState(t, dir) != "done" {
		t.Error("the slot should be done once the mail is read")
	}
}

// --outcome failed skips the proof check, not the mail.
func TestWorkerDoneFailedStillRefusesUnreadMail(t *testing.T) {
	dir := doneProject(t, "true")
	postMail(t, dir, "REVIEW x")
	sum := t.TempDir() + "/s.txt"
	writeFile(t, sum, "a\nb\nc\n")
	if code, out, _ := rotaIn(t, dir, "worker", "done", "ben", "--outcome", "failed", "--summary-file", sum, "--json"); code != 4 || data(t, out)["unread"] != 1.0 {
		t.Fatalf("%d %s", code, out)
	}
}
