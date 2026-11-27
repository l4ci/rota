package host

// AgentStatus is a native agent state with separate failure evidence. Its
// zero value means the host has no native status or the slot has no handle.
// State otherwise carries the host's native vocabulary (working, idle, ...),
// or one of the failure states below. Reason explains an unverifiable state.
type AgentStatus struct {
	State  string
	Reason string
}

const (
	StatusWorking  = "working"
	StatusStarting = "starting"
	StatusIdle     = "idle"
	StatusBlocked  = "blocked"
	StatusDone     = "done"
	StatusUnknown  = "unknown"

	StatusGone         = "gone"
	StatusUnverifiable = "unverifiable"

	ReasonHostUnreachable = "host-unreachable"
	ReasonStaleHandle     = "stale-handle"
	ReasonPaneUnreadable  = "pane-unreadable"
)
