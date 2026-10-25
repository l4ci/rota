package backlog

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestClaimLabel(t *testing.T) {
	for _, tc := range []struct{ id, cfg, want string }{
		{"dana@3", `{}`, "claimed:dana@main"},
		{"mac-ben@3", `{"round":{"support":"mac"}}`, "claimed:ben@mac"},
		{"dana@3", `{"round":{"support":"mac","role":"support"}}`, "claimed:dana@main"},
		{"mac-ben@3", `{}`, "claimed:mac-ben@main"},
		{strings.Repeat("a", 37) + "@3", `{}`, "claimed:" + strings.Repeat("a", 37) + "@main"},
		{strings.Repeat("a", 38) + "@3", `{}`, ""},
		{"alice", `{}`, ""}, {"x@y", `{}`, ""}, {"a@-1", `{}`, ""},
		{"A@3", `{}`, ""}, {"mac-@3", `{"round":{"support":"mac"}}`, ""},
	} {
		t.Run(tc.id+tc.cfg, func(t *testing.T) {
			got, ok := ClaimLabel(mustDecode(t, tc.cfg), tc.id)
			if got != tc.want || ok != (tc.want != "") {
				t.Fatalf("ClaimLabel = %q, %v; want %q, %v", got, ok, tc.want, tc.want != "")
			}
		})
	}
}

func TestIssuesClaimLabel(t *testing.T) {
	b, tr := newIssues(t, `{}`, open(1))
	if won, _, err := b.ClaimShared("1", "dana@3", 1); err != nil || !won {
		t.Fatalf("claim: won=%v err=%v", won, err)
	}
	if got := tr.Issues[0].Labels; !slices.Equal(got, []string{"in-progress", "claimed:dana@main"}) {
		t.Fatalf("labels after claim: %v want [in-progress claimed:dana@main]", got)
	}
	if won, holder, err := b.ClaimShared("1", "ben@3", 1); err != nil || won || holder != "dana@3" {
		t.Fatalf("losing claim: won=%v holder=%q err=%v", won, holder, err)
	}
	if got := tr.Issues[0].Labels; !slices.Equal(got, []string{"in-progress", "claimed:dana@main"}) {
		t.Fatalf("labels after lost race: %v", got)
	}
	if changed, err := b.Release("1", "dana@3"); err != nil || !changed {
		t.Fatalf("release: changed=%v err=%v", changed, err)
	}
	if got := tr.Issues[0].Labels; len(got) != 0 {
		t.Fatalf("labels after release: %v want []", got)
	}
	tr.Calls = nil
	if changed, err := b.Release("1", "dana@3"); err != nil || changed {
		t.Fatalf("second release: changed=%v err=%v", changed, err)
	}
	for _, c := range tr.Calls {
		if c.Method != "get" && c.Method != "comments" {
			t.Fatalf("second release wrote: %+v", c)
		}
	}
}

func TestIssuesClaimLabelShared(t *testing.T) {
	b, tr := newIssues(t, `{"round":{"support":"mac"}}`, open(1, "keep"))
	for _, id := range []string{"dana@3", "mac-ben@3"} {
		if won, _, err := b.ClaimShared("1", id, 2); err != nil || !won {
			t.Fatalf("claim %s: won=%v err=%v", id, won, err)
		}
	}
	if won, _, err := b.ClaimShared("1", "kit@3", 2); err != nil || won {
		t.Fatalf("third claim: won=%v err=%v", won, err)
	}
	if got := tr.Issues[0].Labels; !slices.Equal(got, []string{"keep", "in-progress", "claimed:dana@main", "claimed:ben@mac"}) {
		t.Fatalf("shared labels: %v", got)
	}
	if changed, err := b.Release("1", "dana@3"); err != nil || !changed {
		t.Fatalf("release: changed=%v err=%v", changed, err)
	}
	if got := tr.Issues[0].Labels; !slices.Equal(got, []string{"keep", "in-progress", "claimed:ben@mac"}) {
		t.Fatalf("remaining claim labels: %v", got)
	}
}

func TestIssuesClaimLabelFailureWarns(t *testing.T) {
	for _, cfg := range []string{`{}`, `{"issues":{"autoCreateLabel":false}}`} {
		b, tr := newIssues(t, cfg, open(1))
		tr.Fail = map[string]error{"add_labels": errors.New("label unavailable")}
		var warnings []string
		b.Warn = func(msg string) { warnings = append(warnings, msg) }
		if won, _, err := b.Claim("1", "dana@3"); err != nil || !won {
			t.Fatalf("label failure lost claim: won=%v err=%v", won, err)
		}
		st, err := b.Status("1")
		if err != nil || st.Claim != "dana@3" || len(warnings) != 1 || !strings.Contains(warnings[0], "label unavailable") {
			t.Fatalf("status=%+v err=%v warnings=%v", st, err, warnings)
		}
		for _, c := range tr.Calls {
			if c.Method == "add_labels" && c.Args[2] != b.AutoCreate() {
				t.Fatalf("autoCreate flag = %v, want %v", c.Args[2], b.AutoCreate())
			}
		}
	}

	b, tr := newIssues(t, `{}`, open(1))
	if won, _, err := b.Claim("1", "dana@3"); err != nil || !won {
		t.Fatal(won, err)
	}
	tr.Fail = map[string]error{"remove_labels": errors.New("label unavailable")}
	var warnings []string
	b.Warn = func(msg string) { warnings = append(warnings, msg) }
	// The existing in-progress removal still returns its error, but a failed
	// visible marker never prevents writing the authoritative release.
	if changed, _ := b.Release("1", "dana@3"); !changed {
		t.Fatal("release not recorded")
	}
	st, err := b.Status("1")
	if err != nil || st.Claim != "" || len(warnings) != 1 {
		t.Fatalf("status=%+v err=%v warnings=%v", st, err, warnings)
	}
}
