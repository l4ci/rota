package hook

import (
	"testing"

	"github.com/l4ci/rota/internal/keepstate"
)

func TestHookHandoffReasonsByteExact(t *testing.T) {
	cases := []struct {
		name, got, want string
	}{
		{
			name: "context",
			got:  blockReason(80, 75, "/handoff 100%.md"),
			want: "Context is at 80%, at or above the handoff threshold of 75%. " +
				"Write the handoff for the next orchestrator session to /handoff 100%.md now: first line \"<!-- rota-handoff: orchestrator -->\", " +
				"second line \"<!-- written <UTC timestamp, YYYY-MM-DDTHH:MM:SSZ> -->\", then the body from " +
				"references/handoff-template.md with a \"## Round state\" section (slots, who holds what, merges pending, escalations open). " +
				"Commit nothing you do not own. Then run /exit.",
		},
		{
			name: "usage without reset",
			got:  usageReason(keepstate.UsageHandoff{UsedPct: 91.5, Window: WindowFiveHour}, 90, "/handoff.md"),
			want: "Usage is at 91.5% of the five_hour window, at or above the switch threshold of 90%. " +
				"The next orchestrator session may run under another account. " +
				"Write the handoff for it to /handoff.md now: first line \"<!-- rota-handoff: orchestrator -->\", " +
				"second line \"<!-- written <UTC timestamp, YYYY-MM-DDTHH:MM:SSZ> -->\", then the body from " +
				"references/handoff-template.md with a \"## Round state\" section (slots, who holds what, merges pending, escalations open). " +
				"Commit nothing you do not own. Then run /exit.",
		},
		{
			name: "usage with reset",
			got: usageReason(keepstate.UsageHandoff{
				UsedPct: 92, Window: WindowSevenDay, ResetsAt: "2026-11-13T12:00:00Z",
			}, 90, "/handoff.md"),
			want: "Usage is at 92% of the seven_day window, resets 2026-11-13T12:00:00Z, at or above the switch threshold of 90%. " +
				"The next orchestrator session may run under another account. " +
				"Write the handoff for it to /handoff.md now: first line \"<!-- rota-handoff: orchestrator -->\", " +
				"second line \"<!-- written <UTC timestamp, YYYY-MM-DDTHH:MM:SSZ> -->\", then the body from " +
				"references/handoff-template.md with a \"## Round state\" section (slots, who holds what, merges pending, escalations open). " +
				"Commit nothing you do not own. Then run /exit.",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("reason bytes differ:\n got: %q\nwant: %q", tc.got, tc.want)
			}
		})
	}
}
