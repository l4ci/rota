package cli

import (
	"flag"
	"os"
	"strings"

	"github.com/l4ci/rota/internal/artifact"
	"github.com/l4ci/rota/internal/design"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/plan"
)

// Glue for the design and plan groups (A6). Add, show, put and rm run on
// whichever store the backend gives them (a6_stores.go); list, amend and the
// plan checks work on the files and refuse under backlog.backend "issues".

func docsCommands() []*Command {
	return []*Command{
		{Name: "design", Summary: "per-item design documents", Subs: []*Command{
			{Name: "add", Summary: "create a design stub", Verb: designAdd},
			{Name: "list", Summary: "list designs", Verb: noFlags(runDesignList)},
			{Name: "show", Summary: "print a design", Verb: noFlags(runDesignShow)},
			{Name: "put", Summary: "replace a design's text", Verb: designPut},
			{Name: "rm", Summary: "delete a design", Verb: noFlags(runDesignRm)},
			{Name: "amend", Summary: "amend one section of a design", Verb: designAmend},
		}},
		{Name: "plan", Summary: "milestone and item plans", Subs: []*Command{
			{Name: "add", Summary: "create a plan stub", Verb: planAdd},
			{Name: "list", Summary: "list plans", Verb: planList},
			{Name: "show", Summary: "print a plan", Verb: noFlags(runPlanShow)},
			{Name: "put", Summary: "replace a plan's text", Verb: planPut},
			{Name: "rm", Summary: "delete a plan", Verb: noFlags(runPlanRm)},
			{Name: "validate-docs", Summary: "check doc-by-path deliverables", Verb: noFlags(runPlanValidateDocs)},
			{Name: "rename-check", Summary: "files that mention a name", Verb: noFlags(runPlanRenameCheck)},
			{Name: "uncertain", Summary: "uncertainty pre-flight for an item", Verb: noFlags(runPlanUncertain)},
		}},
	}
}

// fileRoot returns the project root for a file-only verb. Under issue mode
// the verb is under the wrong backend: a mutating verb exits 4, a read-only
// one exits 1 (the conventions forbid 4 there), both with the failure data
// {"blockedBy": "backend", "changed": false}.
func fileRoot(c *Ctx, readOnly bool) (string, Result, error) {
	root, err := c.Root()
	if err != nil {
		return "", Result{}, err
	}
	if !artifact.IssueMode(root) {
		return root, Result{}, nil
	}
	d := jsonx.NewObject()
	d.Set("blockedBy", "backend")
	d.Set("changed", false)
	msg := "%s works on the file backend only (backlog.backend is \"issues\")"
	if readOnly {
		return "", Result{Data: d}, Failed(msg, c.Path)
	}
	return "", Result{Data: d}, Refused(msg, c.Path)
}

func bodyFlag(fs *flag.FlagSet) *string {
	return fs.String("body-file", "", "file holding the new text (`-` for stdin)")
}

func readBody(c *Ctx, file string) (string, error) {
	if file == "" {
		return "", Usage("--body-file is required")
	}
	s, err := artifact.ReadBody(c.Stdin, file)
	return s, fromArtifact(err)
}

func idData(id string, changed any) *jsonx.Object {
	d := jsonx.NewObject()
	d.Set("id", id)
	d.Set("type", design.Type(idOr(id)))
	if changed != nil {
		d.Set("changed", changed)
	}
	return d
}

func idOr(id string) string {
	if id == "" {
		return "?"
	}
	return id
}

// ---- design

func designAdd(fs *flag.FlagSet) RunFunc {
	title := fs.String("title", "", "design title")
	return func(c *Ctx, args []string) (Result, error) {
		id, err := oneArg(args, "item ID")
		if err != nil {
			return Result{}, err
		}
		if *title == "" {
			return Result{}, Usage("--title is required")
		}
		st, w, err := openDesign(c, id)
		if err != nil {
			return Result{}, err
		}
		if err := design.Add(st, id, *title); err != nil {
			return failAny(err)
		}
		return Result{Data: typedData(w.ID, w.Type, true), Text: w.ID}, nil
	}
}

// refusal is the exit-4 failure data: {"blockedBy": "exists", "changed": false}.
func refusal(err error) any {
	if ae, ok := err.(*artifact.Error); ok && ae.Exit == artifact.ExitRefused {
		d := jsonx.NewObject()
		d.Set("blockedBy", "exists")
		d.Set("changed", false)
		return d
	}
	return nil
}

