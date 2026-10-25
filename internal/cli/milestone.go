package cli

import (
	"errors"
	"flag"
	"fmt"
	"slices"
	"strings"

	"github.com/l4ci/rota/internal/artifact"
	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/blockedby"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/jsonx"
	ms "github.com/l4ci/rota/internal/milestone"
)

// Glue for the milestone group. Every verb works on a milestone.Store
// opened once by openMilestones: .rota/milestones files, one repo's tracker
// issues, or an umbrella's.

// openMilestones is the milestone store for the project's backlog backend.
// Duplicate-tracking-issue notices go to stderr and into the envelope's
// warnings.
func openMilestones(c *Ctx) (ms.Store, error) {
	root, issue, err := modeRoot(c)
	if err != nil {
		return nil, err
	}
	if !issue {
		return ms.FileStore{Root: root, Today: c.deps().Today}, nil
	}
	be, err := openIssueBackend(c, "", false)
	if err != nil {
		return nil, err
	}
	return be.MilestoneStore(), nil
}

// milestoneFail maps a store error, whichever store raised it, onto the exit
// table. by names what blocked a refused write ("" for verbs that have none).
func milestoneFail(err error, by string) (Result, error) {
	var data any
	switch {
	case errors.Is(err, backlog.ErrMilestoneText):
		return Result{Data: blocked(&exitcode.Error{Exit: exitcode.ExitRefused}, by)}, Refused("%s", err.Error())
	case asArtifact(err) != nil:
		if by != "" {
			data = blocked(err, by)
		}
		return Result{Data: data}, err
	}
	return backlogFail(err)
}

// reindex regenerates MILESTONES.md and the vision block from st.
func reindex(c *Ctx, st ms.Store) (bool, error) {
	root, err := c.Root()
	if err != nil {
		return false, err
	}
	return ms.Reindex(root, st)
}

// openStore opens the milestone store, mapping a failure onto the exit table.
func openStore(c *Ctx) (ms.Store, error) {
	st, err := openMilestones(c)
	if err != nil {
		_, ferr := milestoneFail(err, "")
		return nil, ferr
	}
	return st, nil
}

func milestoneCommands() []*Command {
	return []*Command{
		{Name: "milestone", Summary: "vision milestones", Subs: []*Command{
			{Name: "add", Summary: "mint a milestone", Verb: milestoneAdd},
			{Name: "list", Summary: "list milestones", Verb: noFlags(runMilestoneList)},
			{Name: "show", Summary: "print a milestone", Verb: noFlags(runMilestoneShow)},
			{Name: "put", Summary: "replace a milestone's text", Verb: milestonePut},
			{Name: "overview", Summary: "replace the MILESTONES.md overview text", Verb: milestoneOverview},
			{Name: "status", Summary: "change a milestone's status", Verb: milestoneStatus},
			{Name: "active", Summary: "IDs of active milestones", Verb: noFlags(runMilestoneActive)},
			{Name: "check", Summary: "every criterion has a feature issue, every feature a ## Change on a listed capability", Verb: milestoneCheck},
			{Name: "index", Summary: "regenerate the overview and vision block", Verb: noFlags(runMilestoneIndex)},
		}},
	}
}

func milestoneAdd(fs *flag.FlagSet) RunFunc {
	title := fs.String("title", "", "milestone title")
	summary := fs.String("summary", "", "one-paragraph summary")
	depends := fs.String("depends", "", "comma list of milestone IDs this depends on")
	kind := fs.String("kind", ms.KindDelivery, strings.Join(ms.Kinds, "|"))
	return func(c *Ctx, args []string) (Result, error) {
		if err := noArgs(args); err != nil {
			return Result{}, err
		}
		if *title == "" || *summary == "" {
			return Result{}, Usage("--title and --summary are required")
		}
		if !slices.Contains(ms.Kinds, *kind) {
			return Result{}, Failed("unknown --kind %q: use %s", *kind, strings.Join(ms.Kinds, " or "))
		}
		st, err := openStore(c)
		if err != nil {
			return Result{}, err
		}
		id, err := st.Add(*title, *summary, artifact.SplitCSV(*depends), *kind)
		if err != nil {
			return milestoneFail(err, "")
		}
		d := jsonx.NewObject()
		d.Set("id", id)
		d.Set("changed", true)
		return Result{Data: d, Text: id}, nil
	}
}

func runMilestoneList(c *Ctx, args []string) (Result, error) {
	if err := noArgs(args); err != nil {
		return Result{}, err
	}
	st, err := openStore(c)
	if err != nil {
		return Result{}, err
	}
	list, err := st.List()
	if err != nil {
		return milestoneFail(err, "")
	}
	return milestoneListResult(list), nil
}

