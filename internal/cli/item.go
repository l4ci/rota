package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/exitmap"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/pystr"
	"github.com/l4ci/rota/internal/worker"
)

// The item verbs: `rota id next` and `rota item create|field|complete|reopen|
// rm|shipped|ready|comment`. Shapes, flags and exits are the verb contract's
// (docs/contributing/contract/); the file backend does the work.

// newTracker builds the issue tracker the issue backend reads and writes
// through: the gh or glab adapter for the project's origin, configured by
// issues.*. Deps.NewTracker builds it, so unit tests inject a fake.

// withReadOnly joins the tracker groups and wraps their read-only verbs.
func withReadOnly(groups ...[]*Command) []*Command {
	var cmds []*Command
	for _, g := range groups {
		cmds = append(cmds, g...)
	}
	markReadOnly(cmds, "")
	return cmds
}

// readOnlyVerbs are the tracker verbs whose contract data carries no "changed".
// The conventions forbid exit 4 for them, so a refusal (the wrong backend)
// answers exit 1 with the same failure data (contract: backend). A test checks
// this set against docs/contributing/contract/.
var readOnlyVerbs = map[string]bool{
	"update": true, "config show": true, "config check": true,
	"repo which": true, "repo resolve": true, "repo umbrella": true,
	"item show": true, "item ready": true, "item comment list": true, "item note show": true,
	"item field get": true, "item field list": true, "item shipped": true,
	"backlog list": true, "backlog ids": true, "backlog milestones": true, "backlog drift": true,
	"backlog stale": true, "summary": true,
	"issues imported": true, "issues provider": true,
	"status show": true, "status handoff": true,
	"refactor age": true, "refactor targets": true,
}

// markReadOnly wraps every verb in readOnlyVerbs under cmds so an exit 4
// becomes exit 1; prefix is the command path above cmds.
func markReadOnly(cmds []*Command, prefix string) {
	for _, cmd := range cmds {
		path := strings.TrimSpace(prefix + " " + cmd.Name)
		if cmd.Verb != nil && readOnlyVerbs[path] {
			verb := cmd.Verb
			cmd.Verb = func(fs *flag.FlagSet) RunFunc {
				run := verb(fs)
				return func(c *Ctx, args []string) (Result, error) {
					res, err := run(c, args)
					var e *Error
					if errors.As(err, &e) && e.Exit == ExitRefused {
						e.Exit = ExitFailed
					}
					return res, err
				}
			}
		}
		markReadOnly(cmd.Subs, path)
	}
}

func itemCommands() []*Command {
	return []*Command{
		{Name: "id", Summary: "mint item and milestone IDs", Subs: []*Command{
			{Name: "next", Summary: "mint the next counter ID", Repo: true, Verb: idNext},
		}},
		{Name: "item", Summary: "backlog items", Subs: []*Command{
			{Name: "create", Summary: "capture one item", Repo: true, Verb: itemCreate},
			{Name: "field", Summary: "read and write item fields", Subs: []*Command{
				{Name: "get", Summary: "print one field of an item", Repo: true, Verb: itemFieldGet},
				{Name: "set", Summary: "set, replace or clear a field of an open item", Repo: true, Verb: itemFieldSet},
				{Name: "list", Summary: "every field of an item", Repo: true, Verb: itemFieldList},
			}},
			{Name: "complete", Summary: "close an item", Repo: true, Verb: itemComplete},
			{Name: "reopen", Summary: "restore a completed item", Repo: true, Verb: itemReopen},
			{Name: "rm", Summary: "remove items with their cross-references and files", Repo: true, Verb: itemRm},
			{Name: "shipped", Summary: "look for evidence that titles already shipped", Repo: true, Verb: itemShipped},
			{Name: "ready", Summary: "is the item specified well enough to start", Repo: true, Verb: itemReady},
			{Name: "show", Summary: "status block of an issue-mode item", Repo: true, Verb: itemShow},
			{Name: "claim", Summary: "take an item so two agents never work it at once", Repo: true, Verb: itemClaim},
			{Name: "release", Summary: "give a claimed item back", Repo: true, Verb: itemRelease},
			{Name: "state", Summary: "set the workflow state label of an item", Repo: true, Verb: itemState},
			{Name: "note", Summary: "durable item notes (issue mode)", Subs: []*Command{
				{Name: "add", Summary: "write a note", Repo: true, Verb: itemNoteAdd},
				{Name: "show", Summary: "print a note", Repo: true, Verb: itemNoteShow},
				{Name: "rm", Summary: "delete a note", Repo: true, Verb: itemNoteRm},
			}},
			{Name: "comment", Summary: "item comments", Subs: []*Command{
				{Name: "add", Summary: "append a comment", Repo: true, Verb: itemCommentAdd},
				{Name: "list", Summary: "list comments", Repo: true, Verb: itemCommentList},
			}},
		}},
	}
}

