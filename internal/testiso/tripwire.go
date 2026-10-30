package testiso

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// hostEnvPrefixes are the host-session variables a test must never inherit:
// herdr and tmux pane/socket state. Same rule as isolate_env in
// test/lib/isolate.sh, by prefix so a new HERDR_* variable is covered unseen.
var hostEnvPrefixes = []string{"HERDR_", "TMUX"}

// scrubHostEnv unsets every variable that starts with a host prefix.
func scrubHostEnv() {
	for _, kv := range os.Environ() {
		for _, p := range hostEnvPrefixes {
			if strings.HasPrefix(kv, p) {
				os.Unsetenv(kv[:strings.IndexByte(kv, '=')])
				break
			}
		}
	}
}

// Tripwire is a dir of fake host binaries put first on PATH. Each one logs its
// call to Hit and exits 99, so a test that reaches a real herdr, tmux or forge
// binary fails the run instead of killing live agents.
type Tripwire struct {
	Dir string // holds the fake binaries and a tmp/ dir
	Hit string // file the fakes append their calls to
}

// NewTripwire writes fake bins into a fresh temp dir named after prefix,
// prepends it to PATH and roots TMPDIR under it. Close removes the dir.
func NewTripwire(prefix string, bins ...string) (*Tripwire, error) {
	dir, err := os.MkdirTemp("", prefix)
	if err != nil {
		return nil, err
	}
	t := &Tripwire{Dir: dir, Hit: filepath.Join(dir, "hit")}
	for _, bin := range bins {
		script := fmt.Sprintf("#!/bin/sh\necho \"$0 $*\" >> '%s'\necho 'tripwire: real %s reached from a test' >&2\nexit 99\n", t.Hit, bin)
		if err := os.WriteFile(filepath.Join(dir, bin), []byte(script), 0o755); err != nil {
			os.RemoveAll(dir)
			return nil, err
		}
	}
	os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	// Child processes (fake forge and host scripts mktemp) root their temp
	// dirs here, removed by Close (#110).
	if tmp := filepath.Join(dir, "tmp"); os.Mkdir(tmp, 0o755) == nil {
		os.Setenv("TMPDIR", tmp)
	}
	return t, nil
}

// Check returns the logged calls, empty when no fake was reached.
func (t *Tripwire) Check() string {
	b, err := os.ReadFile(t.Hit)
	if err != nil {
		return ""
	}
	return string(b)
}

// Close removes the tripwire dir.
func (t *Tripwire) Close() { os.RemoveAll(t.Dir) }