// milestoneListResult is milestone list's answer for either store.
func milestoneListResult(list []ms.Entry) Result {
	rows, text := []any{}, ""
	for _, m := range list {
		o := jsonx.NewObject()
		o.Set("id", m.ID)
		o.Set("title", m.Title)
		o.Set("status", m.Status)
		deps := make([]any, len(m.Depends))
		for i, d := range m.Depends {
			deps[i] = d
		}
		o.Set("depends", deps)
		o.Set("ready", m.Ready)
		rows = append(rows, o)
		text += m.ID + "\t" + m.Status + "\t" + m.Title + "\n"
	}
	d := jsonx.NewObject()
	d.Set("milestones", rows)
	return Result{Data: d, Text: text}
}

func runMilestoneShow(c *Ctx, args []string) (Result, error) {
	id, err := oneArg(args, "milestone ID")
	if err != nil {
		return Result{}, err
	}
	if !ms.ValidID(id) {
		return Result{}, Usage("milestone ID must match M\\d{2,} (e.g. M01, M03), got %q", id)
	}
	st, err := openStore(c)
	if err != nil {
		return Result{}, err
	}
	body, err := st.Show(id)
	if err != nil {
		return milestoneFail(err, "")
	}
	d := jsonx.NewObject()
	d.Set("id", id)
	d.Set("kind", ms.KindOf(body))
	d.Set("body", body)
	return Result{Data: d, Text: body}, nil
}

func milestonePut(fs *flag.FlagSet) RunFunc {
	file := bodyFlag(fs)
	return func(c *Ctx, args []string) (Result, error) {
		id, err := oneArg(args, "milestone ID")
		if err != nil {
			return Result{}, err
		}
		if !ms.ValidID(id) {
			return Result{}, Usage("milestone ID must match M\\d{2,} (e.g. M01, M03), got %q", id)
		}
		text, err := readBody(c, *file)
		if err != nil {
			return Result{}, err
		}
		st, err := openStore(c)
		if err != nil {
			return Result{}, err
		}
		changed, err := st.Put(id, text)
		if err != nil {
			return milestoneFail(err, blockedby.IDMismatch)
		}
		// A redraft carries no generated Items section; put it back so the list still agrees with the items.
		if _, err := syncMilestoneItems(c, id); err != nil {
			return backlogFail(err)
		}
		d := jsonx.NewObject()
		d.Set("id", id)
		d.Set("changed", changed)
		return Result{Data: d, Text: id}, nil
	}
}

