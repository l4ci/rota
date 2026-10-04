package cli

import (
	"flag"
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/pystr"
)

// The issue-mode workflow verbs: `rota item show|claim|release|state` and
// `rota item note add|show|rm`. The file backend answers claim, release and state
// with a successful no-op (changed false; status.json is its lock) and refuses
// show and the notes as issue-only (exit 4, backend).

// a4Flow opens the backlog and resolves ref to its canonical ID and type.
func a4Flow(c *Ctx, ref string) (backlog.Backend, backlog.Workflow, string, string, error) {
	root, err := a4Scope(c)
	if err != nil {
		return nil, nil, "", "", err
	}
	be, err := a4Open(c, root, false, "")
	if err != nil {
		return nil, nil, "", "", err
	}
	id, typ, err := a4Item(be, ref)
	if err != nil {
		return nil, nil, "", "", err
	}
	return be, be.(backlog.Workflow), id, typ, nil
}

// ---- item show ------------------------------------------------------------

func a4Show(fs *flag.FlagSet) RunFunc {
	return func(c *Ctx, args []string) (Result, error) {
		if err := a4Args(c, args, 1, 1, "item show takes one item ID"); err != nil {
			return Result{}, err
		}
		_, wf, _, _, err := a4Flow(c, args[0])
		if err != nil {
			return a4FailRead(err)
		}
		st, err := wf.Status(args[0])
		if err != nil {
			return a4FailRead(err)
		}
		comments := []any{}
		var rows []string
		for _, r := range st.Comments {
			comments = append(comments, a4Obj("who", r.Who, "kind", r.Kind, "text", r.Text))
			rows = append(rows, a4CommentRow(r)...)
		}
		assignees := []any{}
		for _, a := range st.Assignees {
			assignees = append(assignees, a)
		}
		notes := []any{}
		for _, n := range st.Notes {
			notes = append(notes, n)
		}
		data := a4Obj("id", st.ID, "type", st.Type, "title", st.Title, "status", st.Status,
			"state", a4Null(st.State), "claimedBy", a4Null(st.Claim), "assignees", assignees,
			"milestone", a4Null(st.Milestone), "notes", notes, "comments", comments)
		word := map[string]string{"B": "bug", "F": "feature", "T": "task"}[st.Type]
		lines := []string{
			fmt.Sprintf("[%s] %s", a4Spell(st.Type, st.ID), st.Title),
			"type: " + word,
			"status: " + st.Status,
			"state: " + a4None(st.State),
			"claimed by: " + a4None(st.Claim),
			"assignee: " + a4None(strings.Join(st.Assignees, ", ")),
			"milestone: " + a4None(st.Milestone),
			"notes: " + a4None(strings.Join(st.Notes, ", ")),
			fmt.Sprintf("comments: %d", len(st.Comments)),
		}
		return Result{Data: data, Text: strings.Join(append(lines, rows...), "\n")}, nil
	}
}

// a4Spell is the bullet spelling of an item: "F12", and "repo:F12" for the
// umbrella ID "repo:12".
func a4Spell(typ, id string) string {
	if repo, n, ok := strings.Cut(id, ":"); ok {
		return repo + ":" + typ + n
	}
	return typ + id
}

