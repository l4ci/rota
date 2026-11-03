package host

import (
	"errors"
	"testing"
)

func TestCoreContextSelection(t *testing.T) {
	env := map[string]string{"HC_TERMINAL_ID": "t1", "HC_TERMINAL_SOCKET": "/app/core.sock", "HERDR_ENV": "1", "HERDR_WORKSPACE_ID": "w1", "TMUX": "stale"}
	get := func(k string) string { return env[k] }
	absent := func(string) (string, error) { return "", errors.New("absent") }
	if got := Resolve("", "", get, absent); got != Kind("herdr-core") {
		t.Errorf("Core project terminal selects %q, want herdr-core without legacy CLI", got)
	}
	if got := Resolve(Herdr, "", get, absent); got != Herdr {
		t.Errorf("active legacy round migrated: %q", got)
	}
	got, err := ResolveLaunch("", get, absent)
	if err != nil || got.Kind != Kind("herdr-core") || got.NewSession {
		t.Errorf("Core launch = %+v, %v; want associated runtime", got, err)
	}
	delete(env, "HC_TERMINAL_SOCKET")
	if _, err := ResolveLaunch("", get, absent); err == nil {
		t.Error("partial Core markers must refuse, never fallback")
	}
}