// ---- shared helpers -------------------------------------------------------

func jsonObj(kv ...any) *jsonx.Object {
	o := jsonx.NewObject()
	for i := 0; i+1 < len(kv); i += 2 {
		o.Set(kv[i].(string), kv[i+1])
	}
	return o
}

// argCount checks the positional count of a verb.
func argCount(c *Ctx, args []string, min, max int, usage string) error {
	if len(args) < min || (max >= 0 && len(args) > max) {
		return Usage("%s", usage)
	}
	return nil
}

// backlogFail maps a backlog error to a verb failure and, for a refusal, its
// failure data. An unclassified error passes through.
func backlogFail(err error) (Result, error) {
	data, ferr := exitmap.Translate(err, exitmap.Options{Classes: exitmap.Backlog})
	return Result{Data: data}, ferr
}

// backlogFailRead is backlogFail for a read-only verb: the conventions forbid exit 4
// there, so a refusal (the wrong backend) becomes exit 1 with the same data.
func backlogFailRead(err error) (Result, error) {
	data, ferr := exitmap.Translate(err, exitmap.Options{Classes: exitmap.Backlog, ReadOnly: true})
	return Result{Data: data}, ferr
}

// resolveItem is the canonical ID and type of the item behind ref (contract rule 11).
// File mode keeps the reference as typed. Issue mode asks the tracker, so an
// unknown number, a type-letter mismatch or a milestone tracker is exit 3
// before anything is written, and "F7" or "#7" both answer "7" and "F".
func resolveItem(be backlog.Backend, ref string) (id, typ string, err error) {
	if !be.Capabilities().IssueIDs {
		return ref, backlog.ItemType(ref), nil
	}
	it, err := be.Get(ref)
	if err != nil {
		return "", "", err
	}
	return it.ID, it.Type, nil
}

// withBackend opens the backlog and runs fn on it; every backlog error comes
// back as the verb's failure. fileOnly refuses the issue backend with hint.
func withBackend(c *Ctx, root string, fileOnly bool, hint string, fn func(be backlog.Backend) (Result, error)) (Result, error) {
	be, err := openBacklog(c, root, fileOnly, hint)
	if err != nil {
		return backlogFail(err)
	}
	res, err := fn(be)
	var e *Error
	if err != nil && !errors.As(err, &e) {
		return backlogFail(err)
	}
	return res, err
}

// withItem resolves ref to its canonical ID and type, then runs fn. A backlog
// error becomes the verb's failure; a Result fn returns beside an *Error (a
// verdict such as "not ready") is kept.
func withItem(be backlog.Backend, ref string, fn func(id, typ string) (Result, error)) (Result, error) {
	id, typ, err := resolveItem(be, ref)
	if err != nil {
		return backlogFail(err)
	}
	res, err := fn(id, typ)
	var e *Error
	if err != nil && !errors.As(err, &e) {
		return backlogFail(err)
	}
	return res, err
}

func todayDate(c *Ctx) string { return c.deps().Today().Format("2006-01-02") }

// givenFlags is the set of flags the parser saw, by name.
func givenFlags(fs *flag.FlagSet) map[string]bool {
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	return set
}

// readInputFile reads a --body-file style path; "-" is stdin.
func readInputFile(c *Ctx, path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(c.Stdin)
	}
	return os.ReadFile(path)
}

// readInputErr is exit 3: a named input that cannot be read did not resolve.
func readInputErr(flagName, path string, err error) error {
	return Resolution("cannot read --%s: %s: %s", flagName, unwrapPathErr(err), path)
}

// ---- id next --------------------------------------------------------------

