package worker

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A bundled slot's PR must close the lead and every member: a body that leaves
// one member out is refused, naming it, before anything merges.
func TestGateClosesEveryBundledIssue(t *testing.T) {
	cases := []struct {
		name, body string
		missing    []string // the refusal names these, in this order; nil passes
	}{
		{name: "member only referenced", body: "Closes #5\nRefs #6\n", missing: []string{"#6"}},
		{name: "lead and member missing", body: "Adds the thing.\n", missing: []string{"#5", "#6", "#7"}},
		{name: "second member missing", body: "Closes #5\nCloses #6\n", missing: []string{"#7"}},
		{name: "all closed", body: "Closes #5\nCloses #6\nCloses #7\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t, ghURL)
			os.WriteFile(filepath.Join(w.dir, ".rota", "workers.json"),
				[]byte(fmt.Sprintf(`{"slots":[{"name":"w1","branch":"w1","task":"#5","pr":%q,"bundle":["#6","#7"]}]}`, ghURL)), 0o644)
			w.setPRBody(c.body)
			res, err := w.gate(false, GateOpts{CheckOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			if c.missing == nil {
				if res.Verdict != GateFresh {
					t.Fatalf("verdict %q: %+v", res.Verdict, res)
				}
				return
			}
			if res.Verdict != GateNotClosing || res.Changed {
				t.Fatalf("verdict %q, want %s: %+v", res.Verdict, GateNotClosing, res)
			}
			if want := "does not close " + strings.Join(c.missing, ", ") + ","; !strings.Contains(res.Err, want) {
				t.Errorf("message %q lacks %q", res.Err, want)
			}
			for _, m := range c.missing {
				if !strings.Contains(res.Hint, "Closes "+m) {
					t.Errorf("hint %q lacks `Closes %s`", res.Hint, m)
				}
			}
		})
	}
}

// A member closed since the assign leaves the bundle: the body need not close it.
func TestGateClosesSkipsAClosedMember(t *testing.T) {
	w := newWorld(t, ghURL)
	os.WriteFile(filepath.Join(w.dir, ".rota", "workers.json"),
		[]byte(fmt.Sprintf(`{"slots":[{"name":"w1","branch":"w1","task":"#5","pr":%q,"bundle":["#6","#7"]}]}`, ghURL)), 0o644)
	// the fake forge answers every Get with this state; the body closes #5 and #7
	w.forge("issueState", "closed")
	w.setPRBody("Closes #5\nCloses #7\n")
	res, err := w.gate(false, GateOpts{CheckOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != GateFresh {
		t.Fatalf("verdict %q: %+v", res.Verdict, res)
	}
}

func TestGateClosesAfterBundleLeadPromotion(t *testing.T) {
	for _, queued := range []bool{false, true} {
		for _, tc := range []struct {
			name, body, verdict, missing string
		}{
			{"remaining only", "Closes #6\nCloses #7\n", GateFresh, ""},
			{"new lead missing", "Closes #7\n", GateNotClosing, "#6"},
			{"member missing", "Closes #6\n", GateNotClosing, "#7"},
		} {
			t.Run(fmt.Sprintf("queued=%t/%s", queued, tc.name), func(t *testing.T) {
				w := newWorld(t, ghURL)
				branch := "ben/5-bundle"
				gitq(t, w.worker, "push", "-q", "origin", "w1:refs/heads/"+branch)
				w.forge("head", branch)
				reg := fmt.Sprintf(`{"slots":[{"name":"w1","branch":%q,"task":"5","pr":%q,"bundle":["6","7"]}]}`, branch, ghURL)
				if queued {
					reg = fmt.Sprintf(`{"slots":[],"prs":[{"from":"w1","branch":%q,"issue":"5","pr":%q,"bundle":["6","7"]}]}`, branch, ghURL)
				}
				if err := os.WriteFile(filepath.Join(w.dir, ".rota", "workers.json"), []byte(reg), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := PromoteBundleLead(w.dir, "5"); err != nil {
					t.Fatal(err)
				}
				w.setPRBody(tc.body)
				res, err := w.env(false).Gate(bg, w.dir, GateOpts{Slot: ghURL, Base: "main", CheckOnly: true})
				if err != nil || res.Verdict != tc.verdict || res.Issue != "6" {
					t.Fatalf("gate on original branch: %+v error %v", res, err)
				}
				if tc.missing != "" && !strings.Contains(res.Err, "does not close "+tc.missing+",") {
					t.Errorf("missing remaining issue: %s", res.Err)
				}
			})
		}
	}
}
