package round

import (
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