var idCounters = []string{"bugs", "features", "tasks", "milestones"}

func idNext(fs *flag.FlagSet) RunFunc {
	kind := fs.String("kind", "", "bugs|features|tasks|milestones")
	return func(c *Ctx, args []string) (Result, error) {
		if err := argCount(c, args, 0, 0, "id next takes no positional arguments"); err != nil {
			return Result{}, err
		}
		root, err := backlogScope(c)
		if err != nil {
			return Result{}, err
		}
		if !slices.Contains(idCounters, *kind) {
			return Result{}, Usage("--kind must be bugs|features|tasks|milestones")
		}
		ops, err := openBacklogFile(c, root, "IDs are issue numbers; capture creates the issue")
		if err != nil {
			return backlogFail(err)
		}
		id, err := ops.NextID(*kind)
		if err != nil {
			return backlogFail(err)
		}
		return Result{Data: jsonObj("kind", *kind, "id", id, "changed", true), Text: id}, nil
	}
}

// ---- item create ----------------------------------------------------------

func itemCreate(fs *flag.FlagSet) RunFunc {
	kind := fs.String("kind", "", "bugs|features|tasks")
	title := fs.String("title", "", "item title")
	tag := fs.String("tag", "", "P0..P3 (bugs) or Major|Minor|Cosmetic (features)")
	desc := fs.String("desc", "", "one-line description")
	bodyFile := fs.String("body-file", "", "detail file content, path or - for stdin")
	dependsOn := fs.String("depends-on", "", "items that must close first, comma or space separated (#N, or an ID like B07); written as a ## Depends on section")
	rawFile := fs.String("raw-file", "", "a preformatted bullet to append verbatim, path or - for stdin")
	named := map[string]*string{}
	for _, n := range []string{"related", "milestone", "repos", "subsystem", "captured"} {
		named[n] = fs.String(n, "", n+" field")
	}
	return func(c *Ctx, args []string) (Result, error) {
		if err := argCount(c, args, 0, 0, "item create takes no positional arguments"); err != nil {
			return Result{}, err
		}
		root, err := backlogScope(c)
		if err != nil {
			return Result{}, err
		}
		given := givenFlags(fs)
		if !backlog.ValidKind(*kind) {
			return Result{}, Usage("--kind must be bugs|features|tasks")
		}
		if given["raw-file"] {
			for n := range given {
				if n != "kind" && n != "raw-file" && !globalNames[n] {
					return Result{}, Usage("--raw-file takes only --kind")
				}
			}
			raw, err := readInputFile(c, *rawFile)
			if err != nil {
				return Result{}, readInputErr("raw-file", *rawFile, err)
			}
			ops, err := openBacklogFile(c, root, "--raw-file appends to BACKLOG.md; use item create --title")
			if err != nil {
				return backlogFail(err)
			}
			res, err := backlog.AppendRaw(ops, *kind, raw)
			if err != nil {
				return backlogFail(err)
			}
			return Result{Data: jsonObj("id", res.ID, "type", res.Type, "kind", *kind, "changed", true), Text: res.ID}, nil
		}
		req := backlog.CaptureRequest{Kind: *kind, Title: *title, Tag: *tag, Desc: *desc,
			Fields: map[string]string{}, Given: given, DependsOn: *dependsOn, DependsGiven: given["depends-on"]}
		for n, v := range named {
			req.Fields[n] = *v
		}
		if *bodyFile != "" {
			body, err := readInputFile(c, *bodyFile)
			if err != nil {
				return Result{}, readInputErr("body-file", *bodyFile, err)
			}
			req.Body, req.HasBody = body, true
		}
		in, err := backlog.BuildCreateInput(req)
		if err != nil {
			return backlogFail(err)
		}
		return withBackend(c, root, false, "", func(be backlog.Backend) (Result, error) {
			res, err := be.Create(in)
			if err != nil {
				return Result{}, err
			}
			data := jsonObj("id", res.ID, "type", res.Type, "kind", *kind)
			if res.Detail != "" {
				data.Set("detail", res.Detail)
			}
			data.Set("changed", true)
			return Result{Data: data, Text: res.ID}, nil
		})
	}
}

// ---- item field -----------------------------------------------------------

