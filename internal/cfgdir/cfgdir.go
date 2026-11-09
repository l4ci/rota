// Package cfgdir resolves the global rota config, data and state dirs from an
// injected process environment. It is a leaf: the settings package and the
// project registry both read the environment through it, and it imports
// nothing from rota.
package cfgdir

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Where a resolved config dir came from.
const (
	SourceOverride = "override" // ROTA_CONFIG_DIR
	SourceXDG      = "xdg"      // XDG_CONFIG_HOME
	SourceDefault  = "default"  // ~/.config/rota
)

// Env is the process environment the dir lookups read: Getenv the variable
// reader, Home the home-dir reader and TempDirs the OS temp roots. The cli supplies them from Deps; the
// package reads neither the process env nor the home itself.
type Env struct {
	Getenv func(string) string
	Home   func() (string, error)
	// TempDirs supplies the OS temporary directories. Nil uses TMPDIR and
	// the standard Unix/macOS locations; tests can supply their own OS roots.
	TempDirs func() []string
}

// OSEnv is the process environment and home dir: the edge for code that has
// no Deps to take an Env from.
func OSEnv() Env { return Env{Getenv: os.Getenv, Home: os.UserHomeDir} }

// Resolve finds the global rota config dir and says why: $ROTA_CONFIG_DIR when
// that is an absolute path, else $XDG_CONFIG_HOME/rota when that is an
// absolute path (the XDG spec ignores a relative one), else ~/.config/rota.
// warning is non-empty when a relative ROTA_CONFIG_DIR was ignored.
func Resolve(env Env) (dir, source, warning string, err error) {
	if o := env.Getenv("ROTA_CONFIG_DIR"); o != "" {
		if filepath.IsAbs(o) {
			return filepath.Clean(o), SourceOverride, "", nil
		}
		warning = "ROTA_CONFIG_DIR=" + o + " is not an absolute path, ignored"
	}
	if x := env.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(x) {
		return filepath.Join(x, "rota"), SourceXDG, warning, nil
	}
	home, err := env.Home()
	if err != nil {
		return "", "", warning, errors.New("no global config dir: none of ROTA_CONFIG_DIR, XDG_CONFIG_HOME or HOME is set")
	}
	return filepath.Join(home, ".config", "rota"), SourceDefault, warning, nil
}

var warnOnce sync.Once

// Dir is the global rota config dir (see Resolve). A relative ROTA_CONFIG_DIR
// is ignored with one warning on stderr per process.
func Dir(env Env) (string, error) {
	dir, _, warning, err := Resolve(env)
	if warning != "" {
		warnOnce.Do(func() { fmt.Fprintln(os.Stderr, "rota: warning: "+warning) })
	}
	return dir, err
}

// DataDir is the global rota data dir: $XDG_DATA_HOME/rota when that is an
// absolute path, else ~/.local/share/rota. It holds what must not travel with
// a synced config dir, such as the token fallback file.
func DataDir(env Env) (string, error) {
	if x := env.Getenv("XDG_DATA_HOME"); filepath.IsAbs(x) {
		return filepath.Join(x, "rota"), nil
	}
	home, err := env.Home()
	if err != nil {
		return "", errors.New("no global data dir: neither XDG_DATA_HOME nor HOME is set")
	}
	return filepath.Join(home, ".local", "share", "rota"), nil
}

// StateDir is the rota state dir, where machine-rewritten files live (the
// project registry and its lock): $XDG_STATE_HOME/rota when that is an
// absolute path, else ~/.local/state/rota.
func StateDir(env Env) (string, error) {
	if x := env.Getenv("XDG_STATE_HOME"); filepath.IsAbs(x) {
		return filepath.Join(x, "rota"), nil
	}
	home, err := env.Home()
	if err != nil {
		return "", errors.New("no state dir: neither XDG_STATE_HOME nor HOME is set")
	}
	return filepath.Join(home, ".local", "state", "rota"), nil
}
