package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"slices"
	"strings"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/exitmap"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/marker"
	"github.com/l4ci/rota/internal/pystr"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/roundcfg"
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
			{Name: "section", Summary: "edit one body section of an item", Subs: []*Command{
				{Name: "set", Summary: "replace or add one ## section of an issue-mode body", Repo: true, Verb: itemSectionSet},
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
	batchFile := fs.String("batch-file", "", "a JSON array of records (kind, title, tag, desc, body, dependsOn, related, milestone, repos, subsystem, captured), filed in order; path or - for stdin")
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
		if given["batch-file"] {
			for n := range given {
				if n != "batch-file" && !globalNames[n] {
					return Result{}, Usage("--batch-file takes no other flag; each record carries its own kind and fields")
				}
			}
			return itemCreateBatch(c, root, *batchFile)
		}
		if !backlog.ValidKind(*kind) {
			return Result{}, Usage("--kind must be bugs|features|tasks")
		}
		if given["raw-file"] {
			for n := range given {
				if n != "kind" && n != "raw-file" && !globalNames[n] {
					return Result{}, Usage("--raw-file takes only --kind")
				}
			}
			raw, err := readBodyInput(c, "raw-file", *rawFile, ExitResolution)
			if err != nil {
				return Result{}, err
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
			body, err := readBodyInput(c, "body-file", *bodyFile, ExitResolution)
			if err != nil {
				return Result{}, err
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

// batchRecord is one item of `item create --batch-file`: the flags create
// takes, as one record, so a title and its body travel together (#838).
type batchRecord struct {
	Kind      string `json:"kind"`
	Title     string `json:"title"`
	Tag       string `json:"tag"`
	Desc      string `json:"desc"`
	Body      string `json:"body"`
	DependsOn string `json:"dependsOn"`
	Related   string `json:"related"`
	Milestone string `json:"milestone"`
	Repos     string `json:"repos"`
	Subsystem string `json:"subsystem"`
	Captured  string `json:"captured"`
}

// itemCreateBatch validates every record before it files any, then files them
// in order and reports one id per record. A record that fails to file stops the
// batch; the ids already filed ride in the error so none is lost.
func itemCreateBatch(c *Ctx, root, path string) (Result, error) {
	raw, err := readBodyInput(c, "batch-file", path, ExitResolution)
	if err != nil {
		return Result{}, err
	}
	var recs []batchRecord
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&recs); err != nil {
		return Result{}, Usage("--batch-file is not a JSON array of records: %v", err)
	}
	if len(recs) == 0 {
		return Result{}, Usage("--batch-file holds no records")
	}
	ins := make([]backlog.CreateInput, len(recs))
	for i, r := range recs {
		if err := backlog.CheckTitleWords(r.Title); err != nil {
			return backlogFail(fmt.Errorf("record %d: %w", i+1, err))
		}
		if !backlog.ValidKind(r.Kind) {
			return Result{}, Usage("record %d: kind must be bugs|features|tasks", i+1)
		}
		fields := map[string]string{"related": r.Related, "milestone": r.Milestone, "repos": r.Repos,
			"subsystem": r.Subsystem, "captured": r.Captured}
		given := map[string]bool{}
		for n, v := range fields {
			given[n] = v != ""
		}
		in, err := backlog.BuildCreateInput(backlog.CaptureRequest{Kind: r.Kind, Title: r.Title, Tag: r.Tag, Desc: r.Desc,
			Fields: fields, Given: given, Body: []byte(r.Body), HasBody: r.Body != "",
			DependsOn: r.DependsOn, DependsGiven: r.DependsOn != ""})
		if err != nil {
			return backlogFail(fmt.Errorf("record %d: %w", i+1, err))
		}
		ins[i] = in
	}
	return withBackend(c, root, false, "", func(be backlog.Backend) (Result, error) { return fileBatch(be, ins) })
}

// fileBatch files ins in order. A failure at record N keeps the first N-1 ids:
// they ride in the data (`items` filed so far, `failedAt`) and in the message,
// since the tracker error's own text names none of them.
func fileBatch(be backlog.Backend, ins []backlog.CreateInput) (Result, error) {
	var out []any
	var ids []string
	for i, in := range ins {
		res, err := be.Create(in)
		if err != nil {
			filed := "nothing filed yet"
			if len(ids) > 0 {
				filed = "filed " + strings.Join(ids, " ")
			}
			r, ferr := backlogFail(fmt.Errorf("record %d (%q) failed: %w", i+1, in.Title, err))
			var xe *Error
			if errors.As(ferr, &xe) {
				xe.Message = fmt.Sprintf("record %d (%q) failed, %s: %s", i+1, in.Title, filed, xe.Message)
			} else {
				// Unclassified (file I/O, a lock): classify as a single verb would, so a
				// lock timeout stays retry and an unreadable state file stays unavailable.
				xe = asError(err)
				xe.Cause = err
				xe.Message = fmt.Sprintf("record %d (%q) failed, %s: %s", i+1, in.Title, filed, err)
				ferr = xe
			}
			data, _ := r.Data.(*jsonx.Object)
			if data == nil {
				data = jsonx.NewObject()
			}
			data.Set("items", out)
			data.Set("failedAt", i+1)
			data.Set("changed", len(out) > 0)
			r.Data = data
			return r, ferr
		}
		ids = append(ids, res.ID)
		out = append(out, jsonObj("id", res.ID, "type", res.Type, "kind", in.Kind, "title", in.Title))
	}
	return Result{Data: jsonObj("items", out, "changed", true), Text: strings.Join(ids, "\n")}, nil
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

// ---- item section set ------------------------------------------------------

func itemSectionSet(fs *flag.FlagSet) RunFunc {
	name := fs.String("name", "", "section heading, e.g. acceptance")
	bodyFile := fs.String("body-file", "", "section text, path or - for stdin")
	return func(c *Ctx, args []string) (Result, error) {
		if err := argCount(c, args, 1, 1, "item section set takes one item ID"); err != nil {
			return Result{}, err
		}
		if strings.TrimSpace(*name) == "" || strings.ContainsAny(*name, "\r\n") || strings.HasPrefix(strings.TrimSpace(*name), "#") {
			return Result{}, Usage("--name is required: one section heading, without a leading #")
		}
		if *bodyFile == "" {
			return Result{}, Usage("--body-file is required")
		}
		root, err := backlogScope(c)
		if err != nil {
			return Result{}, err
		}
		raw, err := readBodyInput(c, "body-file", *bodyFile, ExitResolution)
		if err != nil {
			return Result{}, err
		}
		text := decodeText(raw)
		if pystr.Strip(text) == "" {
			return Result{}, Usage("empty section body")
		}
		return withBackend(c, root, false, "", func(be backlog.Backend) (Result, error) {
			ed, ok := be.(backlog.SectionEditor)
			if !ok {
				return Result{}, &backlog.RefusedError{BlockedBy: "backend", Err: backlog.ErrWrongBackend,
					Hint: "in file mode edit the item's detail file under .rota/ directly",
					Msg:  "item section set is not available when issues.source is file"}
			}
			return withItem(be, args[0], func(id, typ string) (Result, error) {
				changed, err := ed.SetSection(args[0], *name, text)
				if err != nil {
					return Result{}, err
				}
				return Result{Data: jsonObj("id", id, "type", typ, "section", *name, "changed", changed),
					Text: fmt.Sprintf("%s section %s", id, *name)}, nil
			})
		})
	}
}

// ---- item complete / reopen -----------------------------------------------

// completeItem closes the item behind ref. A done close without proof is a
// refusal whose hint names the proof verb.
func completeItem(be backlog.Backend, ref string, in backlog.CompleteInput, gist string) (Result, error) {
	return withItem(be, ref, func(id, typ string) (Result, error) {
		gist = strings.Join(strings.Fields(gist), " ")
		it, err := checkGist(be, ref, id, gist)
		if err != nil {
			return Result{}, err
		}
		changed, err := be.Complete(ref, in)
		if err != nil {
			if errors.Is(err, backlog.ErrProofMissing) {
				res, e := backlogFail(err)
				e.(*Error).Hint = "record proof with `rota proof add`, or pass --no-proof"
				return res, e
			}
			return Result{}, err
		}
		data := jsonObj("id", id, "type", typ, "reason", in.Reason, "commit", in.Commit, "changed", changed)
		text := fmt.Sprintf("completed %s (%s) at %s", id, in.Reason, in.Commit)
		if gist != "" {
			mid, err := backlog.LogDecision(be, it, backlog.DecisionRef(id), gist)
			if err != nil {
				return Result{}, err
			}
			data.Set("decisionLog", mid)
			text += "; no milestone to log the decision in"
			if mid != "" {
				text = fmt.Sprintf("completed %s (%s) at %s; logged the decision in %s", id, in.Reason, in.Commit, mid)
			}
		}
		return Result{Data: data, Text: text}, nil
	})
}

// checkGist reads the item and enforces the decision-log contract of `item complete --gist`: a
// decision item closes with a gist, any other item takes none. It runs before
// the close, so a refusal leaves the item open.
func checkGist(be backlog.Backend, ref, id, gist string) (*backlog.Item, error) {
	it, err := be.Get(ref)
	if err != nil {
		if gist == "" {
			// Nothing to check: Complete reports an unknown item in its own words.
			return nil, nil
		}
		return nil, err
	}
	dec, err := round.DecisionOf(be.Capabilities(), *it)
	switch {
	case err != nil:
		return nil, Failed("%s", err.Error())
	case dec != "" && gist == "":
		return nil, Failed("%s is a decision item: pass --gist <one line> for the milestone's decision log", id)
	case dec == "" && gist != "":
		return nil, Failed("--gist is only for decision items; %s is not a decision item", id)
	}
	return it, nil
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
	gist := fs.String("gist", "", "decision items: the one-line gist for the milestone's decision log")
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
			}, *gist)
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
				// A reopened item starts a fresh wall-clock (round.itemTimeoutMinutes).
				if id := worker.NormID(args[0]); id != "" {
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
		dir, err := c.deps().Getwd()
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
	relayRound := fs.Int("round", -1, "relay only: the round to carry (default the round state's)")
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
		// harvested is the mark knowledge harvest --mark writes, which a support
		// machine may not post.
		if *kind == "harvested" {
			if res, err := supportRefusal(c, root, "rota item comment add --kind harvested"); err != nil {
				return res, err
			}
		}
		if *bodyFile == "" {
			return Result{}, Usage("--body-file is required")
		}
		raw, err := readBodyInput(c, "body-file", *bodyFile, ExitResolution)
		if err != nil {
			return Result{}, err
		}
		text := decodeText(raw)
		if pystr.Strip(text) == "" {
			return Result{}, Usage("empty comment body")
		}
		if *relayRound >= 0 && *kind != "relay" {
			return Result{}, Usage("--round only goes with --kind relay")
		}
		if *kind == "relay" {
			// A relay is the orchestrator's word: a worker (ROTA_SLOT set) never posts one.
			if c.deps().Getenv("ROTA_SLOT") != "" {
				return Result{}, Refused("--kind relay is the orchestrator's: a worker slot (ROTA_SLOT set) cannot post one")
			}
			n := *relayRound
			if n < 0 {
				n = worker.RoundOf(root)
			}
			text = marker.RelayLine(n) + "\n" + text
			// The stamp goes last so the relay marker stays the first line and
			// the summary the gate reads stays the relayed text (#1869).
			if cfg := c.deps().Config(root); roundcfg.SupportName(cfg) != "" {
				role, _ := roundcfg.MachineRole(cfg)
				text = strings.TrimRight(text, "\n") + "\n" + marker.FromLine(role)
			}
		} else if *kind != "harvested" {
			// With the support split active the other machine's watch has to tell
			// whose comment this is (#1827).
			cfg := c.deps().Config(root)
			if roundcfg.SupportName(cfg) != "" {
				role, _ := roundcfg.MachineRole(cfg)
				text = marker.FromLine(role) + "\n" + text
			}
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