var (
	itemGetFields = []string{"title", "detail", "related", "milestone", "repos", "subsystem", "since", "reason", "note"}
	itemSetFields = backlog.SettableFields
)

func itemFieldValue(caps backlog.Capabilities, it *backlog.Item, name string) string {
	switch name {
	case "title":
		// The old helpers' title stops at the first "." (FindOrigin); issue
		// mode keeps the full title, as hv-todo-field did there.
		if !caps.IssueIDs {
			_, t, _ := backlog.FindOrigin("- "+it.Line, it.ID)
			return t
		}
		return it.Title
	case "reason":
		return it.Reason
	case "note":
		return it.Note
	}
	return it.Fields.Get(name)
}

// checkFieldName reads --name and checks it against allowed.
func checkFieldName(name string, allowed []string, what string) error {
	if name == "" {
		return Usage("--name is required")
	}
	if !slices.Contains(allowed, name) {
		return Usage("%s field %s; pick one of %s", what, name, strings.Join(allowed, ", "))
	}
	return nil
}

func itemFieldGet(fs *flag.FlagSet) RunFunc {
	name := fs.String("name", "", strings.Join(itemGetFields, "|"))
	return func(c *Ctx, args []string) (Result, error) {
		if err := argCount(c, args, 1, 1, "item field get takes one item ID"); err != nil {
			return Result{}, err
		}
		root, err := backlogScope(c)
		if err != nil {
			return Result{}, err
		}
		if err := checkFieldName(*name, itemGetFields, "unknown"); err != nil {
			return Result{}, err
		}
		be, err := openBacklog(c, root, false, "")
		if err != nil {
			return backlogFail(err)
		}
		it, err := be.Get(args[0])
		if err != nil {
			return backlogFail(err)
		}
		v := itemFieldValue(be.Capabilities(), it, *name)
		return Result{Data: jsonObj("id", it.ID, "type", it.Type, "field", *name, "value", v), Text: v}, nil
	}
}

func itemFieldList(fs *flag.FlagSet) RunFunc {
	return func(c *Ctx, args []string) (Result, error) {
		if err := argCount(c, args, 1, 1, "item field list takes one item ID"); err != nil {
			return Result{}, err
		}
		root, err := backlogScope(c)
		if err != nil {
			return Result{}, err
		}
		be, err := openBacklog(c, root, false, "")
		if err != nil {
			return backlogFail(err)
		}
		it, err := be.Get(args[0])
		if err != nil {
			return backlogFail(err)
		}
		fields := jsonx.NewObject()
		for _, n := range itemGetFields {
			fields.Set(n, itemFieldValue(be.Capabilities(), it, n))
		}
		text, _ := jsonx.MarshalCompact(fields)
		return Result{Data: jsonObj("id", it.ID, "type", it.Type, "fields", fields), Text: string(text)}, nil
	}
}

func itemFieldSet(fs *flag.FlagSet) RunFunc {
	name := fs.String("name", "", strings.Join(itemSetFields, "|"))
	value := fs.String("value", "", "new value; empty clears the field")
	return func(c *Ctx, args []string) (Result, error) {
		if err := argCount(c, args, 1, 1, "item field set takes one item ID"); err != nil {
			return Result{}, err
		}
		root, err := backlogScope(c)
		if err != nil {
			return Result{}, err
		}
		if err := checkFieldName(*name, itemSetFields, "unknown or read-only"); err != nil {
			return Result{}, err
		}
		if !givenFlags(fs)["value"] {
			return Result{}, Usage("--value is required (--value '' clears the field)")
		}
		return withBackend(c, root, *name == "detail", "--name detail points at a file; issues have a body instead", func(be backlog.Backend) (Result, error) {
			return setItemField(be, args[0], *name, *value)
		})
	}
}

// setItemField sets one field of the item behind ref.
func setItemField(be backlog.Backend, ref, name, value string) (Result, error) {
	return withItem(be, ref, func(id, typ string) (Result, error) {
		changed, err := be.SetField(ref, name, value)
		if err != nil {
			return Result{}, err
		}
		return Result{Data: jsonObj("id", id, "type", typ, "field", name, "value", value, "changed", changed),
			Text: fmt.Sprintf("%s %s: %s", id, name, value)}, nil
	})
}

