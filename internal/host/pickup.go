package host

import "time"

// Pickup policies preserve each transport's confirmation protocol. In
// particular, Orca only polls; herdr must check for a dialog before Enter and
// uses an RPC wait rather than a local settle.
type pickupPolicy struct {
	attempts                 int
	probeFirst               bool
	beforeNudge, beforeProbe time.Duration
	probeTimeout             time.Duration
}

// confirmPickup retries the backend's probe and optional nudge. A probe
// reports pickup, a terminal error, or neither (retry). Its timeout is zero
// for an immediate observation, positive for a transport-level wait. The
// kind's policy is its descriptor's pickup field (policy.go); a kind without
// one gets the zero policy, which makes no attempt.
func confirmPickup(d *Deps, kind Kind, probe func(time.Duration) (bool, error), nudge func()) error {
	spec, _ := specOf(kind)
	p := spec.pickup
	for i := 0; i < p.attempts; i++ {
		if p.probeFirst {
			if pickedUp, err := probe(0); pickedUp || err != nil {
				return err
			}
		}
		if p.beforeNudge > 0 {
			d.settle(p.beforeNudge)
		}
		if nudge != nil {
			nudge()
		}
		if p.beforeProbe > 0 {
			d.settle(p.beforeProbe)
		}
		if pickedUp, err := probe(p.probeTimeout); pickedUp || err != nil {
			return err
		}
	}
	return ErrNotSubmitted
}
