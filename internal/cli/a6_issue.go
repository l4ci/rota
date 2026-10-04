package cli

import (
	"github.com/l4ci/rota/internal/artifact"
	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/design"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/plan"
	"github.com/l4ci/rota/internal/proof"
)

// The issue-mode halves of the design, plan and proof verbs. They resolve
// the item through the A4 workflow (so "F7", "#7" and "7" all answer "7"
// and an unknown item is exit 3) and keep their text in item notes.

// modeRoot is the project root and whether backlog.backend is "issues".
func modeRoot(c *Ctx) (root string, issue bool, err error) {
	root, err = c.Root()
	if err != nil {
		return "", false, err
	}
	return root, artifact.IssueMode(root), nil
}

// failAny maps a domain error (artifact or backlog) onto the exit table,
// keeping the exit-4 refusal data a duplicate create carries.
func failAny(err error) (Result, error) {
	if ae := asArtifact(err); ae != nil {
		return Result{Data: refusal(err)}, err
	}
	return a4Fail(err)
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

// ---- design

func designAddIssue(c *Ctx, id, title string) (Result, error) {
	if !design.ValidIssueID(id) {
		return failAny(design.AddNote(nil, "", id, title))
	}
	_, wf, cid, typ, err := a4Flow(c, id)
	if err != nil {
		return a4Fail(err)
	}
	if err := design.AddNote(wf, id, id, title); err != nil {
		return failAny(err)
	}
	return Result{Data: typedData(cid, typ, true), Text: cid}, nil
}

func designShowIssue(c *Ctx, id string) (Result, error) {
	if !design.ValidIssueID(id) {
		return failAny(errOf(design.ShowNote(nil, "", id)))
	}
	_, wf, cid, typ, err := a4Flow(c, id)
	if err != nil {
		return a4Fail(err)
	}
	body, err := design.ShowNote(wf, id, id)
	if err != nil {
		return failAny(err)
	}
	d := typedData(cid, typ, nil)
	d.Set("body", body)
	return Result{Data: d, Text: body}, nil
}

func designPutIssue(c *Ctx, id, file string) (Result, error) {
	if !design.ValidIssueID(id) {
		return failAny(errOf(design.PutNote(nil, "", id, "")))
	}
	text, err := readBody(c, file)
	if err != nil {
		return Result{}, err
	}
	_, wf, cid, typ, err := a4Flow(c, id)
	if err != nil {
		return a4Fail(err)
	}
	changed, err := design.PutNote(wf, id, id, text)
	if err != nil {
		return failAny(err)
	}
	return Result{Data: typedData(cid, typ, changed), Text: cid}, nil
}

func designRmIssue(c *Ctx, id string) (Result, error) {
	if !design.ValidIssueID(id) {
		return failAny(design.RmNote(nil, "", id))
	}
	_, wf, cid, typ, err := a4Flow(c, id)
	if err != nil {
		return a4Fail(err)
	}
	if err := design.RmNote(wf, id, id); err != nil {
		return failAny(err)
	}
	return Result{Data: typedData(cid, typ, true), Text: cid}, nil
}

// errOf drops a function's value and keeps its error, for the validation-only calls above.
func errOf[T any](_ T, err error) error { return err }

// ---- plan (item plans; slice plans come with the milestone verbs)

// issuesBackend opens the issue backend for verbs that address a milestone
// rather than an item; duplicate-tracking-issue notices go to stderr and
// into the envelope's warnings.
func issuesBackend(c *Ctx) (*backlog.Issues, error) {
	be, err := openIssues(c)
	if err != nil {
		return nil, err
	}
	switch b := be.(type) {
	case *backlog.Issues:
		b.Warn = func(msg string) { c.Warn("%s", msg) }
		return b, nil
	case *backlog.Umbrella:
		// Slice plans, and the plan of a milestone, live on the home sub-repo.
		home, err := b.HomeSub()
		if err != nil {
			return nil, err
		}
		home.Warn = func(msg string) { c.Warn("%s", msg) }
		return home, nil
	}
	return nil, Refused("%s works on the issue backend only", c.Path)
}

// milestoneBackend is what the milestone verbs need of either issue backend:
// one repo, or an umbrella that mints IDs and aggregates status over every
// sub-repo's native milestones.
type milestoneBackend interface {
	MilestoneAdd(mid, title, summary string, depends []string, today string) (string, error)
	MilestoneList() ([]backlog.MilestoneRow, error)
	MilestoneShow(mid string) (string, error)
	MilestonePut(mid, text string) error
	MilestoneStatus(mid, status string) error
}

func milestonesBackend(c *Ctx) (milestoneBackend, error) {
	be, err := openIssues(c)
	if err != nil {
		return nil, err
	}
	switch b := be.(type) {
	case *backlog.Issues:
		b.Warn = func(msg string) { c.Warn("%s", msg) }
		return b, nil
	case *backlog.Umbrella:
		if home, err := b.HomeSub(); err == nil {
			home.Warn = func(msg string) { c.Warn("%s", msg) }
		}
		return b, nil
	}
	return nil, Refused("%s works on the issue backend only", c.Path)
}

// openIssues opens the backlog backend for a verb that addresses a milestone
// rather than an item.
func openIssues(c *Ctx) (backlog.Backend, error) {
	root, err := a4Scope(c)
	if err != nil {
		return nil, err
	}
	return a4Open(c, root, false, "")
}

func planAddIssue(c *Ctx, root string, o plan.AddOpts) (Result, error) {
	if err := plan.CheckAdd(o, true); err != nil {
		return Result{}, err
	}
	slice := o.Key == ""
	if !slice {
		if _, _, isSlice := plan.SliceOf(o.Key); isSlice {
			slice = true
		}
	}
	if slice {
		be, err := issuesBackend(c)
		if err != nil {
			return a4Fail(err)
		}
		key, err := plan.AddSliceNote(root, be, o)
		if err != nil {
			return failAny(err)
		}
		return Result{Data: addData(key, "slice"), Text: key}, nil
	}
	item, _, err := plan.ItemOf(o.Key)
	if err != nil {
		return Result{}, err
	}
	_, wf, _, _, err := a4Flow(c, item)
	if err != nil {
		return a4Fail(err)
	}
	key, err := plan.AddItemNote(root, wf, o)
	if err != nil {
		return failAny(err)
	}
	return Result{Data: addData(key, "item"), Text: key}, nil
}

func addData(key, kind string) *jsonx.Object {
	d := jsonx.NewObject()
	d.Set("key", key)
	d.Set("unitKind", kind)
	d.Set("changed", true)
	return d
}

func planShowIssue(c *Ctx, key string) (Result, error) {
	if err := validKey(key); err != nil {
		return Result{}, err
	}
	var body string
	if _, _, isSlice := plan.SliceOf(key); isSlice {
		be, err := issuesBackend(c)
		if err != nil {
			return a4Fail(err)
		}
		if body, err = plan.ShowSliceNote(be, key); err != nil {
			return failAny(err)
		}
	} else {
		item, _, _ := plan.ItemOf(key)
		_, wf, _, _, err := a4Flow(c, item)
		if err != nil {
			return a4Fail(err)
		}
		if body, err = plan.ShowItemNote(wf, item); err != nil {
			return failAny(err)
		}
	}
	d := keyData(key, nil)
	d.Set("body", body)
	return Result{Data: d, Text: body}, nil
}

func validKey(key string) error {
	if !plan.ValidKey(key) && !plan.ItemOnlyKey(key) {
		return Usage("key must look like #7, B7, M01-B07 or M01-S02, got %q", key)
	}
	return nil
}

func planPutIssue(c *Ctx, key, file string) (Result, error) {
	if err := validKey(key); err != nil {
		return Result{}, err
	}
	text, err := readBody(c, file)
	if err != nil {
		return Result{}, err
	}
	var changed bool
	if _, _, isSlice := plan.SliceOf(key); isSlice {
		be, err := issuesBackend(c)
		if err != nil {
			return a4Fail(err)
		}
		if changed, err = plan.PutSliceNote(be, key, text); err != nil {
			return failAny(err)
		}
	} else {
		item, _, _ := plan.ItemOf(key)
		_, wf, _, _, err := a4Flow(c, item)
		if err != nil {
			return a4Fail(err)
		}
		if changed, err = plan.PutItemNote(wf, item, key, text); err != nil {
			return failAny(err)
		}
	}
	return Result{Data: keyData(key, changed), Text: key}, nil
}

func planRmIssue(c *Ctx, key string) (Result, error) {
	if err := validKey(key); err != nil {
		return Result{}, err
	}
	if _, _, isSlice := plan.SliceOf(key); isSlice {
		be, err := issuesBackend(c)
		if err != nil {
			return a4Fail(err)
		}
		if err := plan.RmSliceNote(be, key); err != nil {
			return failAny(err)
		}
	} else {
		item, _, _ := plan.ItemOf(key)
		_, wf, _, _, err := a4Flow(c, item)
		if err != nil {
			return a4Fail(err)
		}
		if err := plan.RmItemNote(wf, item); err != nil {
			return failAny(err)
		}
	}
	return Result{Data: keyData(key, true), Text: key}, nil
}

func planListIssue(c *Ctx, milestone string) (Result, error) {
	be, err := issuesBackend(c)
	if err != nil {
		return a4Fail(err)
	}
	c.Warn("item plans live on their issues (backlog.backend \"issues\"); listing slice plans from the milestone tracking issues only")
	list, err := plan.ListSlices(be, milestone)
	if err != nil {
		return failAny(err)
	}
	return planListResult(list), nil
}

// ---- proof

func proofAddIssue(c *Ctx, root, id string, o proof.AddOpts) (Result, error) {
	_, wf, cid, typ, err := a4Flow(c, id)
	if err != nil {
		return a4Fail(err)
	}
	row, changed, err := proof.AddNote(wf, id, root, o)
	if err != nil {
		return failAny(err)
	}
	d := typedData(cid, typ, nil)
	d.Set("check", row.Check)
	d.Set("result", row.Result)
	d.Set("sha", row.Sha)
	d.Set("evidence", row.Evidence)
	d.Set("changed", changed)
	return Result{Data: d, Text: cid}, nil
}

func proofShowIssue(c *Ctx, id string, countOnly bool) (Result, error) {
	_, wf, cid, typ, err := a4Flow(c, id)
	if err != nil {
		return a4Fail(err)
	}
	rows, lines, err := proof.ShowNote(wf, id)
	if err != nil {
		return failAny(err)
	}
	return proofResult(cid, typ, rows, lines, countOnly), nil
}

// ---- plan uncertain

func uncertainIssue(c *Ctx, id string) (Result, error) {
	be, _, _, _, err := a4Flow(c, id)
	if err != nil {
		return a4Fail(err)
	}
	it, err := be.Get(id)
	if err != nil {
		return a4Fail(err)
	}
	typ, reasons, err := plan.UncertainIssue(be, it)
	if err != nil {
		return failAny(err)
	}
	return uncertainResult(it.ID, typ, reasons)
}
