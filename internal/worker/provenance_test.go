package worker

import (
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/jsonx"
)

func TestApprovalsSection(t *testing.T) {
	for in, want := range map[string]string{
		"## Approvals\na\nb\n## X\nc": "a\nb",
		"intro\n##  Approvals  \na":   "a",
		"## Approvalsx\na":            "",
		"## Approvals\n##\nc":         "",
		"## Approvals\n### Sub\nc":    "### Sub\nc",
		"no heading":                  "",
	} {
		got, found := approvalsSection(in)
		if strings.TrimRight(got, "\n") != want || (want != "" && !found) {
			t.Errorf("approvalsSection(%q) = %q, %v; want %q", in, got, found, want)
		}
	}
}

func relayLog(round int, summary string) []any {
	e := jsonx.NewObject()
	e.Set("round", round)
	e.Set("summary", summary)
	return []any{e}
}

func TestCheckApprovals(t *testing.T) {
	relayed := relayLog(3, "use the shared cache for the lookup")
	for _, c := range []struct {
		name   string
		body   string
		relays []any
		want   string // substring of the failure; "" means pass
	}{
		{"no relays, no section", "just a body", nil, ""},
		{"no section while relays exist", "just a body", relayed, "1 relay(s) logged but the PR body has no ## Approvals section"},
		{"deflation: round never sent", "## Approvals\n- x: orchestrator relay round 5\n", relayed, "relay for round 5 but none is logged"},
		{"deflation: unnumbered cite, none logged", "## Approvals\n- orchestrator relay said so\n", nil, "orchestrator relay but none is logged"},
		{"honest cite of a logged round", "## Approvals\n- x: orchestrator relay round 3\n", relayed, ""},
		{"inflation: relayed text cited as maintainer", "## Approvals\n- the maintainer: use the shared cache for the lookup\n", relayed, "cites the maintainer for text the orchestrator relayed (round 3)"},
		{"maintainer line unrelated to any relay", "## Approvals\n- maintainer in pane: rename the flag\n", relayed, ""},
		{"short summary is too weak to match", "## Approvals\n- the maintainer said ok\n", relayLog(1, "ok"), ""},
		{"heading is case-insensitive", "## approvals\n- x: orchestrator relay round 9\n", relayed, "round 9"},
		{"### does not end the section", "## Approvals\n### detail\n- orchestrator relay round 9\n", relayed, "round 9"},
		{"cite after the next ## is ignored", "## Approvals\n- ok\n## Notes\n- orchestrator relay round 9\n", relayed, ""},
		{"non-object relay entries are skipped", "just a body", []any{"junk"}, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := checkApprovals(c.body, c.relays)
			if c.want == "" && got != "" || !strings.Contains(got, c.want) {
				t.Errorf("checkApprovals = %q, want %q", got, c.want)
			}
		})
	}
}

func TestNewRelayEntry(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 30, 0, 0, time.FixedZone("x", 3600))
	e := newRelayEntry(4, now, "--- ORCHESTRATOR (round 4) ---\n\n  do the thing  \nmore")
	if got := jsonx.Str(e, "summary"); got != "do the thing" {
		t.Errorf("summary = %q", got)
	}
	if got := jsonx.Str(e, "ts"); got != "2026-10-05T11:30:00Z" {
		t.Errorf("ts = %q", got)
	}
	if v, _ := e.Get("round"); v != 4 {
		t.Errorf("round = %v", v)
	}
}
