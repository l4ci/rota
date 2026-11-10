package host

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os/exec"
	"strings"

	"github.com/l4ci/rota/internal/harness"
)

// This file is where a host kind's policy lives: facts callers used to infer
// by comparing a Kind. A caller asks CapabilitiesForKind(k) (or a host's
// Capabilities()) and reads a field; a new host edits this file and its
// adapter, nothing else.

// PaneKinds are the host kinds that have panes, in resolution order.
var PaneKinds = []Kind{Herdr, HerdrCore, Tmux, Orca}

// InSessionRule is a host that only works from inside one of its own panes.
type InSessionRule struct {
	// Pane names the pane in a refusal ("herdr pane").
	Pane string
	// Env is the marker the host injects into every pane ("HERDR_ENV=1").
	Env string
}

// ServerProc names a host's server process, for the round lease: every pane
// shares that ancestor, so it can never be the holder (#205).
type ServerProc struct {
	// Comm is the process name.
	Comm string
	// Renames: the server and its clients rename themselves "<Comm>: <role>".
	Renames bool
}

// Match reports whether comm is this server process.
func (s ServerProc) Match(comm string) bool {
	return s.Comm != "" && (comm == s.Comm || (s.Renames && strings.HasPrefix(comm, s.Comm+":")))
}

// Probe is what a host's Diagnose may touch; the caller fills every field.
type Probe struct {
	Getenv func(string) string
	Look   func(name string) (string, bool)
	Exec   func(ctx context.Context, bin string, args []string) (Result, error)
	Dial   func(ctx context.Context, path string) (net.Conn, error)
}

// HealthStatus is the outcome of a host's Diagnose.
type HealthStatus int

// The outcomes of a host check.
const (
	HealthOK HealthStatus = iota
	HealthWarn
	HealthFail
)

// Health is what a host's Diagnose found.
type Health struct {
	Status HealthStatus
	Detail string
	Hint   string
}

// SessionAttach builds the command that starts (or attaches to) a named
// session of the host running line in cwd. argv[0] is the binary.
type SessionAttach func(session, cwd, label, line string) []string

// The env var each host injects to name the pane a process runs in. This is
// the one place those names live; adapters and CurrentPane read them here.
const (
	envHerdrPane = "HERDR_PANE_ID"
	envTmuxPane  = "TMUX_PANE"
	envOrcaPane  = "ORCA_TERMINAL_HANDLE"
	envCoreTerm  = "HC_TERMINAL_ID"
)

// paneEnv is the env var naming the current pane under k, "" for Solo and
// unknown kinds (no pane to name).
func paneEnv(k Kind) string {
	switch k {
	case Herdr:
		return envHerdrPane
	case HerdrCore:
		return envCoreTerm
	case Tmux:
		return envTmuxPane
	case Orca:
		return envOrcaPane
	}
	return ""
}

// kindPolicy overlays k's policy on c: the fields no optional interface
// carries. Solo and unknown kinds have none.
func kindPolicy(k Kind, c Capabilities) Capabilities {
	switch k {
	case Herdr:
		c.InSessionRule = &InSessionRule{Pane: "herdr pane", Env: "HERDR_ENV=1"}
		c.LaunchKindBound = true
		c.NoNominalHandle = true
		c.Harness = harness.HostTraits{CodexHost: true, HerdrIntegration: true}
		c.Server = ServerProc{Comm: Herdr.Binary()}
		c.Attach = func(session, _, _, _ string) []string {
			return []string{Herdr.Binary(), "session", "attach", session}
		}
		c.Diagnose = diagnoseHerdr
	case HerdrCore:
		c.NoNominalHandle = true
		c.InPlace = true
		c.Harness = harness.HostTraits{CodexHost: true, CoreIdentity: true}
		c.Diagnose = diagnoseCore
	case Tmux:
		c.Server = ServerProc{Comm: Tmux.Binary(), Renames: true}
		// -A attaches when the session exists; its command is then ignored.
		// The shell after the command keeps a failure message on screen.
		c.Attach = func(session, cwd, label, line string) []string {
			return []string{Tmux.Binary(), "new-session", "-A", "-s", session, "-c", cwd, "-n", label, line + `; exec "${SHELL:-sh}"`}
		}
		c.Diagnose = diagnoseTmux
	case Orca:
		c.NoNominalHandle = true
		c.Harness = harness.HostTraits{CodexHost: true}
		c.Diagnose = diagnoseOrca
	}
	return c
}

