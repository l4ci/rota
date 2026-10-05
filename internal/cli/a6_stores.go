package cli

import (
	"github.com/l4ci/rota/internal/artifact"
	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/design"
	"github.com/l4ci/rota/internal/plan"
	"github.com/l4ci/rota/internal/proof"
)

// The one place the design, plan and proof verbs meet backlog.backend: each
// opens the store of its kind (files, or notes on the issue backend) and the
// verb then calls the kind's module without asking which backend it is.
// Stores that need the tracker connect on first use, so a verb that fails
// validation never reaches it.

// backlogMode is the project root and whether backlog.backend is "issues".
// An invalid backlog.backend is an internal error (exit 70, as the old
// helpers exited 1 on it).
func backlogMode(c *Ctx) (root string, issue bool, err error) {
	root, err = c.Root()
	if err != nil {
		return "", false, err
	}
	name, err := config.Backend(config.Load(root + "/.rota/config.json"))
	if err != nil {
		return "", false, &Error{Exit: ExitInternal, Message: err.Error()}
	}
	return root, name == "issues", nil
}

// who is the item a verb answers for: the ID the backend resolves the typed
// one to ("F7" is "7" on the issue backend) and its type letter. A notes
// store fills it in when it resolves the item.
type who struct{ ID, Type string }

// itemNotes resolves id to its issue's notes. An unknown item is exit 3.
func itemNotes(c *Ctx, id string, w *who) func() (artifact.Notes, error) {
	return func() (artifact.Notes, error) {
		_, wf, cid, typ, err := a4Flow(c, id)
		if err != nil {
			return nil, err
		}
		*w = who{cid, typ}
		return wf, nil
	}
}

// openDesign is the design store for item id.
func openDesign(c *Ctx, id string) (design.Store, *who, error) {
	root, issue, err := backlogMode(c)
	if err != nil {
		return nil, nil, err
	}
	w := &who{ID: id, Type: a4Type(id)}
	if issue {
		return design.NewNotes(itemNotes(c, id, w)), w, nil
	}
	return design.Files(root), w, nil
}

// openProof is the proof store for item id.
func openProof(c *Ctx, id string) (root string, st proof.Store, w *who, err error) {
	root, issue, err := backlogMode(c)
	if err != nil {
		return "", nil, nil, err
	}
	w = &who{ID: id, Type: a4Type(id)}
	if issue {
		return root, proof.NewNotes(itemNotes(c, id, w)), w, nil
	}
	return root, proof.Files(root), w, nil
}

// openPlans is the plan store: item plans resolve their item, slice plans
// open the milestone's tracking issue.
func openPlans(c *Ctx) (root string, st plan.Store, err error) {
	root, issue, err := backlogMode(c)
	if err != nil {
		return "", nil, err
	}
	if !issue {
		return root, plan.Files(root), nil
	}
	item := func(id string) (artifact.Notes, error) {
		return itemNotes(c, id, &who{})()
	}
	slices := func() (plan.SliceStore, error) {
		be, err := issuesBackend(c)
		if err != nil {
			return nil, err
		}
		return be, nil
	}
	return root, plan.NewNotes(item, slices, func(msg string) { c.Warn("%s", msg) }), nil
}

// openItems is where plan uncertain looks for open item id.
func openItems(c *Ctx, id string) (plan.Items, error) {
	root, issue, err := backlogMode(c)
	if err != nil {
		return nil, err
	}
	if !issue {
		return plan.FileItems(root), nil
	}
	return plan.NewIssueItems(func() (backlog.Backend, error) {
		be, _, _, _, err := a4Flow(c, id)
		return be, err
	}), nil
}
