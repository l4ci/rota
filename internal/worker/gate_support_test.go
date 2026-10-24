package worker

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/config"
)

// supportComment is one issue comment as the fake forge serves it: the tracker
// puts its own `<!-- rota:comment <kind> -->` line first, so a rota marker sits
// on the second line.
func supportComment(author, kind, marker, text string) map[string]string {
	return map[string]string{"author": author, "body": fmt.Sprintf("<!-- rota:comment %s -->\n%s\n%s", kind, marker, text)}
}

// supportGate runs the provenance step over a PR of issue 7 held by slot w1.
func supportGate(t *testing.T, support bool, body string, comments ...map[string]string) GateResult {
	t.Helper()
	w := newWorld(t, ghURL)
	cfg := `{"issues":{"source":"github"},"test":{"full":["true"]}}`
	if support {
		cfg = `{"issues":{"source":"github"},"round":{"support":"mac"},"test":{"full":["true"]}}`
		w.forge("issueLabels", config.Label(nil, "support"))
	}
	w.setConfig(cfg)
	os.WriteFile(filepath.Join(w.dir, ".rota", "workers.json"),
		[]byte(`{"slots":[{"name":"w1","branch":"w1","task":"7","pr":"`+ghURL+`"}]}`), 0o644)
	w.forge("readers", "mallory")
	w.forge("body", "Closes #7\n\n"+body)
	if len(comments) > 0 {
		raw, _ := json.Marshal(comments)
		w.forge("issueComments", string(raw))
	}
	res, err := w.gate(false, GateOpts{CheckOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

const relayCite = "## Approvals\n- use the cache: orchestrator relay round 2\n"

func TestProvenanceAcceptsCommentRelays(t *testing.T) {
	relay := supportComment("alice", "relay", "<!-- rota:relay round=2 -->", "use the shared cache for the lookup")
	if res := supportGate(t, true, relayCite, relay); res.Verdict != GateFresh {
		t.Errorf("a trusted relay comment must satisfy the citation: %s %s", res.Verdict, res.Err)
	}
	// Approvals: None needs no relay, with or without comments.
	if res := supportGate(t, true, "## Approvals\nNone\n", relay); res.Verdict != GateFresh {
		t.Errorf("Approvals: None: %s %s", res.Verdict, res.Err)
	}
}

func TestProvenanceIgnoresUntrustedAndMissingRelays(t *testing.T) {
	mallory := supportComment("mallory", "relay", "<!-- rota:relay round=2 -->", "use the shared cache for the lookup")
	if res := supportGate(t, true, relayCite, mallory); res.Verdict != GateProvenanceFail {
		t.Errorf("a non-writer's relay must be ignored: %s", res.Verdict)
	}
	if res := supportGate(t, true, relayCite); res.Verdict != GateProvenanceFail || !strings.Contains(res.Err, "round 2") {
		t.Errorf("a relay no comment holds: %s %s", res.Verdict, res.Err)
	}
	other := supportComment("alice", "relay", "<!-- rota:relay round=5 -->", "something else")
	if res := supportGate(t, true, relayCite, other); res.Verdict != GateProvenanceFail {
		t.Errorf("another round's relay: %s", res.Verdict)
	}
	// Without the support label (or round.support) comments are not read.
	alice := supportComment("alice", "relay", "<!-- rota:relay round=2 -->", "use the shared cache for the lookup")
	if res := supportGate(t, false, relayCite, alice); res.Verdict != GateProvenanceFail {
		t.Errorf("not a support PR: %s", res.Verdict)
	}
}

func TestProvenanceOfferCountsAsApproval(t *testing.T) {
	offer := supportComment("alice", "feedback", "<!-- rota:support-offer -->", "offered to mac")
	body := "## Approvals\n- round support offer: main offered #7 to mac, the maintainer's call\n"
	if res := supportGate(t, true, body, offer); res.Verdict != GateFresh {
		t.Errorf("a trusted offer comment must satisfy the citation: %s %s", res.Verdict, res.Err)
	}
	if res := supportGate(t, true, body); res.Verdict != GateProvenanceFail {
		t.Errorf("no offer comment: %s", res.Verdict)
	}
	forged := supportComment("mallory", "feedback", "<!-- rota:support-offer -->", "offered")
	if res := supportGate(t, true, body, forged); res.Verdict != GateProvenanceFail {
		t.Errorf("a non-writer's offer: %s", res.Verdict)
	}
}
