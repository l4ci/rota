package reap

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/proc"
)

// lsofTimeout bounds the one lsof call: it walks every open file of every
// process of the user, which takes a while on a busy macOS machine.
const lsofTimeout = 2 * time.Minute

// ReadLsofUses is ReadProcUses for a host with no /proc (macOS): it asks
// `lsof -w -u <uid> -Fpn` for the open files, working directories, executables
// and mapped files of uid's processes. Fail closed: a missing lsof, a failed
// run or an empty listing is an error, since then no process can be ruled out.
// lsof cannot see a process's HOME and TMPDIR environ, so a process that
// merely started with a scratch HOME holds nothing here.
func ReadLsofUses(ctx context.Context, run proc.Runner, uid int) ([]ProcUse, error) {
	res, err := run(ctx, proc.Cmd{Name: "lsof", Args: []string{"-w", "-u", strconv.Itoa(uid), "-Fpn"}, Env: []string{"LC_ALL=C"}, Timeout: lsofTimeout})
	if err != nil {
		return nil, fmt.Errorf("lsof: %w", err)
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("lsof exited %d", res.ExitCode)
	}
	uses := parseLsofUses(res.Stdout)
	if len(uses) == 0 {
		return nil, errors.New("lsof listed no process")
	}
	return uses, nil
}

// parseLsofUses reads `lsof -Fpn` output: a `p<pid>` line opens a process and
// each `n<name>` line names one of its files. Other field lines are ignored,
// and so is a name that is not an absolute path (a socket, a pipe). A process
// with no path is still listed.
func parseLsofUses(out string) []ProcUse {
	var uses []ProcUse
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "p"):
			if pid, err := strconv.Atoi(line[1:]); err == nil {
				uses = append(uses, ProcUse{PID: pid})
			}
		case strings.HasPrefix(line, "n/") && len(uses) > 0:
			u := &uses[len(uses)-1]
			u.Paths = append(u.Paths, line[1:])
		}
	}
	return uses
}