func runDesignList(c *Ctx, args []string) (Result, error) {
	if err := noArgs(args); err != nil {
		return Result{}, err
	}
	root, res, err := fileRoot(c, true)
	if err != nil {
		return res, err
	}
	list, err := design.List(root)
	if err != nil {
		return Result{}, err
	}
	rows, text := []any{}, ""
	for _, e := range list {
		o := jsonx.NewObject()
		o.Set("id", e.ID)
		o.Set("title", e.Title)
		o.Set("status", e.Status)
		o.Set("created", e.Created)
		rows = append(rows, o)
		text += e.ID + "\t" + e.Status + "\t" + e.Title + "\n"
	}
	d := jsonx.NewObject()
	d.Set("designs", rows)
	return Result{Data: d, Text: text}, nil
}

func runDesignShow(c *Ctx, args []string) (Result, error) {
	id, err := oneArg(args, "item ID")
	if err != nil {
		return Result{}, err
	}
	st, w, err := openDesign(c, id)
	if err != nil {
		return Result{}, err
	}
	body, err := design.Show(st, id)
	if err != nil {
		return failAny(err)
	}
	d := typedData(w.ID, w.Type, nil)
	d.Set("body", body)
	return Result{Data: d, Text: body}, nil
}

func designPut(fs *flag.FlagSet) RunFunc {
	file := bodyFlag(fs)
	return func(c *Ctx, args []string) (Result, error) {
		id, err := oneArg(args, "item ID")
		if err != nil {
			return Result{}, err
		}
		st, w, err := openDesign(c, id)
		if err != nil {
			return Result{}, err
		}
		if err := design.CheckID(st, id); err != nil {
			return failAny(err)
		}
		text, err := readBody(c, *file)
		if err != nil {
			return Result{}, err
		}
		changed, err := design.Put(st, id, text)
		if err != nil {
			return failAny(err)
		}
		return Result{Data: typedData(w.ID, w.Type, changed), Text: w.ID}, nil
	}
}

func runDesignRm(c *Ctx, args []string) (Result, error) {
	id, err := oneArg(args, "item ID")
	if err != nil {
		return Result{}, err
	}
	st, w, err := openDesign(c, id)
	if err != nil {
		return Result{}, err
	}
	if err := design.Rm(st, id); err != nil {
		return failAny(err)
	}
	return Result{Data: typedData(w.ID, w.Type, true), Text: w.ID}, nil
}

func designAmend(fs *flag.FlagSet) RunFunc {
	heading := fs.String("section", "", "heading to amend (without ##)")
	mode := fs.String("mode", "", "append or replace")
	file := bodyFlag(fs)
	return func(c *Ctx, args []string) (Result, error) {
		id, err := oneArg(args, "item ID")
		if err != nil {
			return Result{}, err
		}
		root, res, err := fileRoot(c, false) // mutating, file-only
		if err != nil {
			return res, err
		}
		if *heading == "" {
			return Result{}, Usage("--section is required")
		}
		if *mode != "append" && *mode != "replace" {
			return Result{}, Usage("--mode must be append or replace")
		}
		if !design.ValidID(id) {
			_, err := design.Amend("", id, *heading, *mode, "")
			return Result{}, fromArtifact(err)
		}
		text, err := readBody(c, *file)
		if err != nil {
			return Result{}, err
		}
		changed, err := design.Amend(root, id, *heading, *mode, text)
		if err != nil {
			return Result{}, fromArtifact(err)
		}
		d := idData(id, nil)
		d.Set("section", *heading)
		d.Set("mode", *mode)
		d.Set("changed", changed)
		return Result{Data: d, Text: id}, nil
	}
}

// ---- plan

func planAdd(fs *flag.FlagSet) RunFunc {
	title := fs.String("title", "", "plan title")
	designID := fs.String("design", "", "design item ID to link")
	repos := fs.String("repos", "", "comma list of sub-repos")
	milestone := fs.String("milestone", "", "milestone for a minted slice key")
	slice := fs.Bool("slice", false, "mint the next S<NN> key for --milestone")
	return func(c *Ctx, args []string) (Result, error) {
		if len(args) > 1 {
			return Result{}, Usage("expected one plan key, got %d arguments", len(args))
		}
		o := plan.AddOpts{Milestone: *milestone, Slice: *slice, Title: *title, Design: *designID, Repos: *repos}
		if len(args) == 1 {
			o.Key = args[0]
		}
		root, st, err := openPlans(c)
		if err != nil {
			return Result{}, err
		}
		key, kind, err := plan.Add(root, st, o)
		if err != nil {
			return failAny(err)
		}
		d := jsonx.NewObject()
		d.Set("key", key)
		d.Set("unitKind", kind)
		d.Set("changed", true)
		return Result{Data: d, Text: key}, nil
	}
}

