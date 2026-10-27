package backlog

import (
	"errors"
	"strings"
	"testing"
)

func TestFileAcceptancePreflightIsReadOnly(t *testing.T) {
	body := "## Acceptance\n- [ ] AC-1: ready\n"
	f, _ := proj(t, map[string]string{".rota/BACKLOG.md": base, ".rota/bugs/B01.md": body})
	before := read(t, f, ".rota/BACKLOG.md")
	in := CompleteInput{Reason: "done", NoProof: true}
	var refusal *RefusedError
	if err := f.CheckAcceptance("B01", in); !errors.As(err, &refusal) || strings.Contains(refusal.Hint, "plan pass") {
		t.Fatalf("preflight refusal: %v", err)
	}
	in.UnmetReason = "Accepted findings"
	if err := f.CheckAcceptance("B01", in); err != nil {
		t.Fatal(err)
	}
	if read(t, f, ".rota/BACKLOG.md") != before || read(t, f, ".rota/bugs/B01.md") != body {
		t.Fatal("preflight wrote completion data")
	}
}

func TestUmbrellaAcceptancePreflightUsesOwner(t *testing.T) {
	is := typed(1, "task", "Review")
	is.Body = "## Acceptance\n- [ ] AC-1: ready\n"
	u, remotes, _ := newUmbrella(t, `{}`, map[string][]Issue{"web": {is}}, "web")
	var refusal *RefusedError
	if err := u.CheckAcceptance("web:1", CompleteInput{Reason: "done"}); !errors.As(err, &refusal) {
		t.Fatalf("owner acceptance was skipped: %v", err)
	}
	if err := u.CheckAcceptance("web:1", CompleteInput{Reason: "done", UnmetReason: "Accepted findings"}); err != nil {
		t.Fatal(err)
	}
	if remotes["web"].Issues[0].State != "open" || len(remotes["web"].Issues[0].Comments) != 0 {
		t.Fatal("preflight mutated owner issue")
	}
}
