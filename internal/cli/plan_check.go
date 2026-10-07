package cli

import (
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/acceptance"
	"github.com/l4ci/rota/internal/plan"
)

// runPlanCheck is read-only: it holds the plan's tasks against the item's
// acceptance criteria. Criteria live in the item body, so like plan pass it
// needs the issue backend; unlike plan pass it is a read, so that case exits 1
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
	_, issue, err := modeRoot(c)
	if err != nil {
		return Result{}, err
	}
	if !issue {
		return Result{Data: jsonObj("blockedBy", "backend", "changed", false)},
			Failed("%s needs item criteria, which only the issue backend has (backlog.backend is not \"issues\")", c.Path)
	}
	_, st, err := openPlans(c)
	if err != nil {
		return Result{}, err
	}
	body, err := plan.Show(st, key)
	if err != nil {
		return failAny(err)
	}
	_, wf, _, _, err := itemFlow(c, ref)
	if err != nil {
		return backlogFailRead(err)
	}
	status, err := wf.Status(ref)
	if err != nil {
		return backlogFailRead(err)
	}
	var ids []string
	for _, cr := range acceptance.IDs(status.Body) {
		ids = append(ids, cr.ID)
	}
	r := plan.Check(body, ids)

	unknown := []any{}
	var lines []string
	for _, u := range r.Unknown {
		unknown = append(unknown, jsonObj("task", u.Task, "ids", strs(u.IDs)))
		lines = append(lines, fmt.Sprintf("%s serves unknown %s", u.Task, strings.Join(u.IDs, ", ")))
	}
	for _, id := range r.Uncovered {
		lines = append(lines, id+" has no task")
	}
	for _, t := range r.Orphans {
		lines = append(lines, t+" serves no criterion")
	}
	for _, t := range r.NoVerify {
		lines = append(lines, t+" has no Verify step")
	}
	d := jsonObj("key", key, "ok", r.Ok(), "criteria", strs(ids),
		"uncovered", strs(r.Uncovered), "orphans", strs(r.Orphans),
		"unknown", unknown, "noVerify", strs(r.NoVerify))
	if r.Ok() {
		return Result{Data: d, Text: key + ": ok"}, nil
	}
	return Result{Data: d, Text: strings.Join(lines, "\n")},
		Failed("%s: %d problem(s)", key, len(lines))
}