// ---- item complete / reopen -----------------------------------------------

// completeItem closes the item behind ref. A done close without proof is a
// refusal whose hint names the proof verb.
func completeItem(be backlog.Backend, ref string, in backlog.CompleteInput) (Result, error) {
	return withItem(be, ref, func(id, typ string) (Result, error) {
		changed, err := be.Complete(ref, in)
		if err != nil {
			if errors.Is(err, backlog.ErrProofMissing) {
				res, e := backlogFail(err)
				e.(*Error).Hint = "record proof with `rota proof add`, or pass --no-proof"
				return res, e
			}
			return Result{}, err
		}
		return Result{Data: jsonObj("id", id, "type", typ, "reason", in.Reason, "commit", in.Commit, "changed", changed),
			Text: fmt.Sprintf("completed %s (%s) at %s", id, in.Reason, in.Commit)}, nil
	})
}

// reopenItem restores the completed item behind ref.
func reopenItem(be backlog.Backend, ref string) (Result, error) {
	return withItem(be, ref, func(id, typ string) (Result, error) {
		changed, err := be.Reopen(ref)
		if err != nil {
			return Result{}, err
		}
		text := "reopened " + id
		if !changed {
			text = id + " is already active"
		}
		return Result{Data: jsonObj("id", id, "type", typ, "changed", changed), Text: text}, nil
	})
}

func itemComplete(fs *flag.FlagSet) RunFunc {
	commit := fs.String("commit", "", "commit hash for the Done line; default HEAD")
	reason := fs.String("reason", "done", "done|handed-off|blocked|dropped")
	note := fs.String("note", "", "closure note")
	noProof := fs.Bool("no-proof", false, "skip the proof gate")
	return func(c *Ctx, args []string) (Result, error) {
		if err := argCount(c, args, 1, 1, "item complete takes one item ID"); err != nil {
			return Result{}, err
		}
		root, err := backlogScope(c)
		if err != nil {
			return Result{}, err
		}
		if !slices.Contains(backlog.ClosureReasons, *reason) {
			return Result{}, Usage("--reason must be done|handed-off|blocked|dropped")
		}
		hash := *commit
		if hash == "" {
			h, err := shortHead(c)
			if err != nil {
				return Result{}, err
			}
			hash = h
		}
		return withBackend(c, root, false, "", func(be backlog.Backend) (Result, error) {
			return completeItem(be, args[0], backlog.CompleteInput{
				Commit: hash, Date: todayDate(c), Reason: *reason,
				Note: strings.ReplaceAll(*note, "\n", " "), NoProof: *noProof,
			})
		})
	}
}

// shortHead is HEAD's short hash through the Deps git runner, the default of
// --commit.
func shortHead(c *Ctx) (string, error) {
	res, err := c.deps().Git(c.Context(), "", "rev-parse", "--short", "HEAD")
	h := strings.TrimRight(res.Stdout, "\n")
	if err != nil || res.ExitCode != 0 || h == "" {
		return "", Unavailable("git has no HEAD to default --commit").WithHint("pass --commit <hash>")
	}
	return h, nil
}

func itemReopen(fs *flag.FlagSet) RunFunc {
	return func(c *Ctx, args []string) (Result, error) {
		if err := argCount(c, args, 1, 1, "item reopen takes one item ID"); err != nil {
			return Result{}, err
		}
		root, err := backlogScope(c)
		if err != nil {
			return Result{}, err
		}
		return withBackend(c, root, false, "", func(be backlog.Backend) (Result, error) {
			res, err := reopenItem(be, args[0])
			if err == nil {
				// A reopened item starts a fresh wall-clock (work.itemTimeoutMinutes).
				if id := strings.ToUpper(strings.TrimPrefix(args[0], "#")); id != "" {
					_ = worker.ClearItemStart(root, id)
				}
			}
			return res, err
		})
	}
}

// ---- item rm --------------------------------------------------------------

