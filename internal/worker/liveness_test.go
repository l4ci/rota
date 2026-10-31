package worker

import (
	"testing"

	"github.com/l4ci/rota/internal/host"
)

// One table over every host kind: the verdict differs only through the host's
// capabilities, never through the kind.
func TestJudgeLivenessAcrossHosts(t *testing.T) {
	const H = "tab-1"
	cases := []struct {
		name string
		ev   LivenessEvidence
		// want per kind; "" means the same as herdr
		herdr, orca string
		reason      string // orca's reason
	}{
		{"listed", LivenessEvidence{Answered: true, Listed: true, Handle: H}, "alive", "alive", ""},
		{"absent with handle", LivenessEvidence{Answered: true, Handle: H}, "dead", "unverifiable", "stale-handle"},
		{"absent recorded dead", LivenessEvidence{Answered: true, Handle: H, State: "DEAD"}, "dead", "dead", ""},
		{"absent no handle", LivenessEvidence{Answered: true}, "alive", "alive", ""},
		{"host unanswered", LivenessEvidence{Handle: H}, "alive", "unverifiable", "stale-handle"},
		{"unanswered last poll unverifiable", LivenessEvidence{Handle: H, State: "unverifiable", Reason: "host-unreachable"}, "unverifiable", "unverifiable", "stale-handle"},
		{"unanswered unverifiable unrecorded", LivenessEvidence{State: "unverifiable"}, "unverifiable", "unverifiable", "unrecorded"},
		{"unanswered recorded dead", LivenessEvidence{Handle: H, State: "dead"}, "dead", "dead", ""},
	}
	for _, kind := range []host.Kind{host.Herdr, host.Tmux, host.Orca, host.Solo} {
		caps := host.CapabilitiesForKind(kind)
		for _, c := range cases {
			want := c.herdr
			if kind == host.Orca {
				want = c.orca
			}
			ev := c.ev
			ev.Caps = caps
			got := JudgeLiveness(ev)
			if got.State != want {
				t.Errorf("%s/%s: state %q, want %q", kind, c.name, got.State, want)
			}
			if kind == host.Orca && c.orca == "unverifiable" && got.Reason != c.reason {
				t.Errorf("%s/%s: reason %q, want %q", kind, c.name, got.Reason, c.reason)
			}
		}
	}
}

func TestCapabilitiesForKind(t *testing.T) {
	for _, k := range []host.Kind{host.Herdr, host.Tmux, host.Solo} {
		if c := host.CapabilitiesForKind(k); c.RuntimeScopedHandles || c.TreeKillClose {
			t.Errorf("%s: unexpected orca capabilities %+v", k, c)
		}
	}
	if c := host.CapabilitiesForKind(host.Orca); !c.RuntimeScopedHandles || !c.TreeKillClose {
		t.Errorf("orca lacks its capabilities: %+v", c)
	}
}

// The real adapters declare the same capabilities the fake table is built on.
func TestRealAdaptersDeclareLivenessCapabilities(t *testing.T) {
	for _, k := range []host.Kind{host.Herdr, host.Tmux, host.Orca} {
		if got, want := host.New(k, host.Deps{}).Capabilities(), host.CapabilitiesForKind(k); got.RuntimeScopedHandles != want.RuntimeScopedHandles || got.TreeKillClose != want.TreeKillClose {
			t.Errorf("%s: adapter %+v, kind table %+v", k, got, want)
		}
	}
}