func diagnoseCore(p Probe) Health {
	const hint = "open the associated Herdr App project terminal and check HC_TERMINAL_ID and HC_TERMINAL_SOCKET"
	get := p.Getenv
	if get == nil {
		get = func(string) string { return "" }
	}
	if get("HC_TERMINAL_ID") == "" || get("HC_TERMINAL_SOCKET") == "" {
		return Health{HealthFail, "Herdr Core terminal identity/endpoint is missing", hint}
	}
	h := New(HerdrCore, Deps{Getenv: get, Dial: p.Dial})
	if err := h.Require(); err != nil {
		return Health{HealthFail, err.Error(), hint}
	}
	detail := "Herdr Core associated endpoint verified"
	if identity := h.Capabilities().CoreIdentity; identity != nil {
		id, version := identity.CoreIdentity()
		detail += "; id=" + id
		if version != "" {
			detail += "; version=" + version
		}
	}
	return Health{Status: HealthOK, Detail: detail}
}

func diagnoseHerdr(p Probe) Health {
	want := SupportedHerdr + ".x"
	bin, ok := p.Look(Herdr.Binary())
	if !ok {
		return Health{HealthFail, "herdr not found on PATH", "install herdr " + want + " (https://herdr.dev)"}
	}
	r, err := p.Exec(context.Background(), bin, []string{"--version"})
	out := strings.TrimSpace(r.Stdout + " " + r.Stderr)
	v, verr := HerdrVersion(out)
	if err != nil || r.ExitCode != 0 || v == "" {
		return Health{HealthFail, "herdr version unreadable", "reinstall herdr " + want + " (https://herdr.dev)"}
	}
	if verr != nil {
		return Health{HealthFail, fmt.Sprintf("herdr %s, need %s", v, want), "install herdr " + want + " (https://herdr.dev)"}
	}
	return Health{Status: HealthOK, Detail: "herdr " + v}
}

func diagnoseTmux(p Probe) Health {
	if _, ok := p.Look(Tmux.Binary()); !ok {
		return Health{HealthFail, "tmux not found on PATH", "install tmux"}
	}
	return Health{Status: HealthOK, Detail: "tmux on PATH"}
}

func diagnoseOrca(p Probe) Health {
	var statusJSON string
	h := New(Orca, Deps{
		LookPath: func(name string) (string, error) {
			bin, ok := p.Look(name)
			if !ok {
				return "", exec.ErrNotFound
			}
			return bin, nil
		},
		Run: func(ctx context.Context, name string, args []string) (Result, error) {
			bin, ok := p.Look(name)
			if !ok {
				return Result{}, exec.ErrNotFound
			}
			r, err := p.Exec(ctx, bin, args)
			statusJSON = r.Stdout
			return r, err
		},
	})
	if err := h.Require(); err != nil {
		return Health{HealthFail, err.Error(), "put orca on PATH and open the Orca app"}
	}
	var status struct {
		AutoRename bool `json:"autoRenameBranchFromWork"`
		Settings   struct {
			AutoRename bool `json:"autoRenameBranchFromWork"`
		} `json:"settings"`
	}
	// The setting is optional status evidence, not a documented settings API.
	// Dispatch checks the saved branch after pickup even when it is absent.
	if json.Unmarshal([]byte(statusJSON), &status) == nil && (status.AutoRename || status.Settings.AutoRename) {
		return Health{HealthWarn, "orca app running; autoRenameBranchFromWork is enabled", "disable autoRenameBranchFromWork before dispatching rota slots"}
	}
	return Health{Status: HealthOK, Detail: "orca on PATH; app running"}
}