func itemRm(fs *flag.FlagSet) RunFunc {
	scrub := fs.Bool("scrub-archive", false, "also remove the entries from ARCHIVE.md")
	apply := fs.Bool("apply", false, "remove for real; without it only a plan is shown")
	return func(c *Ctx, args []string) (Result, error) {
		var ids []string
		seen := map[string]bool{}
		for _, a := range args {
			for _, p := range strings.Split(a, ",") {
				if p = pystr.Strip(p); p != "" && !seen[p] {
					seen[p] = true
					ids = append(ids, p)
				}
			}
		}
		if len(ids) == 0 {
			return Result{}, Usage("item rm needs at least one item ID")
		}
		root, err := backlogScope(c)
		if err != nil {
			return Result{}, err
		}
		ops, err := openBacklogFile(c, root, "rota item complete <ID> --reason dropped")
		if err != nil {
			return backlogFail(err)
		}
		res, err := ops.Remove(ids, *scrub, *apply)
		if err != nil {
			return backlogFail(err)
		}
		items := []any{}
		var lines []string
		for _, it := range res.Items {
			o := jsonObj("id", it.ID, "type", it.Type, "todoEntry", it.TodoEntry, "crossRefs", it.CrossRefs)
			if it.DetailFile != "" {
				o.Set("detailFile", it.DetailFile)
			}
			o.Set("planFiles", it.PlanFiles)
			o.Set("archive", it.Archive)
			if it.ActiveBranch != "" {
				o.Set("activeBranch", it.ActiveBranch)
			}
			items = append(items, o)
			lines = append(lines, itemRmLine(it, res.Applied))
		}
		if !res.Applied {
			c.Warn("preview only; pass --apply")
		}
		return Result{Data: jsonObj("applied", res.Applied, "items", items, "changed", res.Applied), Text: strings.Join(lines, "\n")}, nil
	}
}

func itemRmLine(it backlog.RmItem, applied bool) string {
	var parts []string
	if it.TodoEntry {
		parts = append(parts, "TODO entry")
	}
	if it.CrossRefs > 0 {
		parts = append(parts, fmt.Sprintf("%d cross-reference(s)", it.CrossRefs))
	}
	if it.DetailFile != "" {
		parts = append(parts, "detail file")
	}
	if n := len(it.PlanFiles); n > 0 {
		parts = append(parts, fmt.Sprintf("%d plan file(s)", n))
	}
	if it.Archive && it.Scrub {
		parts = append(parts, "ARCHIVE entry")
	}
	verb := "would remove"
	if applied {
		verb = "removed"
	}
	s := fmt.Sprintf("%s: %s %s", it.ID, verb, strings.Join(parts, ", "))
	if it.ActiveBranch != "" {
		s += " (active on " + it.ActiveBranch + ")"
	}
	return s
}

// ---- item shipped ---------------------------------------------------------

func itemShipped(fs *flag.FlagSet) RunFunc {
	return func(c *Ctx, args []string) (Result, error) {
		var titles []string
		for _, a := range args {
			if pystr.Strip(a) != "" {
				titles = append(titles, a)
			}
		}
		if len(titles) == 0 {
			return Result{}, Usage("item shipped needs at least one title")
		}
		if _, err := backlogScope(c); err != nil {
			return Result{}, err
		}
		dir, err := os.Getwd()
		if err != nil {
			return Result{}, err
		}
		rep := backlog.Shipped(dir, titles)
		out := []any{}
		for _, a := range rep.Audits {
			hits := []any{}
			for _, h := range a.Hits {
				o := jsonObj("level", h.Level)
				switch h.Level {
				case backlog.HitPath:
					o.Set("token", h.Token)
					o.Set("path", h.Path)
				default:
					o.Set("hash", h.Hash)
					o.Set("subject", h.Subject)
					o.Set("tokens", h.Tokens)
				}
				hits = append(hits, o)
			}
			out = append(out, jsonObj("title", a.Title, "hits", hits))
		}
		res := Result{Data: jsonObj("found", rep.Found, "titles", out), Text: rep.Text}
		if !rep.Found {
			res.Text = "no ship evidence found"
			return res, Failed("no ship evidence found")
		}
		return res, nil
	}
}

// ---- item ready -----------------------------------------------------------