func planList(fs *flag.FlagSet) RunFunc {
	milestone := fs.String("milestone", "", "only this milestone's plans")
	return func(c *Ctx, args []string) (Result, error) {
		if err := noArgs(args); err != nil {
			return Result{}, err
		}
		if *milestone != "" && !plan.ValidMilestone(*milestone) {
			return Result{}, Usage("--milestone must look like M01, got %q", *milestone)
		}
		_, st, err := openPlans(c)
		if err != nil {
			return Result{}, err
		}
		list, err := plan.List(st, *milestone)
		if err != nil {
			return failAny(err)
		}
		return planListResult(list), nil
	}
}

// planListResult is plan list's answer for either mode.
func planListResult(list []plan.Entry) Result {
	rows, text := []any{}, ""
	for _, e := range list {
		o := jsonx.NewObject()
		o.Set("key", e.Key)
		o.Set("milestone", e.Milestone)
		o.Set("unit", e.Unit)
		o.Set("unitKind", e.UnitKind)
		o.Set("title", e.Title)
		o.Set("status", e.Status)
		o.Set("created", e.Created)
		repos := make([]any, len(e.Repos))
		for i, r := range e.Repos {
			repos[i] = r
		}
		o.Set("repos", repos)
		rows = append(rows, o)
		text += e.Key + "\t" + e.Status + "\t" + e.Title + "\n"
	}
	d := jsonx.NewObject()
	d.Set("plans", rows)
	return Result{Data: d, Text: text}
}

func keyData(key string, changed any) *jsonx.Object {
	d := jsonx.NewObject()
	d.Set("key", key)
	if changed != nil {
		d.Set("changed", changed)
	}
	return d
}

func runPlanShow(c *Ctx, args []string) (Result, error) {
	key, err := oneArg(args, "plan key")
	if err != nil {
		return Result{}, err
	}
	_, st, err := openPlans(c)
	if err != nil {
		return Result{}, err
	}
	body, err := plan.Show(st, key)
	if err != nil {
		return failAny(err)
	}
	d := keyData(key, nil)
	d.Set("body", body)
	return Result{Data: d, Text: body}, nil
}

func planPut(fs *flag.FlagSet) RunFunc {
	file := bodyFlag(fs)
	return func(c *Ctx, args []string) (Result, error) {
		key, err := oneArg(args, "plan key")
		if err != nil {
			return Result{}, err
		}
		_, st, err := openPlans(c)
		if err != nil {
			return Result{}, err
		}
		if err := plan.CheckKey(st, key); err != nil {
			return failAny(err)
		}
		text, err := readBody(c, *file)
		if err != nil {
			return Result{}, err
		}
		changed, err := plan.Put(st, key, text)
		if err != nil {
			return failAny(err)
		}
		return Result{Data: keyData(key, changed), Text: key}, nil
	}
}

func runPlanRm(c *Ctx, args []string) (Result, error) {
	key, err := oneArg(args, "plan key")
	if err != nil {
		return Result{}, err
	}
	_, st, err := openPlans(c)
	if err != nil {
		return Result{}, err
	}
	if err := plan.Rm(st, key); err != nil {
		return failAny(err)
	}
	return Result{Data: keyData(key, true), Text: key}, nil
}

func runPlanValidateDocs(c *Ctx, args []string) (Result, error) {
	key, err := oneArg(args, "plan key")
	if err != nil {
		return Result{}, err
	}
	root, res, err := fileRoot(c, true)
	if err != nil {
		return res, err
	}
	ms, text, err := plan.ValidateDocs(root, key)
	if err != nil {
		return Result{}, fromArtifact(err)
	}
	rows := []any{}
	for _, m := range ms {
		o := jsonx.NewObject()
		o.Set("path", m.Path)
		if m.TargetRepo != "" {
			o.Set("targetRepo", m.TargetRepo)
		}
		o.Set("issue", m.Issue)
		if m.Suggestion != "" {
			o.Set("suggestion", m.Suggestion)
		}
		rows = append(rows, o)
	}
	d := keyData(key, nil)
	d.Set("valid", len(ms) == 0)
	d.Set("mismatches", rows)
	return Result{Data: d, Text: text}, nil
}

func runPlanRenameCheck(c *Ctx, args []string) (Result, error) {
	if len(args) == 0 {
		return Result{}, Usage("missing <old> name")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return Result{}, err
	}
	files := plan.RenameCheck(cwd, args[0], args[1:])
	rows := make([]any, len(files))
	for i, f := range files {
		rows[i] = f
	}
	d := jsonx.NewObject()
	d.Set("files", rows)
	return Result{Data: d, Text: strings.Join(files, "\n")}, nil
}
