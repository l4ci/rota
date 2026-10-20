package cli

import (
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/acceptance"
	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/plan"
	"github.com/l4ci/rota/internal/rotatree"
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
	root, issue, err := modeRoot(c)
	if err != nil {
		return Result{}, err
	}
	if !issue {
		return Result{Data: jsonObj("blockedBy", "backend", "changed", false)},
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
	cfg := config.Load(rotatree.Config(root))
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
		"unknown", unknown, "noVerify", strs(r.NoVerify), "warnings", strs(warns))
	problems := len(lines)
	if r.Ok() {
		lines = []string{key + ": ok"}
	}
	for _, w := range warns {
		lines = append(lines, "warn: "+w)
	}
	if r.Ok() {
		return Result{Data: d, Text: strings.Join(lines, "\n")}, nil
	}
	return Result{Data: d, Text: strings.Join(lines, "\n")},
		Failed("%s: %d problem(s)", key, problems)
}
