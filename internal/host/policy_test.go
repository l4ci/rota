package host

import (
	"reflect"
	"testing"
)

// CapabilitiesForKind is the kind-path twin of New(k).Capabilities(): every
// field, optional side and policy value alike, must agree for every kind, so
// a new kind or field cannot drift between the two.
func TestCapabilitiesForKindParity(t *testing.T) {
	for _, k := range append(append([]Kind(nil), PaneKinds...), Solo, "") {
		byKind := reflect.ValueOf(CapabilitiesForKind(k))
		var built Capabilities
		if k != Solo && k != "" {
			built = New(k, Deps{}).Capabilities()
		}
		got := reflect.ValueOf(built)
		for i := 0; i < got.NumField(); i++ {
			name := got.Type().Field(i).Name
			a, b := byKind.Field(i), got.Field(i)
			switch a.Kind() {
			case reflect.Interface, reflect.Func:
				if a.IsNil() != b.IsNil() {
					t.Errorf("%q: %s nil-ness differs: kind path %v, built host %v", k, name, a.IsNil(), b.IsNil())
				}
			default:
				if !reflect.DeepEqual(a.Interface(), b.Interface()) {
					t.Errorf("%q: %s = %v, built host %v", k, name, a.Interface(), b.Interface())
				}
			}
		}
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