func itemReady(fs *flag.FlagSet) RunFunc {
	return func(c *Ctx, args []string) (Result, error) {
		if err := argCount(c, args, 1, 1, "item ready takes one item ID"); err != nil {
			return Result{}, err
		}
		root, err := backlogScope(c)
		if err != nil {
			return Result{}, err
		}
		return withBackend(c, root, false, "", func(be backlog.Backend) (Result, error) {
			return withItem(be, args[0], func(id, typ string) (Result, error) {
				reasons, err := be.Ready(args[0])
				if err != nil {
					return Result{}, err
				}
				if reasons == nil {
					reasons = []string{}
				}
				res := Result{Data: jsonObj("id", id, "type", typ, "ready", len(reasons) == 0, "reasons", reasons)}
				if len(reasons) == 0 {
					res.Text = id + " is ready"
					return res, nil
				}
				res.Text = strings.Join(reasons, "\n")
				return res, Failed("%s is not ready: %s", id, strings.Join(reasons, "; "))
			})
		})
	}
}

// ---- item comment ---------------------------------------------------------

func itemCommentAdd(fs *flag.FlagSet) RunFunc {
	kind := fs.String("kind", "", strings.Join(backlog.CommentKinds, "|"))
	bodyFile := fs.String("body-file", "", "comment text, path or - for stdin")
	return func(c *Ctx, args []string) (Result, error) {
		if err := argCount(c, args, 1, 1, "item comment add takes one item ID"); err != nil {
			return Result{}, err
		}
		root, err := backlogScope(c)
		if err != nil {
			return Result{}, err
		}
		if !slices.Contains(backlog.CommentKinds, *kind) {
			return Result{}, Usage("--kind must be %s", strings.Join(backlog.CommentKinds, "|"))
		}
		if *bodyFile == "" {
			return Result{}, Usage("--body-file is required")
		}
		raw, err := readInputFile(c, *bodyFile)
		if err != nil {
			return Result{}, readInputErr("body-file", *bodyFile, err)
		}
		text := decodeText(raw)
		if pystr.Strip(text) == "" {
			return Result{}, Usage("empty comment body")
		}
		return withBackend(c, root, false, "", func(be backlog.Backend) (Result, error) {
			return withItem(be, args[0], func(id, typ string) (Result, error) {
				cid, err := be.AddComment(args[0], *kind, text)
				if err != nil {
					return Result{}, err
				}
				data := jsonObj("id", id, "type", typ, "kind", *kind)
				if cid != "" {
					data.Set("commentId", cid)
				}
				data.Set("changed", true)
				return Result{Data: data, Text: fmt.Sprintf("commented on %s (%s)", id, *kind)}, nil
			})
		})
	}
}

// decodeText is bytes.decode(errors="replace"): every invalid byte becomes U+FFFD.
func decodeText(b []byte) string { return string([]rune(string(b))) }

func itemCommentList(fs *flag.FlagSet) RunFunc {
	kind := fs.String("kind", "", strings.Join(backlog.CommentKinds, "|")+"; default all")
	return func(c *Ctx, args []string) (Result, error) {
		if err := argCount(c, args, 1, 1, "item comment list takes one item ID"); err != nil {
			return Result{}, err
		}
		root, err := backlogScope(c)
		if err != nil {
			return Result{}, err
		}
		if *kind != "" && !slices.Contains(backlog.CommentKinds, *kind) {
			return Result{}, Usage("--kind must be %s", strings.Join(backlog.CommentKinds, "|"))
		}
		return withBackend(c, root, false, "", func(be backlog.Backend) (Result, error) {
			return withItem(be, args[0], func(id, typ string) (Result, error) {
				rows, err := be.Comments(args[0], *kind)
				if err != nil {
					return Result{}, err
				}
				list := []any{}
				var lines []string
				for _, r := range rows {
					list = append(list, jsonObj("who", r.Who, "kind", r.Kind, "text", r.Text))
					first, rest, _ := strings.Cut(r.Text, "\n")
					who := r.Who
					if who == "" {
						who = "?"
					}
					lines = append(lines, fmt.Sprintf("- %s · %s · %s", who, r.Kind, first))
					if rest != "" || strings.Contains(r.Text, "\n") {
						for _, l := range strings.Split(rest, "\n") {
							if pystr.Strip(l) != "" {
								lines = append(lines, "  "+l)
							} else {
								lines = append(lines, "")
							}
						}
					}
				}
				return Result{Data: jsonObj("id", id, "type", typ, "comments", list), Text: strings.Join(lines, "\n")}, nil
			})
		})
	}
}
