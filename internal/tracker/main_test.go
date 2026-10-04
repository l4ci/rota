package tracker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// tripwireHit is the file the tripwire CLIs append to.
var tripwireHit string

// TestMain puts tripwire gh and glab first on PATH, so a test that forgets to
// inject its executor fails instead of reaching a real forge.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "tracker-tripwire")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	hit := filepath.Join(dir, "hit")
	tripwireHit = hit
	for _, cli := range []string{"gh", "glab"} {
		script := fmt.Sprintf("#!/bin/sh\necho \"$0 $*\" >> '%s'\necho 'tripwire: real %s reached from a test' >&2\nexit 99\n", hit, cli)
		if err := os.WriteFile(filepath.Join(dir, cli), []byte(script), 0o755); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	// Root the temp dirs of child processes under the tripwire dir, which is
	// removed below (#110).
	if tmp := filepath.Join(dir, "tmp"); os.Mkdir(tmp, 0o755) == nil {
		os.Setenv("TMPDIR", tmp)
	}
	code := m.Run()
	if b, err := os.ReadFile(hit); err == nil {
		fmt.Fprintf(os.Stderr, "FAIL: tests exec'd the forge CLI from PATH:\n%s", b)
		code = 1
	}
	os.RemoveAll(dir)
	os.Exit(code)
}

func TestTripwireCatchesRealCLI(t *testing.T) {
	r, err := (&CLI{Provider: "github"}).Run(context.Background(), []string{"issue", "list"}, nil)
	if err != nil || r.ExitCode != 99 {
		t.Fatalf("default executor did not hit the tripwire: %+v, %v", r, err)
	}
	// This test hits the tripwire on purpose; clear the record of it.
	os.Remove(tripwireHit)
}
