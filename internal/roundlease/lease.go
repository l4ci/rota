// Package roundlease is the per-repo orchestrator lease of `rota round start`
// (C3, #59). One orchestrator per repo: the lease is keyed on the git common
// dir, so every worktree of the repo shares it. It cannot live in
// .rota/workers.json, which is per worktree. internal/round re-exports Read and
// ClearStale for reconcile and reap once it exists on main.
package roundlease

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/pidlive"
	"github.com/l4ci/rota/internal/rotastate"
)

// FileName is the lease under <git-common-dir>/rota/.
const FileName = "round-lease.json"

// Lease is the lease document.
type Lease struct {
	PID       int    `json:"pid"`
	Start     uint64 `json:"start,omitempty"`
	Host      string `json:"host"`
	Pane      string `json:"pane,omitempty"`
	PaneHost  string `json:"paneHost,omitempty"`
	Root      string `json:"root"`
	Round     int    `json:"round"`
	StartedAt string `json:"startedAt"`
}

// State is how a lease reads right now.
type State string

const (
	None    State = "none"
	Live    State = "live"
	Stale   State = "stale"   // this host, holder gone
	Foreign State = "foreign" // another host: cannot be checked, treated as live
)

// Outcome is what Acquire did.
type Outcome string

const (
	Taken     Outcome = "taken"
	Renewed   Outcome = "renewed"   // same holder, round number kept
	Numbered  Outcome = "numbered"  // same holder, a lease taken unnumbered (round 0) got its number
	Reclaimed Outcome = "reclaimed" // a stale lease was replaced
)

// Holder identifies an orchestrator process.
type Holder struct {
	PID      int
	Start    uint64
	Pane     string
	PaneHost string
}

// HeldError is returned by Acquire when a live or foreign lease belongs to
// someone else.
type HeldError struct {
	Lease Lease
	State State
}

func (e *HeldError) Error() string {
	where := fmt.Sprintf("pid %d on %s", e.Lease.PID, e.Lease.Host)
	if e.Lease.Pane != "" {
		where += " (pane " + e.Lease.Pane + ")"
	}
	return fmt.Sprintf("a round already holds the lease for this repo: %s, since %s, round %d, %s", where, e.Lease.StartedAt, e.Lease.Round, e.State)
}

// Env is the process and clock access; tests replace it.
type Env struct {
	Host      string
	Alive     func(pid int) bool
	StartTime func(pid int) (uint64, bool)
	Now       func() time.Time
	// Proc reads a process's parent and command name; Parent is this
	// process's parent. Nil means the real /proc and os.Getppid.
	Proc   func(pid int) (ppid int, comm string, ok bool)
	Parent func() int
}

// DefaultEnv reads the real host, process table and clock.
func DefaultEnv() Env {
	name, _ := os.Hostname()
	return Env{Host: name, Alive: pidlive.Alive, StartTime: procStart, Now: time.Now}
}

// Path is the lease file under a common dir.
func Path(commonDir string) string { return rotastate.File(commonDir, FileName) }

// ProcessLive reports whether pid is running and is the process that was
// recorded: a recorded start time (non-zero) that no longer matches means the
// pid was reused.
func (e Env) ProcessLive(pid int, start uint64) bool {
	if !e.Alive(pid) {
		return false
	}
	if start != 0 {
		if s, ok := e.StartTime(pid); ok && s != start {
			return false
		}
	}
	return true
}

// Running is the one liveness rule of the singleton-watcher markers (round
// watch, limit watch, keepalive): the owner is on this host, its pid is alive
// and still the recorded process. A marker from before hosts were recorded
// (empty host) is taken as local. Nothing can be checked about another host's.
func (e Env) Running(hostName string, pid int, start uint64) bool {
	if pid <= 0 || (hostName != "" && hostName != e.Host) {
		return false
	}
	return e.ProcessLive(pid, start)
}

