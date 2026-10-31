package worker

import (
	"strings"

	"github.com/l4ci/rota/internal/host"
)

// The liveness verdicts, which are also the registry and status-row names for
// the same states. Unverifiable is the host unable to say: not proof of death.
const (
	LivenessAlive        = "alive"
	LivenessDead         = "dead"
	LivenessUnverifiable = "unverifiable"
)

// LivenessEvidence is what the callers know about one slot's agent.
type LivenessEvidence struct {
	Caps host.Capabilities
	// State is the slot's recorded state, Reason the reason its last poll
	// recorded for an unverifiable one.
	State, Reason string
	Handle        string
	// Native is the host's own status for the slot's pane, when it gave one:
	// its `gone` is proof of exit, an unverifiable one carries its reason.
	Native string
	// Answered: the host listed its live agents. Listed: one is the slot's.
	Answered, Listed bool
}

// Liveness is the verdict: State is a Liveness* value, Reason says why an
// unverifiable slot is.
type Liveness struct{ State, Reason string }

// JudgeLiveness is the one rule for whether a slot's agent lives. Round status,
// reclaim (Health), poll's pane classifier and the watch digest all ask it, so
// none can disagree: status and reclaim pass the host snapshot, poll the host's
// native pane status, the digest the recorded state.
func JudgeLiveness(in LivenessEvidence) Liveness {
	if v, ok := nativeLiveness(in.Native); ok {
		return v
	}
	dead := IsDead(in.State)
	switch {
	case in.Answered && in.Listed:
		return Liveness{State: LivenessAlive}
	case in.Caps.RuntimeScopedHandles && in.Handle != "" && !dead:
		return Liveness{State: LivenessUnverifiable, Reason: host.ReasonStaleHandle}
	case dead, in.Answered && in.Handle != "":
		return Liveness{State: LivenessDead}
	case !in.Answered && IsUnverifiable(in.State):
		r := in.Reason
		if r == "" {
			r = "unrecorded"
		}
		return Liveness{State: LivenessUnverifiable, Reason: r}
	}
	return Liveness{State: LivenessAlive} // the host cannot be asked: read as alive
}

// nativeLiveness reads a host's native agent status as liveness evidence: the
// host's own `gone` is proof of exit, an unverifiable status carries its
// reason, and every other status says nothing (ok false).
func nativeLiveness(native string) (v Liveness, ok bool) {
	if native == "gone" {
		return Liveness{State: LivenessDead}, true
	}
	if reason, cut := strings.CutPrefix(native, host.UnverifiablePrefix); cut {
		return Liveness{State: LivenessUnverifiable, Reason: reason}, true
	}
	return Liveness{}, false
}
