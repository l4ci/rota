package worker

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func inboxRoot(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".rota"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".rota", "workers.json"), []byte(`{"slots":[{"name":"ben","task":"12","branch":"ben/12-x","state":"busy"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestMailPostsUnreadAndAcksOnce(t *testing.T) {
	root := inboxRoot(t)
	now := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	m1, err := PostMail(root, "ben", "review", "REVIEW one", now)
	if err != nil {
		t.Fatal(err)
	}
	m2, err := PostMail(root, "ben", "review", "REVIEW two", now)
	if err != nil {
		t.Fatal(err)
	}
	if m1.Seq != 1 || m2.Seq != 2 {
		t.Fatalf("seq %d %d, want 1 2", m1.Seq, m2.Seq)
	}
	s := LoadRegistryTolerant(root).Slot("ben")
	if got := s.Unread(); len(got) != 2 || got[0].Text != "REVIEW one" || got[0].Kind != "review" || got[0].Round != 1 || got[0].Read {
		t.Fatalf("unread %+v", got)
	}
	var acked []Mail
	if _, err := UpdateSlot(root, "ben", func(s *Slot) { acked = s.AckUnread(0) }); err != nil {
		t.Fatal(err)
	}
	s = LoadRegistryTolerant(root).Slot("ben")
	if len(acked) != 2 || len(s.Unread()) != 0 || len(s.Inbox()) != 2 {
		t.Fatalf("acked %d unread %d inbox %d", len(acked), len(s.Unread()), len(s.Inbox()))
	}
	m3, _ := PostMail(root, "ben", "review", "REVIEW three", now)
	if m3.Seq != 3 {
		t.Fatalf("seq after ack %d, want 3", m3.Seq)
	}
}

func TestRetractMailRemovesOnlyThatMail(t *testing.T) {
	root := inboxRoot(t)
	now := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	m1, _ := PostMail(root, "ben", "review", "keep", now)
	m2, _ := PostMail(root, "ben", "review", "drop", now)
	if err := RetractMail(root, "ben", m2.Seq); err != nil {
		t.Fatal(err)
	}
	got := LoadRegistryTolerant(root).Slot("ben").Unread()
	if len(got) != 1 || got[0].Seq != m1.Seq {
		t.Fatalf("unread %+v", got)
	}
}

func TestTaskDispatchAndUnbindClearTheInbox(t *testing.T) {
	root := inboxRoot(t)
	_, _ = PostMail(root, "ben", "review", "old task mail", time.Now())
	if _, err := UpdateSlot(root, "ben", func(s *Slot) { s.Dispatch("h", "13", "", "") }); err != nil {
		t.Fatal(err)
	}
	if n := len(LoadRegistryTolerant(root).Slot("ben").Inbox()); n != 0 {
		t.Fatalf("a new task kept %d mails", n)
	}
	_, _ = PostMail(root, "ben", "review", "x", time.Now())
	// A relay (no task) keeps the mail: it is the very thing a relay delivers.
	if _, err := UpdateSlot(root, "ben", func(s *Slot) { s.Dispatch("h", "", "", "") }); err != nil {
		t.Fatal(err)
	}
	if n := len(LoadRegistryTolerant(root).Slot("ben").Inbox()); n != 1 {
		t.Fatalf("a relay dispatch dropped the mail: %d", n)
	}
	if _, err := UpdateSlot(root, "ben", func(s *Slot) { s.Unbind() }); err != nil {
		t.Fatal(err)
	}
	if n := len(LoadRegistryTolerant(root).Slot("ben").Inbox()); n != 0 {
		t.Fatalf("unbind kept %d mails", n)
	}
}
