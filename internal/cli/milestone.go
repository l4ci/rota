package cli

import (
	"errors"
	"flag"
	"strings"

	"github.com/l4ci/rota/internal/artifact"
	"github.com/l4ci/rota/internal/backlog"
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
		return ms.FileStore{Root: root}, nil
	}
	be, err := openIssues(c)
	if err != nil {
		return nil, err
	}
	warn := func(msg string) { c.Warn("%s", msg) }
	switch b := be.(type) {
	case *backlog.Issues:
		b.Warn = warn
		return b.MilestoneStore(), nil
	case *backlog.Umbrella:
		if home, err := b.HomeSub(); err == nil {
			home.Warn = warn
		}
		return b.MilestoneStore(), nil
	}
	return nil, Refused("%s works on the issue backend only", c.Path)
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
			{Name: "index", Summary: "regenerate the overview and vision block", Verb: noFlags(runMilestoneIndex)},
		}},
	}
}

func milestoneAdd(fs *flag.FlagSet) RunFunc {
	title := fs.String("title", "", "milestone title")
	summary := fs.String("summary", "", "one-paragraph summary")
	depends := fs.String("depends", "", "comma list of milestone IDs this depends on")
	return func(c *Ctx, args []string) (Result, error) {
		if err := noArgs(args); err != nil {
			return Result{}, err
		}
		if *title == "" || *summary == "" {
			return Result{}, Usage("--title and --summary are required")
		}
		st, err := openStore(c)
		if err != nil {
			return Result{}, err
		}
		id, err := st.Add(*title, *summary, artifact.SplitCSV(*depends))
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
			return milestoneFail(err, "id mismatch")
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
			return Result{Data: blocked(err, "heading in body"), Text: ""}, err
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
