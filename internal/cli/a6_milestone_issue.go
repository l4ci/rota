package cli

import (
	"errors"
	"github.com/l4ci/rota/internal/exitcode"
	"time"

	"github.com/l4ci/rota/internal/artifact"
	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/jsonx"
	ms "github.com/l4ci/rota/internal/milestone"
)

// The issue-mode halves of the milestone verbs: a milestone is a native
// milestone plus a tracking issue, all handled by backlog.Issues
// (internal/backlog/milestones.go, shared with the issue migration).

func milestoneAddIssue(c *Ctx, title, summary, depends string) (Result, error) {
	be, err := milestonesBackend(c)
	if err != nil {
		return a4Fail(err)
	}
	id, err := be.MilestoneAdd("", title, summary, artifact.SplitCSV(depends), time.Now().Format("2006-01-02"))
	if err != nil {
		return a4Fail(err)
	}
	d := jsonx.NewObject()
	d.Set("id", id)
	d.Set("changed", true)
	return Result{Data: d, Text: id}, nil
}

func entriesOf(rows []backlog.MilestoneRow) []ms.Entry {
	out := make([]ms.Entry, len(rows))
	for i, r := range rows {
		out[i] = ms.Entry{ID: r.ID, Title: r.Title, Status: r.Status, Depends: r.Depends, Ready: r.Ready}
	}
	return out
}

func milestoneListIssue(c *Ctx) (Result, error) {
	be, err := milestonesBackend(c)
	if err != nil {
		return a4Fail(err)
	}
	rows, err := be.MilestoneList()
	if err != nil {
		return a4Fail(err)
	}
	return milestoneListResult(entriesOf(rows)), nil
}

func milestoneShowIssue(c *Ctx, id string) (Result, error) {
	if !ms.ValidID(id) {
		return Result{}, Usage("milestone ID must match M\\d{2,} (e.g. M01, M03), got %q", id)
	}
	be, err := milestonesBackend(c)
	if err != nil {
		return a4Fail(err)
	}
	body, err := be.MilestoneShow(id)
	if err != nil {
		return a4Fail(err)
	}
	body += "\n" // the old helper printed the plan with a newline
	d := jsonx.NewObject()
	d.Set("id", id)
	d.Set("body", body)
	return Result{Data: d, Text: body}, nil
}

func milestonePutIssue(c *Ctx, id, file string) (Result, error) {
	if !ms.ValidID(id) {
		return Result{}, Usage("milestone ID must match M\\d{2,} (e.g. M01, M03), got %q", id)
	}
	text, err := readBody(c, file)
	if err != nil {
		return Result{}, err
	}
	be, err := milestonesBackend(c)
	if err != nil {
		return a4Fail(err)
	}
	before, berr := be.MilestoneShow(id)
	if berr != nil {
		return a4Fail(berr)
	}
	if err := be.MilestonePut(id, text); err != nil {
		if errors.Is(err, backlog.ErrMilestoneText) {
			return Result{Data: blocked(&exitcode.Error{Exit: exitcode.ExitRefused}, "id mismatch")}, Refused("%s", err.Error())
		}
		return a4Fail(err)
	}
	after, aerr := be.MilestoneShow(id)
	if aerr != nil {
		return a4Fail(aerr)
	}
	d := jsonx.NewObject()
	d.Set("id", id)
	d.Set("changed", before != after)
	return Result{Data: d, Text: id}, nil
}

func milestoneStatusIssue(c *Ctx, id, to string) (Result, error) {
	if !ms.ValidID(id) {
		return Result{}, Usage("milestone ID must match M\\d{2,} (e.g. M01, M03), got %q", id)
	}
	be, err := milestonesBackend(c)
	if err != nil {
		return a4Fail(err)
	}
	rows, err := be.MilestoneList()
	if err != nil {
		return a4Fail(err)
	}
	was := ""
	for _, r := range rows {
		if r.ID == id {
			was = r.Status
		}
	}
	if err := be.MilestoneStatus(id, to); err != nil {
		return a4Fail(err)
	}
	if _, err := indexIssue(c, be); err != nil {
		return Result{}, err
	}
	d := jsonx.NewObject()
	d.Set("id", id)
	d.Set("status", to)
	d.Set("changed", was != to)
	return Result{Data: d, Text: id + " " + to}, nil
}

func milestoneActiveIssue(c *Ctx) (Result, error) {
	be, err := milestonesBackend(c)
	if err != nil {
		return a4Fail(err)
	}
	rows, err := be.MilestoneList()
	if err != nil {
		return a4Fail(err)
	}
	ids := []string{}
	for _, r := range rows {
		if r.Status == "active" {
			ids = append(ids, r.ID)
		}
	}
	return milestoneActiveResult(ids), nil
}

func indexIssue(c *Ctx, be milestoneBackend) (bool, error) {
	rows, err := be.MilestoneList()
	if err != nil {
		_, ferr := a4Fail(err)
		return false, ferr
	}
	root, err := c.Root()
	if err != nil {
		return false, err
	}
	changed, err := ms.IndexFrom(root, entriesOf(rows), true)
	return changed, err
}

func milestoneIndexIssue(c *Ctx) (Result, error) {
	be, err := milestonesBackend(c)
	if err != nil {
		return a4Fail(err)
	}
	changed, err := indexIssue(c, be)
	if err != nil {
		return Result{}, err
	}
	d := jsonx.NewObject()
	d.Set("changed", changed)
	return Result{Data: d}, nil
}
