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