// Identity is what a marker records so Running can tell pid from impostor.
func (e Env) Identity(pid int) (host string, start uint64) {
	start, _ = e.StartTime(pid)
	return e.Host, start
}

// Classify says what a lease is, for this Env's host.
func (e Env) Classify(l Lease) State {
	if l.Host != e.Host {
		return Foreign
	}
	if !e.ProcessLive(l.PID, l.Start) {
		return Stale
	}
	return Live
}

// Read loads the lease. A missing file is None. A file that does not parse is
// a Stale lease with no holder: nothing can own it, so it is reclaimable.
func (e Env) Read(commonDir string) (Lease, State, error) {
	b, err := os.ReadFile(Path(commonDir))
	if errors.Is(err, os.ErrNotExist) {
		return Lease{}, None, nil
	}
	if err != nil {
		return Lease{}, None, err
	}
	var l Lease
	if json.Unmarshal(b, &l) != nil || l.PID <= 0 {
		return Lease{}, Stale, nil
	}
	return l, e.Classify(l), nil
}

// Holds reads the lease and reports whether this process is the orchestrator
// it names: a Live lease whose recorded holder matches the one Discover finds
// for holderPID and getenv. A lease on another host (Foreign) is never held
// here, since SameAs needs the lease's host to be this one. The lease and its
// state come back too, for callers that report on them.
func (e Env) Holds(commonDir string, holderPID int, getenv func(string) string) (l Lease, st State, held bool, err error) {
	l, st, err = e.Read(commonDir)
	if err != nil {
		return l, st, false, err
	}
	return l, st, st == Live && e.Discover(holderPID, getenv).SameAs(l, e.Host), nil
}

// SameAs reports whether h is the process or pane that wrote l.
func (h Holder) SameAs(l Lease, host string) bool {
	if l.Host != host {
		return false
	}
	if h.PID == l.PID && h.Start == l.Start {
		return true
	}
	return h.Pane != "" && h.Pane == l.Pane && h.PaneHost == l.PaneHost
}

// Acquire takes the lease for h, or renews it when h already holds it. A
// stale lease is replaced and returned in prev. Another live or foreign
// holder is a *HeldError and nothing is written. round is stored on a new
// lease; a renewed lease keeps its own round, which the returned Lease shows.
func (e Env) Acquire(commonDir, root string, h Holder, round int) (l Lease, out Outcome, prev Lease, err error) {
	path := Path(commonDir)
	err = fsio.Locked(path, fsio.LockTimeout, func() error {
		cur, st, rerr := e.Read(commonDir)
		if rerr != nil {
			return rerr
		}
		out = Taken
		switch st {
		case Live, Foreign:
			if !h.SameAs(cur, e.Host) {
				return &HeldError{Lease: cur, State: st}
			}
			l, out = cur, Renewed
			if l.Round == 0 && round > 0 {
				l.Round, out = round, Numbered
			}
			l.Pane, l.PaneHost = h.Pane, h.PaneHost
			return write(path, l)
		case Stale:
			prev, out = cur, Reclaimed
		}
		l = Lease{PID: h.PID, Start: h.Start, Host: e.Host, Pane: h.Pane, PaneHost: h.PaneHost,
			Root: root, Round: round, StartedAt: e.Now().UTC().Format(time.RFC3339)}
		return write(path, l)
	})
	return l, out, prev, err
}

func write(path string, l Lease) error {
	b, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	return fsio.WriteFileAtomic(path, append(b, '\n'))
}

// Release removes the lease when h holds it. It reports whether it removed
// one; a lease held by someone else is left alone, with no error.
func (e Env) Release(commonDir string, h Holder) (bool, error) {
	path := Path(commonDir)
	removed := false
	err := fsio.Locked(path, fsio.LockTimeout, func() error {
		cur, st, err := e.Read(commonDir)
		if err != nil || st == None || !h.SameAs(cur, e.Host) {
			return err
		}
		removed = true
		return os.Remove(path)
	})
	return removed, err
}