// a4Null is s, or JSON null when it is empty.
func a4Null(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func a4None(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// a4CommentRow is `- <who> · <kind> · <first line>` with the continuation
// lines indented (format_comment_rows).
func a4CommentRow(r backlog.Comment) []string {
	first, rest, multi := strings.Cut(r.Text, "\n")
	who := r.Who
	if who == "" {
		who = "?"
	}
	out := []string{fmt.Sprintf("- %s · %s · %s", who, r.Kind, first)}
	if multi {
		for _, l := range strings.Split(rest, "\n") {
			if pystr.Strip(l) != "" {
				out = append(out, "  "+l)
			} else {
				out = append(out, "")
			}
		}
	}
	return out
}

// ---- item claim / release / state -----------------------------------------

// a4ClaimID checks --as: non-empty, no whitespace, no "-->".
func a4ClaimID(as string) error {
	if as == "" || strings.Contains(as, "-->") || strings.IndexFunc(as, pystr.IsSpace) >= 0 {
		return Usage("--as must be a claim ID: non-empty, no whitespace, no -->")
	}
	return nil
}

func a4Claim(fs *flag.FlagSet) RunFunc {
	as := fs.String("as", "", "claim ID")
	return func(c *Ctx, args []string) (Result, error) {
		if err := a4Args(c, args, 1, 1, "item claim takes one item ID"); err != nil {
			return Result{}, err
		}
		if err := a4ClaimID(*as); err != nil {
			return Result{}, err
		}
		be, wf, id, typ, err := a4Flow(c, args[0])
		if err != nil {
			return a4Fail(err)
		}
		won, holder, err := wf.Claim(args[0], *as)
		if err != nil {
			return a4Fail(err)
		}
		if !won {
			// The loser posted its claim and then its release on the issue, so
			// it did change state on the way to the refusal (contract: changed true).
			return Result{Data: a4Obj("blockedBy", "claimed", "changed", true)},
				Refused("%s is claimed by %s", id, holder)
		}
		issue := be.Name() == "issues"
		res := Result{Data: a4Obj("id", id, "type", typ, "claimId", *as, "changed", issue)}
		if issue {
			res.Text = fmt.Sprintf("claimed %s as %s", id, *as)
		}
		return res, nil
	}
}

func a4Release(fs *flag.FlagSet) RunFunc {
	as := fs.String("as", "", "claim ID")
	return func(c *Ctx, args []string) (Result, error) {
		if err := a4Args(c, args, 1, 1, "item release takes one item ID"); err != nil {
			return Result{}, err
		}
		if err := a4ClaimID(*as); err != nil {
			return Result{}, err
		}
		_, wf, id, typ, err := a4Flow(c, args[0])
		if err != nil {
			return a4Fail(err)
		}
		changed, err := wf.Release(args[0], *as)
		if err != nil {
			return a4Fail(err)
		}
		res := Result{Data: a4Obj("id", id, "type", typ, "claimId", *as, "changed", changed)}
		if changed {
			res.Text = fmt.Sprintf("released %s as %s", id, *as)
		}
		return res, nil
	}
}

func a4State(fs *flag.FlagSet) RunFunc {
	to := fs.String("to", "", strings.Join(backlog.States, "|"))
	return func(c *Ctx, args []string) (Result, error) {
		if err := a4Args(c, args, 1, 1, "item state takes one item ID"); err != nil {
			return Result{}, err
		}
		if !a4In(backlog.States, *to) {
			return Result{}, Usage("--to must be %s", strings.Join(backlog.States, "|"))
		}
		_, wf, id, typ, err := a4Flow(c, args[0])
		if err != nil {
			return a4Fail(err)
		}
		changed, err := wf.SetState(args[0], *to)
		if err != nil {
			return a4Fail(err)
		}
		state := a4Null(*to)
		if *to == "none" {
			state = nil
		}
		return Result{Data: a4Obj("id", id, "type", typ, "state", state, "changed", changed),
			Text: fmt.Sprintf("%s state: %s", id, *to)}, nil
	}
}

// ---- item note ------------------------------------------------------------

func a4NoteKind(kind string) error {
	if !a4In(backlog.NoteKinds, kind) {
		return Usage("--kind must be %s", strings.Join(backlog.NoteKinds, "|"))
	}
	return nil
}

func a4NoteAdd(fs *flag.FlagSet) RunFunc {
	kind := fs.String("kind", "", strings.Join(backlog.NoteKinds, "|"))
	bodyFile := fs.String("body-file", "", "note text, path or - for stdin")
	return func(c *Ctx, args []string) (Result, error) {
		if err := a4Args(c, args, 1, 1, "item note add takes one item ID"); err != nil {
			return Result{}, err
		}
		if err := a4NoteKind(*kind); err != nil {
			return Result{}, err
		}
		if *bodyFile == "" {
			return Result{}, Usage("--body-file is required")
		}
		raw, err := a4ReadInput(c, *bodyFile)
		if err != nil {
			return Result{}, a4ReadErr("body-file", *bodyFile, err)
		}
		text := a4Decode(raw)
		if pystr.Strip(text) == "" {
			return Result{}, Usage("empty note body")
		}
		_, wf, id, typ, err := a4Flow(c, args[0])
		if err != nil {
			return a4Fail(err)
		}
		changed, err := wf.NotePut(args[0], *kind, text)
		if err != nil {
			return a4Fail(err)
		}
		return Result{Data: a4Obj("id", id, "type", typ, "kind", *kind, "changed", changed),
			Text: fmt.Sprintf("wrote %s note on %s", *kind, id)}, nil
	}
}

func a4NoteShow(fs *flag.FlagSet) RunFunc {
	kind := fs.String("kind", "", strings.Join(backlog.NoteKinds, "|"))
	return func(c *Ctx, args []string) (Result, error) {
		if err := a4Args(c, args, 1, 1, "item note show takes one item ID"); err != nil {
			return Result{}, err
		}
		if err := a4NoteKind(*kind); err != nil {
			return Result{}, err
		}
		_, wf, id, typ, err := a4Flow(c, args[0])
		if err != nil {
			return a4FailRead(err)
		}
		body, ok, err := wf.NoteGet(args[0], *kind)
		if err != nil {
			return a4FailRead(err)
		}
		return Result{Data: a4Obj("id", id, "type", typ, "kind", *kind, "exists", ok, "body", body), Text: body}, nil
	}
}

func a4NoteRm(fs *flag.FlagSet) RunFunc {
	kind := fs.String("kind", "", strings.Join(backlog.NoteKinds, "|"))
	return func(c *Ctx, args []string) (Result, error) {
		if err := a4Args(c, args, 1, 1, "item note rm takes one item ID"); err != nil {
			return Result{}, err
		}
		if err := a4NoteKind(*kind); err != nil {
			return Result{}, err
		}
		_, wf, id, typ, err := a4Flow(c, args[0])
		if err != nil {
			return a4Fail(err)
		}
		changed, err := wf.NoteRm(args[0], *kind)
		if err != nil {
			return a4Fail(err)
		}
		return Result{Data: a4Obj("id", id, "type", typ, "kind", *kind, "changed", changed),
			Text: fmt.Sprintf("removed %s note on %s", *kind, id)}, nil
	}
}