// milestoneOverview replaces the overview text above the first "## " heading
// of MILESTONES.md, in file and issue mode alike: the file is tracked in both.
func milestoneOverview(fs *flag.FlagSet) RunFunc {
	file := bodyFlag(fs)
	return func(c *Ctx, args []string) (Result, error) {
		if err := noArgs(args); err != nil {
			return Result{}, err
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		text, err := readBody(c, *file)
		if err != nil {
			return Result{}, err
		}
		changed, err := ms.SetOverview(root, text)
		if err != nil {
			return Result{Data: blocked(err, blockedby.HeadingInBody), Text: ""}, err
		}
		d := jsonx.NewObject()
		d.Set("changed", changed)
		return Result{Data: d, Text: "overview"}, nil
	}
}

// blocked is the exit-4 failure data for a refusal that is not "exists".
func blocked(err error, by string) any {
	if ae := asArtifact(err); ae != nil && ae.Exit == 4 {
		d := jsonx.NewObject()
		d.Set("blockedBy", by)
		d.Set("changed", false)
		return d
	}
	return nil
}

func milestoneStatus(fs *flag.FlagSet) RunFunc {
	to := fs.String("to", "", "planned, active, shipped or archived")
	force := fs.Bool("force", false, "ship without checking open items and unticked criteria")
	return func(c *Ctx, args []string) (Result, error) {
		id, err := oneArg(args, "milestone ID")
		if err != nil {
			return Result{}, err
		}
		if !ms.ValidStatus(*to) {
			return Result{}, Usage("--to must be one of: %s", strings.Join(ms.Statuses, ", "))
		}
		if !ms.ValidID(id) {
			return Result{}, Usage("milestone ID must match M\\d{2,} (e.g. M01, M03), got %q", id)
		}
		st, err := openStore(c)
		if err != nil {
			return Result{}, err
		}
		if *to == "shipped" && !*force {
			res, err := milestoneShipAudit(c, st, id)
			if err != nil {
				return res, err
			}
		}
		changed, err := st.SetStatus(id, *to)
		if err != nil {
			return milestoneFail(err, "")
		}
		if _, err := reindex(c, st); err != nil {
			return milestoneFail(err, "")
		}
		d := jsonx.NewObject()
		d.Set("id", id)
		d.Set("status", *to)
		d.Set("changed", changed)
		return Result{Data: d, Text: id + " " + *to}, nil
	}
}

// milestoneShipAudit refuses (exit 4, nothing changed) to ship a milestone
// that still has open child items, the set `milestone check` reads, or
// unticked criteria in its own body.
func milestoneShipAudit(c *Ctx, st ms.Store, id string) (Result, error) {
	body, err := st.Show(id)
	if err != nil {
		return milestoneFail(err, "")
	}
	kids, err := milestoneChildren(c, id)
	if err != nil {
		return backlogFail(err)
	}
	ids := make([]string, len(kids))
	for i, k := range kids {
		ids[i] = k.ID
	}
	f := ms.Audit(body, ids)
	if f.Clean() {
		return Result{}, nil
	}
	d := jsonx.NewObject()
	d.Set("blockedBy", blockedby.UnfinishedMilestone)
	d.Set("changed", false)
	d.Set("open", strs(f.Open))
	d.Set("unticked", strs(f.Unticked))
	msg := fmt.Sprintf("%s is not done: %d open item(s), %d unticked criteria", id, len(f.Open), len(f.Unticked))
	return Result{Data: d}, Refused("%s", msg).WithHint(fmt.Sprintf("run `rota milestone check %s` to see the breakdown, or pass --force to ship anyway", id))
}

func runMilestoneActive(c *Ctx, args []string) (Result, error) {
	if err := noArgs(args); err != nil {
		return Result{}, err
	}
	st, err := openStore(c)
	if err != nil {
		return Result{}, err
	}
	list, err := st.List()
	if err != nil {
		return milestoneFail(err, "")
	}
	return milestoneActiveResult(ms.ActiveIDs(list)), nil
}

func milestoneActiveResult(ids []string) Result {
	rows := make([]any, len(ids))
	for i, id := range ids {
		rows[i] = id
	}
	d := jsonx.NewObject()
	d.Set("ids", rows)
	return Result{Data: d, Text: strings.Join(ids, "\n")}
}

func runMilestoneIndex(c *Ctx, args []string) (Result, error) {
	if err := noArgs(args); err != nil {
		return Result{}, err
	}
	st, err := openStore(c)
	if err != nil {
		return Result{}, err
	}
	changed, err := reindex(c, st)
	if err != nil {
		return milestoneFail(err, "")
	}
	d := jsonx.NewObject()
	d.Set("changed", changed)
	return Result{Data: d}, nil
}

// milestoneCheck holds a milestone's feature issues against its criteria and
// capabilities. Under the file backend it also regenerates the milestone files'
// Items sections and reports the ones that had drifted.
func milestoneCheck(fs *flag.FlagSet) RunFunc {
	strict := fs.Bool("strict", false, "fail on a criterion no feature issue serves")
	return func(c *Ctx, args []string) (Result, error) {
		id, err := oneArg(args, "milestone ID")
		if err != nil {
			return Result{}, err
		}
		if !ms.ValidID(id) {
			return Result{}, Usage("milestone ID must match M\\d{2,} (e.g. M01, M03), got %q", id)
		}
		st, err := openStore(c)
		if err != nil {
			return Result{}, err
		}
		drifted, err := syncMilestoneItems(c, id)
		if err != nil {
			return backlogFail(err)
		}
		body, err := st.Show(id)
		if err != nil {
			return milestoneFail(err, "")
		}
		kids, err := milestoneChildren(c, id)
		if err != nil {
			return backlogFail(err)
		}
		r := ms.Check(body, kids, *strict)
		res, err := milestoneCheckResult(id, r)
		if drifted {
			if d, ok := res.Data.(*jsonx.Object); ok {
				d.Set("itemsRegenerated", true)
			}
			res.Text += "\n" + id + ": regenerated the ## Items section (it had drifted from the items' Milestone fields)"
		}
		return withChildSync(c, id, res), err
	}
}

// withChildSync regenerates the tracking issue's child list under the tracker
// backend and adds what it found to the check result: a `children` object and,
// on drift, a line naming the refs the list gained and lost. A failure to
// regenerate is a warning; the check's own verdict stands.
func withChildSync(c *Ctx, id string, res Result) Result {
	be, err := openIssueBackend(c, "", false)
	if err != nil {
		return res
	}
	sy, ok := be.(interface {
		SyncMilestoneChildren(string) (backlog.ChildSync, error)
	})
	if !ok {
		return res
	}
	sync, err := sy.SyncMilestoneChildren(id)
	if err != nil {
		c.Warn("could not regenerate the child list of %s: %v", id, err)
		return res
	}
	d, _ := res.Data.(*jsonx.Object)
	if d != nil {
		d.Set("children", jsonObj("changed", sync.Changed, "count", sync.Count, "added", strs(sync.Added), "removed", strs(sync.Removed)))
	}
	if sync.Changed {
		line := fmt.Sprintf("children: list regenerated (added %s; removed %s)", refList(sync.Added), refList(sync.Removed))
		if res.Text == "ok" {
			res.Text = line
		} else {
			res.Text += "\n" + line
		}
	}
	return res
}

func refList(refs []string) string {
	if len(refs) == 0 {
		return "none"
	}
	return strings.Join(refs, ", ")
}

// syncMilestoneItems regenerates every milestone file's Items section under the
// file backend and reports whether milestone id's had drifted. Tracker backends
// keep the list on the tracking issue, so nothing is written there.
func syncMilestoneItems(c *Ctx, id string) (drifted bool, err error) {
	root, err := backlogScope(c)
	if err != nil {
		return false, err
	}
	be, err := openBacklog(c, root, false, "")
	if err != nil {
		return false, err
	}
	f, ok := be.(*backlog.File)
	if !ok {
		return false, nil
	}
	changed, err := f.SyncMilestones()
	for _, m := range changed {
		drifted = drifted || m == id
	}
	return drifted, err
}

// milestoneChildren is the milestone's backlog items as ms.Check reads them:
// id, type letter and detail body. Both `milestone check` and the release
// gate's check line build their report from it.
func milestoneChildren(c *Ctx, id string) ([]ms.Child, error) {
	root, err := backlogScope(c)
	if err != nil {
		return nil, err
	}
	be, err := openBacklog(c, root, false, "")
	if err != nil {
		return nil, err
	}
	rows, _, _, err := backlog.OpenRows(be)
	if err != nil {
		return nil, err
	}
	types := map[string]string{}
	for _, r := range rows {
		types[r.ID] = r.Type
	}
	var kids []ms.Child
	for _, cid := range backlog.IDsByMilestone(rows, id) {
		text, _, err := be.Detail(cid)
		if err != nil {
			return nil, err
		}
		kids = append(kids, ms.Child{ID: cid, Type: types[cid], Body: text})
	}
	return kids, nil
}

func milestoneCheckResult(id string, r ms.Report) (Result, error) {
	unknown, noMig, badTouch, badCap := []any{}, []any{}, []any{}, []any{}
	var lines []string
	if r.BelowThreshold {
		lines = append(lines, fmt.Sprintf("below threshold: %d feature items, %d needed", r.Features, ms.MinFeatures))
	} else {
		for _, x := range r.NoChange {
			lines = append(lines, x+": no ## Change section")
		}
		for _, b := range r.BadCapability {
			badCap = append(badCap, jsonObj("item", b.ID, "capability", b.Capability))
			lines = append(lines, fmt.Sprintf("%s: Change capability '%s' is not a capability of %s", b.ID, b.Capability, id))
		}
		for _, u := range r.Unknown {
			unknown = append(unknown, jsonObj("item", u.ID, "ids", strs(u.IDs)))
			for _, ac := range u.IDs {
				lines = append(lines, fmt.Sprintf("%s: cites %s, not a criterion of %s", u.ID, ac, id))
			}
		}
		for _, m := range r.NoMigration {
			noMig = append(noMig, jsonObj("item", m.ID, "fields", strs(m.Fields)))
			for _, f := range m.Fields {
				lines = append(lines, fmt.Sprintf("%s: REMOVED without %s", m.ID, f))
			}
		}
		for _, b := range r.BadTouches {
			badTouch = append(badTouch, jsonObj("item", b.ID, "entries", strs(b.Entries)))
			for _, e := range b.Entries {
				lines = append(lines, fmt.Sprintf("%s: Touches '%s' is not a capability of %s", b.ID, e, id))
			}
		}
		for _, ac := range r.Uncovered {
			l := ac + ": no feature issue serves it"
			if !r.Strict {
				l += " (warning)"
			}
			lines = append(lines, l)
		}
		if len(lines) == 0 {
			lines = append(lines, "ok")
		}
	}
	d := jsonObj("milestone", id, "ok", r.Ok(), "strict", r.Strict, "belowThreshold", r.BelowThreshold,
		"features", r.Features, "criteria", strs(r.Criteria), "uncovered", strs(r.Uncovered),
		"noChange", strs(r.NoChange), "unknown", unknown, "noMigration", noMig, "badTouches", badTouch, "badCapability", badCap)
	res := Result{Data: d, Text: strings.Join(lines, "\n")}
	if r.Ok() {
		return res, nil
	}
	return res, Failed("%s: milestone check failed", id)
}