// ClearStale removes the lease only when it is Stale and returns what it
// removed. It is the seam `rota reap` calls.
func (e Env) ClearStale(commonDir string) (Lease, bool, error) {
	path := Path(commonDir)
	var gone Lease
	cleared := false
	err := fsio.Locked(path, fsio.LockTimeout, func() error {
		cur, st, err := e.Read(commonDir)
		if err != nil || st != Stale {
			return err
		}
		gone, cleared = cur, true
		return os.Remove(path)
	})
	return gone, cleared, err
}

// procStat reads ppid and start ticks of pid from /proc/<pid>/stat.
func procStat(pid int) (ppid int, start uint64, ok bool) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, 0, false
	}
	s := string(b)
	i := strings.LastIndex(s, ")")
	if i < 0 {
		return 0, 0, false
	}
	f := strings.Fields(s[i+1:])
	if len(f) < 20 {
		return 0, 0, false
	}
	pp, err1 := strconv.Atoi(f[1])
	st, err2 := strconv.ParseUint(f[19], 10, 64)
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return pp, st, true
}

func procStart(pid int) (uint64, bool) {
	_, st, ok := procStat(pid)
	return st, ok
}

func procComm(pid int) string {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/comm")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// realProc reads ppid and comm from /proc.
func realProc(pid int) (int, string, bool) {
	pp, _, ok := procStat(pid)
	comm := procComm(pid)
	return pp, comm, ok && comm != ""
}

// hostServer reports whether comm is a terminal host's server: herdr, or tmux
// (whose server and client rename themselves "tmux: server" and "tmux: client").
// Every pane on it shares that ancestor, so it can never be the holder (#205).
func hostServer(comm string) bool {
	return comm == "herdr" || comm == "tmux" || strings.HasPrefix(comm, "tmux:")
}

// transient are the processes between rota and the orchestrator that exit with
// the command: shells and wrappers.
var transient = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "fish": true, "dash": true, "ash": true,
	"env": true, "timeout": true, "rota": true, "sudo": true, "nohup": true, "script": true,
}

// HolderPIDEnv names the environment variable the keepalive supervisor sets in
// its child: the supervisor holds the lease, so its pid is the holder of
// every `rota` the orchestrator runs.
const HolderPIDEnv = "ROTA_ROUND_HOLDER_PID"

// Discover is the orchestrator holder: pid, when non-zero, is --holder-pid
// and wins; otherwise ROTA_ROUND_HOLDER_PID when it holds a pid; otherwise the
// nearest ancestor of this process that is not a shell, env, timeout or rota,
// else the parent. A terminal host's server is a boundary: reaching it means
// rota ran from a plain shell in a pane, and the holder is that pane's shell,
// the ancestor just below the server, so each pane holds apart. The pane comes
// from the environment.
func (e Env) Discover(pid int, getenv func(string) string) Holder {
	h := Holder{}
	h.Pane, h.PaneHost = host.CurrentPaneAny(getenv)
	if pid <= 0 {
		if n, err := strconv.Atoi(strings.TrimSpace(getenv(HolderPIDEnv))); err == nil && n > 0 {
			pid = n
		}
	}
	if pid <= 0 {
		proc, parent := e.Proc, e.Parent
		if proc == nil {
			proc = realProc
		}
		if parent == nil {
			parent = os.Getppid
		}
		pid = parent()
		below := 0
		for cur, n := pid, 0; n < 16 && cur > 1; n++ {
			pp, comm, ok := proc(cur)
			if !ok {
				break
			}
			if hostServer(comm) {
				if below != 0 {
					pid = below
				}
				break
			}
			if !transient[comm] {
				pid = cur
				break
			}
			below, cur = cur, pp
		}
	}
	h.PID = pid
	if s, ok := e.StartTime(pid); ok {
		h.Start = s
	}
	return h
}
