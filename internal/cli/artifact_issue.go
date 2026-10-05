package cli

import (
	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/jsonx"
)

// Issue-backend helpers the plan store and the milestone verbs share. Items
// resolve through the item workflow (so "F7", "#7" and "7" all answer "7" and
// an unknown item is exit 3).

// failAny maps a domain error (artifact or backlog) onto the exit table,
// keeping the exit-4 refusal data a duplicate create carries.
func failAny(err error) (Result, error) {
	if ae := asArtifact(err); ae != nil {
		return Result{Data: refusal(err)}, err
	}
	return backlogFail(err)
}

func typedData(id, typ string, changed any) *jsonx.Object {
	d := jsonx.NewObject()
	d.Set("id", id)
	d.Set("type", typ)
	if changed != nil {
		d.Set("changed", changed)
	}
	return d
}

// issuesBackend opens the issue backend for verbs that address a milestone
// rather than an item, narrowed to the home sub-repo where slice plans, and
// the plan of a milestone, live.
func issuesBackend(c *Ctx) (*backlog.Issues, error) {
	be, err := openIssueBackend(c, "", false)
	if err != nil {
		return nil, err
	}
	home, err := be.HomeIssues()
	if err != nil {
		return nil, err
	}
	return home, nil
}
