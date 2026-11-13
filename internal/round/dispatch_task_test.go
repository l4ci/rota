package round

import (
	"errors"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/worker"
)

func cfgOf(t *testing.T, s string) any {
	t.Helper()
	v, err := jsonx.Decode([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestRecoveryAssigned(t *testing.T) {
	f := newAssignFixture(t)
	if _, err := f.assign("12", "ben", nil); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		o    worker.DispatchOpts
		want bool
	}{
		{"held task", worker.DispatchOpts{Slot: "ben", Task: "12"}, true},
		{"relay", worker.DispatchOpts{Slot: "ben", Task: "12", Relay: true}, false},
		{"no task", worker.DispatchOpts{Slot: "ben"}, false},
		{"other task", worker.DispatchOpts{Slot: "ben", Task: "13"}, false},
		{"unknown slot", worker.DispatchOpts{Slot: "zed", Task: "12"}, false},
	} {
		got, err := RecoveryAssigned(f.root, tc.o)
		if err != nil || got != tc.want {
			t.Errorf("%s: got %v, %v; want %v", tc.name, got, err, tc.want)
		}
	}
}

func TestRecoveryBriefAppendsNotes(t *testing.T) {
	f := newAssignFixture(t)
	if _, err := f.assign("12", "ben", nil); err != nil {
		t.Fatal(err)
	}
	text, o, err := f.env.RecoveryBrief(bg, f.root, f.be, cfgOf(t, `{}`), worker.DispatchOpts{Slot: "ben", Task: "12"}, "restart from the plan")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "contract") || !strings.HasSuffix(text, "\nNotes appended for recovery:\n\nrestart from the plan") {
		t.Errorf("brief lacks the rebuilt body or notes:\n%s", text)
	}
	if o.Task != "12" || o.Branch == "" {
		t.Errorf("rebuilt opts lost the binding: %+v", o)
	}
}

func TestRecoveryBriefBadConfig(t *testing.T) {
	f := newAssignFixture(t)
	if _, err := f.assign("12", "ben", nil); err != nil {
		t.Fatal(err)
	}
	_, _, err := f.env.RecoveryBrief(bg, f.root, f.be, cfgOf(t, `{"round":{"scope":"nonsense"}}`), worker.DispatchOpts{Slot: "ben", Task: "12"}, "")
	var ce *ConfigError
	if !errors.As(err, &ce) {
		t.Fatalf("want ConfigError, got %v", err)
	}
}

func TestSupportRelay(t *testing.T) {
	f := newAssignFixture(t)
	if _, err := f.assign("12", "ben", nil); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, cfg string
		post      bool
	}{
		{"support machine", `{"round":{"support":"mac","role":"support"}}`, true},
		{"main machine", `{"round":{"support":"mac","role":"main"}}`, false},
		{"no support name", `{"round":{"role":"support"}}`, false},
	} {
		issue, body, post, err := SupportRelay(f.root, cfgOf(t, tc.cfg), "ben", 7, "  hello  ", "")
		if err != nil || post != tc.post {
			t.Fatalf("%s: post=%v err=%v", tc.name, post, err)
		}
		if tc.post && (issue != "12" || body != "<!-- rota:relay round=7 -->\nhello") {
			t.Errorf("%s: issue %q body %q", tc.name, issue, body)
		}
	}
	if _, _, post, _ := SupportRelay(f.root, cfgOf(t, `{"round":{"support":"mac","role":"support"}}`), "zed", 7, "x", ""); post {
		t.Error("slot without a held issue must not post")
	}
}

func TestSupportRelayExplicitPR(t *testing.T) {
	f := newAssignFixture(t)
	if _, err := f.assign("12", "ben", nil); err != nil {
		t.Fatal(err)
	}
	if err := worker.Update(f.root, func(d *worker.Doc) {
		d.QueuePR(worker.QueuedPR{From: "ben", Issue: "11", PR: "https://github.com/o/r/pull/7"})
	}); err != nil {
		t.Fatal(err)
	}
	issue, body, post, err := SupportRelay(f.root, cfgOf(t, `{"round":{"support":"mac","role":"support"}}`), "ben", 7, "hello", "7")
	if err != nil || !post || issue != "11" || body != "<!-- rota:relay round=7 -->\nhello" {
		t.Fatalf("explicit PR support relay: issue=%q body=%q post=%v err=%v", issue, body, post, err)
	}
}
