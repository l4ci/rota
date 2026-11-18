package host

import "testing"

// Every pane kind's descriptor is complete: a kind added without its build,
// policy overlay or pane env var fails here, not at a nil call later.
// CapabilitiesForKind and New both read the descriptor, so they cannot drift.
func TestKindSpecsComplete(t *testing.T) {
	for _, k := range PaneKinds {
		s, ok := specOf(k)
		if !ok || s.build == nil || s.policy == nil {
			t.Fatalf("%q: descriptor missing build or policy", k)
		}
		if got := New(k, Deps{}).Kind(); got != k {
			t.Errorf("%q: build returns kind %q", k, got)
		}
		if k != HerdrCore && (s.paneEnv == "" || s.binary == "" || s.pickup.attempts == 0) {
			t.Errorf("%q: pane env, binary or pickup policy unset", k)
		}
	}
	if _, ok := specOf(Solo); ok {
		t.Error("Solo must have no adapter descriptor")
	}
}

func TestKindPolicy(t *testing.T) {
	h := CapabilitiesForKind(Herdr)
	if h.InSessionRule == nil || h.InSessionRule.Env != "HERDR_ENV=1" || !h.LaunchKindBound || !h.NoNominalHandle {
		t.Errorf("herdr policy = %+v", h)
	}
	if CapabilitiesForKind(Tmux).NoNominalHandle || CapabilitiesForKind(Tmux).InSessionRule != nil {
		t.Error("tmux must have a nominal handle and no in-session rule")
	}
	if !CapabilitiesForKind(HerdrCore).InPlace || CapabilitiesForKind(Herdr).InPlace {
		t.Error("only core runs the orchestrator in place")
	}
	for comm, want := range map[string]bool{"herdr": true, "tmux": true, "tmux: server": true, "tmux:client": true, "herdr: x": false, "bash": false, "": false} {
		got := false
		for _, k := range PaneKinds {
			got = got || CapabilitiesForKind(k).Server.Match(comm)
		}
		if got != want {
			t.Errorf("server match %q = %v, want %v", comm, got, want)
		}
	}
	argv := CapabilitiesForKind(Tmux).Attach("rota-x", "/p", "orch", "run")
	if argv[0] != "tmux" || argv[len(argv)-1] != `run; exec "${SHELL:-sh}"` {
		t.Errorf("tmux attach = %q", argv)
	}
}
