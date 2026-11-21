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

func TestCompletionPreflightOrderAndGate(t *testing.T) {
	errAcc := errors.New("acceptance")
	cases := []struct {
		name       string
		in         CompleteInput
		accErr     error
		count      int
		wantProofs int
		wantErr    string
	}{
		{"done without proof refuses", CompleteInput{Reason: "done"}, nil, 0, 1, "no proof recorded"},
		{"done with proof passes", CompleteInput{Reason: "done"}, nil, 1, 1, ""},
		{"no-proof skips the count", CompleteInput{Reason: "done", NoProof: true}, nil, 0, 0, ""},
		{"non-done skips the count", CompleteInput{Reason: "wontfix"}, nil, 0, 0, ""},
		{"acceptance error wins before the count", CompleteInput{Reason: "done"}, errAcc, 0, 0, "acceptance"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			proofs := 0
			rec, err := completionPreflight("42", c.in,
				func() (string, error) { return "RECORD", c.accErr },
				func() (int, error) { proofs++; return c.count, nil })
			if proofs != c.wantProofs {
				t.Fatalf("proof count calls = %d, want %d", proofs, c.wantProofs)
			}
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("err = %v, want %q", err, c.wantErr)
				}
				return
			}
			if err != nil || rec != "RECORD" {
				t.Fatalf("got (%q, %v), want (RECORD, nil)", rec, err)
			}
		})
	}
}
