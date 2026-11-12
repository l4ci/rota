package round

import (
	"context"
	"errors"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/worker"
)

// integrationBase is the integration branch an interaction item was filed for
// (`worker train --integration`): its slot is cut from that branch and its PR
// targets it, so the fix never lands on a member branch or on the base. ""
// for any other item.
func integrationBase(root string, it *backlog.Item) string {
	reg, err := worker.LoadRegistry(root)
	if err != nil {
		return ""
	}
	if rec, ok := reg.IntegrationForItem(it.ID); ok {
		return rec.Branch
	}
	return ""
}

// ParkIntegrationMembers frees each slot still holding a member PR of a
// landed integration branch, as reconcile --apply parks a slot whose PR
// merged: the landing marks every member merged, so a later reconcile finds
// nothing to repair. A slot that cannot be parked (busy, dirty) is named in
// the error and left as it is.
func (e Env) ParkIntegrationMembers(ctx context.Context, root string, rec worker.Integration) ([]string, error) {
	reg, err := worker.LoadRegistry(root)
	if err != nil {
		return nil, err
	}
	members := map[int]bool{}
	for _, m := range rec.Members {
		if n, ok := worker.PRRefNumber(m.PR); ok {
			members[n] = true
		}
	}
	var parked []string
	var errs []error
	for _, s := range reg.Slots() {
		n, ok := worker.PRRefNumber(s.PR())
		if !ok || !members[n] || s.IsExternal() {
			continue
		}
		if err := e.parkMerged(ctx, root, s.Name()); err != nil {
			errs = append(errs, err)
			continue
		}
		parked = append(parked, s.Name())
	}
	return parked, errors.Join(errs...)
}
