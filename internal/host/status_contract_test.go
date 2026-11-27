package host

import "testing"

// Native status must distinguish a vanished session from an unanswered host,
// whichever adapter owns the session.
func TestStatusVanishedSessionAcrossNativeAdapters(t *testing.T) {
	cases := map[string]func(*testing.T) (Host, string){
		"herdr": func(t *testing.T) (Host, string) {
			f := &fake{handler: func(string, []string) Result {
				return Result{ExitCode: 1, Stderr: herdrErr("agent_not_found")}
			}}
			return New(Herdr, deps(f, herdrEnv, &clock{})), "w9:t7"
		},
		"herdr-core": func(t *testing.T) (Host, string) {
			h, f := coreFixture(t)
			s := coreSpawn(t, h)
			f.missing = true
			return h, s
		},
	}
	for name, mk := range cases {
		t.Run(name, func(t *testing.T) {
			h, handle := mk(t)
			if got := h.Status(bg, "ben", handle); got != (AgentStatus{State: StatusGone}) {
				t.Errorf("Status = %v; want gone", got)
			}
		})
	}
}

func TestStatusAcrossNativeAdapters(t *testing.T) {
	for _, state := range []string{"working", "idle", "blocked", "unknown"} {
		cases := map[string]func(*testing.T) (Host, string){
			"herdr": func(t *testing.T) (Host, string) {
				f := &fake{handler: func(string, []string) Result {
					return Result{Stdout: agentJSON(state)}
				}}
				return New(Herdr, deps(f, herdrEnv, &clock{})), "w9:t7"
			},
			"herdr-core": func(t *testing.T) (Host, string) {
				h, f := coreFixture(t)
				s := coreSpawn(t, h)
				f.status = state
				return h, s
			},
		}
		for name, mk := range cases {
			t.Run(name+"/"+state, func(t *testing.T) {
				h, handle := mk(t)
				if got := h.Status(bg, "ben", handle); got != (AgentStatus{State: state}) {
					t.Errorf("Status = %+v; want state %q without a reason", got, state)
				}
			})
		}
	}
}

func TestStatusUnverifiableEvidence(t *testing.T) {
	type statusCase struct {
		makeHost func(*testing.T) (Host, string)
		want     AgentStatus
	}
	cases := map[string]statusCase{}
	for name, res := range map[string]Result{
		"tab handle gone": {ExitCode: 1, Stderr: herdrErr("tab_not_found")},
		"pane gone":       {ExitCode: 1, Stderr: herdrErr("pane_not_found")},
		"agent pane gone": {ExitCode: 1, Stderr: herdrErr("agent_pane_not_found")},
		"no server":       {ExitCode: 1, Stderr: "could not connect to herdr socket"},
		"not runnable":    {ExitCode: 127, Stderr: "exec: herdr: not found"},
	} {
		reason := ReasonStaleHandle
		if name == "no server" || name == "not runnable" {
			reason = ReasonHostUnreachable
		}
		cases["herdr/"+name] = statusCase{func(t *testing.T) (Host, string) {
			f := &fake{handler: func(string, []string) Result { return res }}
			return New(Herdr, deps(f, herdrEnv, &clock{})), "w9:t7"
		}, AgentStatus{State: StatusUnverifiable, Reason: reason}}
	}
	cases["herdr-core/program replaced"] = statusCase{func(t *testing.T) (Host, string) {
		h, f := coreFixture(t)
		s := coreSpawn(t, h)
		f.pid = 99
		return h, s
	}, AgentStatus{State: StatusUnverifiable, Reason: "worker program was lost or replaced by a shell; recover/reassign"}}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h, handle := tc.makeHost(t)
			if got := h.Status(bg, "ben", handle); got != tc.want {
				t.Errorf("Status = %+v; want %+v", got, tc.want)
			}
		})
	}
}

func TestStatusEmptyHandleOnEveryPaneKind(t *testing.T) {
	for _, kind := range PaneKinds {
		t.Run(string(kind), func(t *testing.T) {
			f := &fake{handler: func(string, []string) Result { t.Fatal("empty handle queried the host"); return Result{} }}
			h := New(kind, deps(f, nil, &clock{}))
			if got := h.Status(bg, "ben", ""); got != (AgentStatus{}) {
				t.Errorf("Status = %+v; want zero status", got)
			}
		})
	}
}
