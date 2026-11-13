package reap

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// ProcUse is what one live process holds by path: its working directory, its
// executable, every open file, and the HOME and TMPDIR it started with.
type ProcUse struct {
	PID   int
	Paths []string
}

// procEnvKeys are the environment variables that name a directory a process
// keeps working in even when its cwd is elsewhere (a scratch HOME, a TMPDIR).
var procEnvKeys = []string{"HOME=", "TMPDIR="}

// ReadProcUses reads the process table under procRoot (/proc) for the processes
// of uid. Others are skipped: the project temp root is 0700 and the user's
// own, so no other user's process can work in it (and /proc shows a
// non-dumpable process, ssh-agent say, as root's). A process that exited
// during the scan is skipped. Anything else it cannot read (no table, or one
// of uid's processes it may not read) is an error, since then no process can
// be ruled out.
func ReadProcUses(procRoot string, uid int) ([]ProcUse, error) {
	ents, err := os.ReadDir(procRoot)
	if err != nil {
		return nil, err
	}
	var out []ProcUse
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		u, ok, err := readProcUse(filepath.Join(procRoot, e.Name()), pid, uid)
		if err != nil {
			return nil, fmt.Errorf("process %d: %w", pid, err)
		}
		if ok {
			out = append(out, u)
		}
	}
	return out, nil
}

// readProcUse reads one process; ok is false for another user's process or
// one that exited.
func readProcUse(dir string, pid, uid int) (u ProcUse, ok bool, err error) {
	fi, err := os.Stat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return u, false, nil
	}
	if err != nil {
		return u, false, err
	}
	st, isStat := fi.Sys().(*syscall.Stat_t)
	if !isStat {
		return u, false, errors.New("no owner in the process table")
	}
	if int(st.Uid) != uid {
		return u, false, nil
	}
	u.PID = pid
	// gone reports whether a read failed because the process exited (or holds
	// no such link, as a zombie), as opposed to one we may not read.
	gone := func(err error) bool { return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ESRCH) }
	add := func(p string) {
		if strings.HasPrefix(p, "/") {
			u.Paths = append(u.Paths, p)
		}
	}
	for _, link := range []string{"cwd", "exe"} {
		p, err := os.Readlink(filepath.Join(dir, link))
		if err != nil && !gone(err) {
			return u, false, err
		}
		add(p)
	}
	fds, err := os.ReadDir(filepath.Join(dir, "fd"))
	if err != nil && !gone(err) {
		return u, false, err
	}
	for _, fd := range fds {
		p, err := os.Readlink(filepath.Join(dir, "fd", fd.Name()))
		if err != nil && !gone(err) {
			return u, false, err
		}
		add(p)
	}
	env, err := os.ReadFile(filepath.Join(dir, "environ"))
	if err != nil && !gone(err) {
		return u, false, err
	}
	for _, kv := range bytes.Split(env, []byte{0}) {
		for _, key := range procEnvKeys {
			if v, found := strings.CutPrefix(string(kv), key); found {
				add(v)
			}
		}
	}
	return u, true, nil
}
