package backlog

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/tracker"
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

func TestIssuesCompleteDropsClaimLabel(t *testing.T) {
	for _, reason := range []string{"done", "dropped", "handed-off"} {
		t.Run(reason, func(t *testing.T) {
			b, tr := newIssues(t, `{}`, open(1, "in-progress", "claimed:dana@main", "claimed:ben@mac", "keep"))
			if changed, err := b.Complete("1", CompleteInput{Reason: reason, NoProof: true, Commit: "abc"}); err != nil || !changed {
				t.Fatalf("complete: changed=%v err=%v", changed, err)
			}
			if got := tr.Issues[0]; got.State != "closed" || !slices.Equal(got.Labels, []string{"keep"}) {
				t.Fatalf("after complete: state=%s labels=%v want closed [keep]", got.State, got.Labels)
			}
		})
	}
}

func TestClaimLabelDrift(t *testing.T) {
	for _, tc := range []struct {
		name, state                         string
		labels, comments, add, remove, want []string
	}{
		{"missing", "open", []string{"keep"}, []string{"<!-- rota:claim dana@3 -->"}, []string{"claimed:dana@main"}, nil, []string{"keep", "claimed:dana@main"}},
		{"wrong agent", "open", []string{"claimed:ben@main", "keep"}, []string{"<!-- rota:claim dana@3 -->"}, []string{"claimed:dana@main"}, []string{"claimed:ben@main"}, []string{"keep", "claimed:dana@main"}},
		{"released", "open", []string{"claimed:dana@main", "keep"}, []string{"<!-- rota:claim dana@3 -->", "<!-- rota:release dana@3 -->"}, nil, []string{"claimed:dana@main"}, []string{"keep"}},
		{"shared", "open", []string{"claimed:dana@main", "keep"}, []string{"<!-- rota:claim dana@3 -->", "<!-- rota:claim mac-ben@3 -->"}, []string{"claimed:ben@mac"}, nil, []string{"claimed:dana@main", "keep", "claimed:ben@mac"}},
		{"closed", "closed", []string{"claimed:dana@main", "keep"}, []string{"<!-- rota:claim dana@3 -->"}, nil, []string{"claimed:dana@main"}, []string{"keep"}},
		{"in sync", "open", []string{"claimed:dana@main", "keep"}, []string{"<!-- rota:claim dana@3 -->"}, nil, nil, []string{"claimed:dana@main", "keep"}},
		{"legacy", "open", []string{"keep"}, []string{"<!-- rota:claim alice -->"}, nil, nil, []string{"keep"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			is := open(1, tc.labels...)
			is.State = tc.state
			for i, body := range tc.comments {
				is.Comments = append(is.Comments, tracker.Comment{ID: string(rune('1' + i)), Body: body})
			}
			b, tr := newIssues(t, `{"round":{"support":"mac"}}`, is)
			before := slices.Clone(is.Comments)
			add, remove, err := b.ClaimLabelDrift("1")
			if err != nil || !slices.Equal(add, tc.add) || !slices.Equal(remove, tc.remove) {
				t.Fatalf("drift: add=%v remove=%v err=%v; want %v %v", add, remove, err, tc.add, tc.remove)
			}
			changed, err := b.SyncClaimLabels("1")
			if err != nil || changed != (len(tc.add)+len(tc.remove) > 0) {
				t.Fatalf("sync: changed=%v err=%v", changed, err)
			}
			if got := tr.Issues[0].Labels; !slices.Equal(got, tc.want) {
				t.Fatalf("labels=%v want %v", got, tc.want)
			}
			if !reflect.DeepEqual(before, tr.Issues[0].Comments) {
				t.Fatal("sync edited comment claims")
			}
			if changed, err := b.SyncClaimLabels("1"); changed || err != nil {
				t.Fatalf("second sync: changed=%v err=%v", changed, err)
			}
		})
	}
}

func TestClaimLabelDriftReadFailure(t *testing.T) {
	b, tr := newIssues(t, `{}`, open(1, "claimed:ben@main"))
	tr.Fail = map[string]error{"comments": errors.New("unreadable claims")}
	if changed, err := b.SyncClaimLabels("1"); changed || err == nil {
		t.Fatalf("unreadable claims: changed=%v err=%v", changed, err)
	}
	if !slices.Equal(tr.Issues[0].Labels, []string{"claimed:ben@main"}) {
		t.Fatalf("guessed a repair: %v", tr.Issues[0].Labels)
	}
}

func TestUmbrellaClaimLabelDrift(t *testing.T) {
	u, remotes, _ := newUmbrella(t, `{}`, map[string][]Issue{
		"web": {open(1, "claimed:ben@main")}, "api": {open(1, "claimed:kit@main")},
	}, "web", "api")
	remotes["web"].Issues[0].Comments = []tracker.Comment{{ID: "1", Body: "<!-- rota:claim dana@3 -->"}}
	add, remove, err := u.ClaimLabelDrift("web:1")
	if err != nil || !slices.Equal(add, []string{"claimed:dana@main"}) || !slices.Equal(remove, []string{"claimed:ben@main"}) {
		t.Fatalf("drift: %v %v %v", add, remove, err)
	}
	if changed, err := u.SyncClaimLabels("web:1"); err != nil || !changed {
		t.Fatalf("sync: %v %v", changed, err)
	}
	if !slices.Equal(remotes["web"].Issues[0].Labels, []string{"claimed:dana@main"}) || !slices.Equal(remotes["api"].Issues[0].Labels, []string{"claimed:kit@main"}) {
		t.Fatal("repair reached wrong tracker")
	}
}
