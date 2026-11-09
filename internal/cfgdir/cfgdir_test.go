package cfgdir

import (
	"os"
	"testing"
)

func fakeEnv(vars map[string]string, home string) Env {
	return Env{
		Getenv: func(k string) string { return vars[k] },
		Home: func() (string, error) {
			if home == "" {
				return "", os.ErrNotExist
			}
			return home, nil
		},
	}
}

func TestResolveReadsOnlyTheInjectedEnv(t *testing.T) {
	t.Setenv("ROTA_CONFIG_DIR", "/process/override")
	t.Setenv("XDG_CONFIG_HOME", "/process/xdg")
	t.Setenv("HOME", "/process/home")
	cases := []struct {
		name         string
		vars         map[string]string
		home, dir    string
		source, warn string
	}{
		{"override", map[string]string{"ROTA_CONFIG_DIR": "/o/dir", "XDG_CONFIG_HOME": "/x"}, "/h", "/o/dir", SourceOverride, ""},
		{"xdg", map[string]string{"XDG_CONFIG_HOME": "/x"}, "/h", "/x/rota", SourceXDG, ""},
		{"home", nil, "/h", "/h/.config/rota", SourceDefault, ""},
		{"relative override", map[string]string{"ROTA_CONFIG_DIR": "rel"}, "/h", "/h/.config/rota", SourceDefault, "ROTA_CONFIG_DIR=rel is not an absolute path, ignored"},
	}
	for _, c := range cases {
		dir, src, warn, err := Resolve(fakeEnv(c.vars, c.home))
		if err != nil || dir != c.dir || src != c.source || warn != c.warn {
			t.Errorf("%s: got %q %q %q %v, want %q %q %q", c.name, dir, src, warn, err, c.dir, c.source, c.warn)
		}
	}
	if _, _, _, err := Resolve(fakeEnv(nil, "")); err == nil {
		t.Error("no env and no home: want an error")
	}
}

func TestDirLookupsReadOnlyTheInjectedEnv(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/process/xdg")
	t.Setenv("XDG_DATA_HOME", "/process/xdg")
	t.Setenv("XDG_STATE_HOME", "/process/xdg")
	t.Setenv("HOME", "/process/home")
	lookups := []struct {
		name, xdg, rel string
		fn             func(Env) (string, error)
	}{
		{"Dir", "XDG_CONFIG_HOME", ".config", Dir},
		{"DataDir", "XDG_DATA_HOME", ".local/share", DataDir},
		{"StateDir", "XDG_STATE_HOME", ".local/state", StateDir},
	}
	for _, l := range lookups {
		if d, err := l.fn(fakeEnv(map[string]string{l.xdg: "/x"}, "/h")); err != nil || d != "/x/rota" {
			t.Errorf("%s xdg: %q %v", l.name, d, err)
		}
		if d, err := l.fn(fakeEnv(map[string]string{l.xdg: "relative/ignored"}, "/h")); err != nil || d != "/h/"+l.rel+"/rota" {
			t.Errorf("%s home: %q %v", l.name, d, err)
		}
		if _, err := l.fn(fakeEnv(nil, "")); err == nil {
			t.Errorf("%s: no env and no home must error", l.name)
		}
	}
}
