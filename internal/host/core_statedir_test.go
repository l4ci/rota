package host

import (
	"path/filepath"
	"testing"
)

func TestCoreStateDir(t *testing.T) {
	t.Parallel()
	const home = "/fixture/home"
	fallback := filepath.Join(home, ".local", "state", "rota", "herdr-core")
	for _, tc := range []struct {
		name string
		env  map[string]string
		want string // "" wants an error
	}{
		{"override", map[string]string{"ROTA_CORE_STATE_DIR": "/fixture/core", "XDG_STATE_HOME": "/fixture/xdg", "HOME": home}, "/fixture/core"},
		{"absolute XDG_STATE_HOME", map[string]string{"XDG_STATE_HOME": "/fixture/xdg", "HOME": home}, "/fixture/xdg/rota/herdr-core"},
		{"relative XDG_STATE_HOME", map[string]string{"XDG_STATE_HOME": "state", "HOME": home}, fallback},
		{"unset XDG_STATE_HOME", map[string]string{"HOME": home}, fallback},
		{"relative XDG_STATE_HOME without HOME", map[string]string{"XDG_STATE_HOME": "state"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &coreHost{d: Deps{Getenv: func(k string) string { return tc.env[k] }}}
			got, err := h.stateDir()
			if got != tc.want || (err != nil) != (tc.want == "") {
				t.Errorf("stateDir() = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}
