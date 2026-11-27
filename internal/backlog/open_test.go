package backlog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// openIssueModeBackend opens root in issue mode over tr, collecting notices.
func openIssueModeBackend(t *testing.T, root string, tr Tracker) (Backend, *[]string) {
	t.Helper()
	var got []string
	be, err := Open(context.Background(), root, mustDecode(t, `{"issues":{"source":"github"}}`), Options{
		CountProof: stubCountProof,
		NewTracker: func(context.Context, string) (Tracker, error) { return tr, nil },
		Warn:       func(msg string) { got = append(got, msg) },
	})
	if err != nil {
		t.Fatal(err)
	}
	return be, &got
}

// claimWithFailingLabel claims ref while the claim label cannot be added: the
// claim wins and the failure arrives as one notice.
func claimWithFailingLabel(t *testing.T, be Backend, got *[]string, ref string) {
	t.Helper()
	won, _, err := be.Claim(ref, "dana@3")
	if err != nil || !won {
		t.Fatalf("claim %s: won=%v err=%v", ref, won, err)
	}
	if len(*got) != 1 || !strings.Contains((*got)[0], "label unavailable") {
		t.Errorf("claim %s: Options.Warn got %q", ref, *got)
	}
}

func TestOpenIssuesRaisesNoticesIntoOptionsWarn(t *testing.T) {
	tr := &fakeTracker{Issues: []Issue{open(1)}, Fail: map[string]error{"add_labels": errors.New("label unavailable")}}
	be, got := openIssueModeBackend(t, t.TempDir(), tr)
	claimWithFailingLabel(t, be, got, "1")
}

func TestOpenUmbrellaRaisesNoticesIntoOptionsWarn(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".rota"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".rota", "repos.json"), []byte(`{"repos":[{"name":"web","path":"web"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	tr := &fakeTracker{Issues: []Issue{open(1)}, Fail: map[string]error{"add_labels": errors.New("label unavailable")}}
	be, got := openIssueModeBackend(t, root, tr)
	if _, ok := be.(*Umbrella); !ok {
		t.Fatalf("Open built %T, want *Umbrella", be)
	}
	claimWithFailingLabel(t, be, got, "web:1")
}

func TestOpenFileRaisesNoticesIntoOptionsWarn(t *testing.T) {
	// M00.md claims id M07, so syncing M07 after the create fails.
	seeded, _ := proj(t, rotaFiles(map[string]string{".rota/milestones/M00.md": mustMsStub("M07", "Wrong id", "s", nil, "2026-01-02")}))
	var got []string
	be, err := Open(context.Background(), seeded.Root, mustDecode(t, `{}`), Options{Warn: func(msg string) { got = append(got, msg) }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := be.Create(CreateInput{Kind: "bugs", Title: "Crash on save", Tag: "P1", Fields: []Field{{"Milestone", "M01"}}}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !strings.Contains(got[0], "M07") {
		t.Errorf("Options.Warn got %q", got)
	}
}
