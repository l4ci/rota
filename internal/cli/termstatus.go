package cli

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"sync"
)

// OSC 7501 is the Program Status Protocol: a program tells its terminal
// whether it is working, done or failed, and herdr, Ghostty and the Core app
// show that in the tab. Terminals that do not know the sequence ignore it, so
// there is no capability probe, only the terminal check below.
// https://www.superlogical.com/rex/docs/build/program-status
const termStatusApp = "rota"

// osc7501 is one report. progress is 0-100, or negative for none (an
// indeterminate working record). msg is base64 text, so it must stay one line.
func osc7501(state, msg string, progress int) string {
	s := "\x1b]7501;state=" + state + ":app=" + termStatusApp
	if progress >= 0 && (state == "working" || state == "blocked") {
		s += ":progress=" + strconv.Itoa(min(progress, 100))
	}
	if msg != "" {
		s += ":msg=" + base64.StdEncoding.EncodeToString([]byte(msg))
	}
	return s + "\x1b\\"
}

// termStatus writes working/done/error records for one long-running verb.
// One made on a pipe writes nothing.
type termStatus struct {
	c    *Ctx
	on   bool
	msg  string
	once sync.Once
}

// trackStatus starts a working record for msg and returns the tracker. It is
// silent unless stdout and stderr are both terminals and the verb is not
// --json, so piped, --json and test output are untouched. The record goes to
// stderr. Finish it from a defer, so a panic or early return still leaves done
// or error behind.
func (c *Ctx) trackStatus(msg string) *termStatus {
	d := c.deps()
	s := &termStatus{c: c, msg: msg, on: !c.JSON && d.IsTerminal(c.Stdout) && d.IsTerminal(c.Stderr)}
	s.Progress(-1)
	return s
}

// Progress reports working at pct percent (negative: indeterminate).
func (s *termStatus) Progress(pct int) {
	if s.on {
		fmt.Fprint(s.c.Stderr, osc7501("working", s.msg, pct))
	}
}

// Finish ends the record: done when ok, else error. Later calls do nothing.
func (s *termStatus) Finish(ok bool) {
	s.once.Do(func() {
		if !s.on {
			return
		}
		state := "error"
		if ok {
			state = "done"
		}
		fmt.Fprint(s.c.Stderr, osc7501(state, s.msg, -1))
	})
}
