package cli

import (
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/l4ci/rota/internal/acceptance"
	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/blockedby"
	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/design"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/plan"
)

// planTaskShape is the one task shape plan check reads.
const planTaskShape = "a task is a `- **T1** — title` bullet under `## Tasks` with indented `Serves:` and `Verify:` sub-bullets"

// runPlanCheck is read-only: it holds the plan's tasks against the item's
// acceptance criteria. Criteria live in the item body, or in the design note
// when the body has none, so like plan pass it needs the issue backend; unlike plan pass it is a read, so that case exits 1
// rather than 4.
func runPlanCheck(c *Ctx, args []string) (Result, error) {
	key, err := oneArg(args, "plan key")
	if err != nil {
		return Result{}, err
	}
	ref, err := passItemRef("plan check", key)
	if err != nil {
		return Result{}, err
	}
	root, issue, err := modeRoot(c)
	if err != nil {
		return Result{}, err
	}
	if !issue {
		return Result{Data: blockedData(nil, blockedby.Backend)},
			Failed("%s needs item criteria, which only the issue backend has (issues.source is file)", c.Path)
	}
	_, st, err := openPlans(c)
	if err != nil {
		return Result{}, err
	}
	body, err := plan.Show(st, key)
	if err != nil {
		return failAny(err)
	}
	be, wf, _, _, err := itemTrack(c, ref, "plan check", backlog.FileShowHint)
	if err != nil {
		return backlogFailRead(err)
	}
	status, err := wf.Status(ref)
	if err != nil {
		return backlogFailRead(err)
	}
	var ids []string
	crits := acceptance.IDs(status.Body)
	if len(crits) == 0 {
		text, err := designText(c, ref)
		if err != nil {
			return failAny(err)
		}
		crits = acceptance.IDs(text)
	}
	for _, cr := range crits {
		ids = append(ids, cr.ID)
	}
	rows, err := be.Comments(ref, "decision")
	if err != nil {
		return backlogFailRead(err)
	}
	var decs, decLines []string
	for i, row := range rows {
		id := fmt.Sprintf("D%d", i+1)
		decs = append(decs, id)
		first, _, _ := strings.Cut(strings.TrimSpace(row.Text), "\n")
		first = strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return -1
			}
			return r
		}, first)
		if rs := []rune(first); len(rs) > 60 {
			first = string(rs[:60])
		}
		decLines = append(decLines, fmt.Sprintf("%s: %s", id, first))
	}
	r := plan.Check(body, ids, decs)
	cfg := c.projectConfig(root)
	var lim plan.Limits
	if lim.MaxTasks, err = config.Int(cfg, "plan.maxTasks", 0, config.MaxInt); err != nil {
		return Result{}, Failed("%v", err)
	}
	if lim.MaxFilesPerTask, err = config.Int(cfg, "plan.maxFilesPerTask", 0, config.MaxInt); err != nil {
		return Result{}, Failed("%v", err)
	}
	warns := plan.CheckSize(body, lim)

	unknown := []any{}
	var lines []string
	for _, u := range r.Unknown {
		unknown = append(unknown, jsonx.Obj("task", u.Task, "ids", jsonx.Strings(u.IDs)))
		lines = append(lines, fmt.Sprintf("%s serves unknown %s", u.Task, strings.Join(u.IDs, ", ")))
	}
	for _, id := range r.Uncovered {
		lines = append(lines, id+" has no task")
	}
	for _, u := range r.UnknownDecisions {
		lines = append(lines, fmt.Sprintf("%s honors unknown %s", u.Task, strings.Join(u.IDs, ", ")))
	}
	for _, id := range r.UncoveredDecisions {
		lines = append(lines, id+" is honored by no task")
	}
	for _, t := range r.Orphans {
		lines = append(lines, t+" serves no criterion")
	}
	for _, t := range r.NoVerify {
		lines = append(lines, t+" has no Verify step")
	}
	d := jsonx.Obj("key", key, "ok", r.Ok(), "criteria", jsonx.Strings(ids),
		"uncovered", jsonx.Strings(r.Uncovered), "orphans", jsonx.Strings(r.Orphans),
		"unknown", unknown, "noVerify", jsonx.Strings(r.NoVerify), "warnings", jsonx.Strings(warns))
	if len(decs) > 0 || len(r.UnknownDecisions) > 0 {
		unknownDec := []any{}
		for _, u := range r.UnknownDecisions {
			unknownDec = append(unknownDec, jsonx.Obj("task", u.Task, "ids", jsonx.Strings(u.IDs)))
		}
		d.Set("decisions", jsonx.Strings(decLines))
		d.Set("uncoveredDecisions", jsonx.Strings(r.UncoveredDecisions))
		d.Set("unknownDecisions", unknownDec)
	}
	problems := len(lines)
	// Neither of these shows in the lists above in a way that says what to fix.
	var hints []string
	if len(ids) == 0 {
		hints = append(hints, "no acceptance ids: looked for `- [ ]` lines under `## Acceptance` in the issue body and in the design note")
	}
	if r.NoTasks {
		hints = append(hints, "no tasks found: "+planTaskShape)
	}
	ok := r.Ok() && len(ids) > 0
	d.Set("ok", ok)
	if ok {
		lines = []string{key + ": ok"}
	}
	lines = append(lines, decLines...)
	for _, w := range warns {
		lines = append(lines, "warn: "+w)
	}
	if ok {
		return Result{Data: d, Text: strings.Join(lines, "\n")}, nil
	}
	why := hints
	if problems > 0 {
		why = append([]string{fmt.Sprintf("%d problem(s)", problems)}, hints...)
	}
	return Result{Data: d, Text: strings.Join(lines, "\n")},
		Failed("%s: %s", key, strings.Join(why, "; "))
}

// designText is the item's design note, "" when it has none.
func designText(c *Ctx, ref string) (string, error) {
	st, _, err := openDesign(c, ref)
	if err != nil {
		return "", err
	}
	text, err := design.Show(st, ref)
	var ae *exitcode.Error
	if errors.As(err, &ae) && ae.Exit == exitcode.ExitResolution {
		return "", nil
	}
	return text, err
}
