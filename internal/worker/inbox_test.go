package worker

import (
	"os"
	"path/filepath"
	"strings"
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

func TestTaskDispatchAndUnbindReturnPendingRequests(t *testing.T) {
	root := inboxRoot(t)
	_, _ = PostMail(root, "ben", "review", "old task mail", time.Now())
	if _, err := UpdateSlot(root, "ben", func(s *Slot) { s.Dispatch("h", "13", "", "") }); err != nil {
		t.Fatal(err)
	}
	s := LoadRegistryTolerant(root).Slot("ben")
	if got := s.Inbox(); len(got) != 1 || got[0].Disposition != "returned" || got[0].Task != "12" || got[0].Reason == "" || len(s.Waiting()) != 0 {
		t.Fatalf("assignment replacement lost ownership or retained obligation: %+v", got)
	}
	_, _ = PostMail(root, "ben", "review", "x", time.Now())
	// A relay (no task) keeps the mail: it is the very thing a relay delivers.
	if _, err := UpdateSlot(root, "ben", func(s *Slot) { s.Dispatch("h", "", "", "") }); err != nil {
		t.Fatal(err)
	}
	if n := len(LoadRegistryTolerant(root).Slot("ben").Waiting()); n != 1 {
		t.Fatalf("a relay dispatch dropped the mail: %d", n)
	}
	if _, err := UpdateSlot(root, "ben", func(s *Slot) { s.Unbind() }); err != nil {
		t.Fatal(err)
	}
	s = LoadRegistryTolerant(root).Slot("ben")
	if got := s.Inbox(); len(got) != 2 || got[1].Disposition != "returned" || got[1].Task != "13" || len(s.Waiting()) != 0 {
		t.Fatalf("unbind lost history or retained obligation: %+v", got)
	}
}

func TestInboxAssignmentIdentityAndSequence(t *testing.T) {
	root := inboxRoot(t)
	update := func(f func(*Slot)) {
		t.Helper()
		if _, err := UpdateSlot(root, "ben", f); err != nil {
			t.Fatal(err)
		}
	}
	update(func(s *Slot) { s.Bind(Binding{Task: "12", Attempt: "first"}) })
	m, err := PostMail(root, "ben", MailBase, "retarget the PR", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	update(func(s *Slot) {
		s.AckUnread(0)
		s.Bind(Binding{Task: "#12", Attempt: "first"})
		s.Dispatch("h", "12", "", "")
	})
	s := LoadRegistryTolerant(root).Slot("ben")
	if len(s.Pending()) != 1 || s.Pending()[0].Attempt != "first" {
		t.Fatalf("same assignment lost request: %+v", s.Inbox())
	}
	update(func(s *Slot) { s.Bind(Binding{Task: "12", Attempt: "second"}) })
	s = LoadRegistryTolerant(root).Slot("ben")
	if len(s.Pending()) != 0 || s.Inbox()[0].Disposition != "returned" || s.Inbox()[0].Attempt != "first" {
		t.Fatalf("new attempt inherited old request: %+v", s.Inbox())
	}
	if err := RetractMail(root, "ben", m.Seq); err != nil {
		t.Fatal(err)
	}
	next, err := PostMail(root, "ben", MailReview, "new request", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if next.Seq != 2 {
		t.Fatalf("retracted sequence reused: %+v", next)
	}
	update(func(s *Slot) {
		if _, err := s.ResolveMail(m.Seq, "handled", "old request"); err == nil {
			t.Error("stale sequence resolved new request")
		}
		s.SetTask("13")
	})
	// SetTask is a rename of the running work: its request follows the new name.
	s = LoadRegistryTolerant(root).Slot("ben")
	if p := s.Pending(); len(p) != 1 || p[0].Seq != next.Seq || p[0].Task != "13" || p[0].Attempt != "second" {
		t.Fatalf("SetTask rename: %+v", s.Inbox())
	}
}

func TestLegacyInformationalAndStaleInboxRecords(t *testing.T) {
	root := inboxRoot(t)
	path := filepath.Join(root, ".rota", "workers.json")
	// A legacy REVIEW is deliberately not upgraded based on its kind. Unknown
	// fields on both the registry and mail must survive acknowledgement.
	fixture := `{"futureRegistry":{"keep":true},"slots":[{"name":"ben","task":"12","state":"busy","attempt":"current","inbox":[{"seq":1,"kind":"review","text":"legacy","futureMail":{"keep":true}},{"seq":2,"kind":"review","text":"stale raw record","actionable":true,"task":"11","attempt":"old","disposition":"pending"}]}]}`
	if err := os.WriteFile(path, []byte(fixture), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateSlot(root, "ben", func(s *Slot) {
		if len(s.Waiting()) != 1 || len(s.Unread()) != 1 || len(s.Pending()) != 0 {
			t.Fatalf("legacy/stale classification: %+v", s.Inbox())
		}
		if _, err := s.ResolveMail(1, "handled", "legacy"); err == nil {
			t.Error("legacy record accepted actionable disposition")
		}
		if _, err := s.ResolveMail(2, "handled", "other assignment"); err == nil {
			t.Error("stale ownership accepted disposition")
		}
		if got := s.AckUnread(0); len(got) != 1 || got[0].Text != "legacy" {
			t.Fatalf("ack touched stale record: %+v", got)
		}
	}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"futureMail"`) || !strings.Contains(string(b), `"futureRegistry"`) {
		t.Fatalf("unknown fields dropped: %s", b)
	}
	m, err := PostMail(root, "ben", "notice", "informational", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if m.Seq != 3 || m.Actionable {
		t.Fatalf("informational classification: %+v", m)
	}
	if _, err := UpdateSlot(root, "ben", func(s *Slot) {
		if _, err := s.ResolveMail(m.Seq, "returned", "info"); err == nil {
			t.Error("informational mail accepted disposition")
		}
		s.AckUnread(0)
	}); err != nil {
		t.Fatal(err)
	}
	s := LoadRegistryTolerant(root).Slot("ben")
	if len(s.Waiting()) != 0 || len(s.Unread()) != 0 || len(s.Pending()) != 0 || len(s.Inbox()) != 3 {
		t.Fatalf("read informational/legacy still active or history lost: %+v", s.Inbox())
	}
}

func TestResolveInboxPreservesUnknownFields(t *testing.T) {
	root := inboxRoot(t)
	path := filepath.Join(root, ".rota", "workers.json")
	fixture := `{"slots":[{"name":"ben","task":"12","attempt":"a","inbox":[{"seq":4,"kind":"base","text":"retarget","actionable":true,"task":"12","attempt":"a","disposition":"pending","futureMail":{"keep":"value"}}]}]}`
	if err := os.WriteFile(path, []byte(fixture), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateSlot(root, "ben", func(s *Slot) {
		if _, err := s.ResolveMail(4, "handled", "retargeted to main"); err != nil {
			t.Fatal(err)
		}
	}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"futureMail"`) || !strings.Contains(string(b), `"value"`) {
		t.Fatalf("disposition dropped extension: %s", b)
	}
	m := LoadRegistryTolerant(root).Slot("ben").Inbox()[0]
	if !m.Read || m.Reason != "retargeted to main" || m.Disposition != "handled" {
		t.Fatalf("disposition not durable: %+v", m)
	}
}

func TestPromoteBundleLeadKeepsPendingRequests(t *testing.T) {
	root := inboxRoot(t)
	if _, err := UpdateSlot(root, "ben", func(s *Slot) {
		s.Bind(Binding{Task: "12", Attempt: "run"})
		s.SetBundle([]string{"15", "16"})
	}); err != nil {
		t.Fatal(err)
	}
	m, err := PostMail(root, "ben", MailReview, "REVIEW fix the bundle", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := PromoteBundleLead(root, "12"); err != nil {
		t.Fatal(err)
	}
	s := LoadRegistryTolerant(root).Slot("ben")
	got := s.Pending()
	if s.Task() != "15" || len(got) != 1 || got[0].Seq != m.Seq || got[0].Task != "15" || got[0].Read {
		t.Fatalf("promotion must keep the running assignment's request pending and unread: task %q inbox %+v", s.Task(), s.Inbox())
	}
}
