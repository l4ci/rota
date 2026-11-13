package round

import (
	"context"
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/marker"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/worker"
)

// ConfigError is a round.* setting that does not parse; the CLI reports it as usage.
type ConfigError struct{ Err error }

func (e *ConfigError) Error() string { return e.Err.Error() }
func (e *ConfigError) Unwrap() error { return e.Err }

// RecoveryAssigned says whether o is a recovery dispatch of the task the slot
// holds. Standalone and relay dispatches are not, and keep their own brief.
func RecoveryAssigned(root string, o worker.DispatchOpts) (bool, error) {
	if o.Relay || o.Task == "" {
		return false, nil
	}
	reg, err := worker.LoadRegistry(root)
	if err != nil {
		return false, err
	}
	s := reg.Slot(o.Slot)
	return s != nil && s.Task() == worker.NormID(o.Task) && s.ClaimID() != "", nil
}

// RecoveryBrief rebuilds the slot's assignment brief from the backlog and
// appends the operator's notes. A setting that does not parse is a *ConfigError.
func (e Env) RecoveryBrief(ctx context.Context, root string, be Board, cfg any, o worker.DispatchOpts, notes string) (string, worker.DispatchOpts, error) {
	set, err := roundcfg.FromConfig(cfg)
	if err != nil {
		return "", o, &ConfigError{Err: err}
	}
	text, rebuilt, err := e.RebuildTaskBrief(ctx, root, be, o, set)
	if err != nil {
		return "", o, err
	}
	return text + "\nNotes appended for recovery:\n\n" + notes, rebuilt, nil
}

// SupportRelay decides whether a relay is also posted on the slot's issue
// (#1827): the main machine's gate cannot read a support machine's local relay
// log, so the issue thread is where it looks. Only a support machine posts:
// on the explicit queued PR's issue, else the slot's held issue. body is the comment text.
func SupportRelay(root string, cfg any, slot string, round int, text, pr string) (issue, body string, post bool, err error) {
	if roundcfg.SupportName(cfg) == "" {
		return "", "", false, nil
	}
	if role, err := roundcfg.MachineRole(cfg); err != nil || role != roundcfg.RoleSupport {
		return "", "", false, nil
	}
	reg, err := worker.LoadRegistry(root)
	if err != nil {
		return "", "", false, err
	}
	if pr != "" {
		if q := reg.QueuedPR(pr); q != nil {
			return q.Issue, marker.RelayLine(round) + "\n" + strings.TrimSpace(text), true, nil
		}
		return "", "", false, fmt.Errorf("relay PR %s is no longer on the review list", pr)
	}
	s := reg.Slot(slot)
	if s == nil || s.HeldID() == "" {
		return "", "", false, nil
	}
	return s.HeldID(), marker.RelayLine(round) + "\n" + strings.TrimSpace(text), true, nil
}
