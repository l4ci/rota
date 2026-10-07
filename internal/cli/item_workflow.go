package cli

import (
	"flag"
	"fmt"
	"slices"
	"strings"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/pystr"
)

// The issue-mode workflow verbs: `rota item show|claim|release|state` and
// `rota item note add|show|rm`. The file backend answers claim, release and state
// with a successful no-op (changed false; status.json is its lock) and refuses
// show and the notes as issue-only (exit 4, backend).

// itemFlow opens the backlog and resolves ref to its canonical ID and type.
func itemFlow(c *Ctx, ref string) (backlog.Backend, backlog.Workflow, string, string, error) {
	root, err := backlogScope(c)
	if err != nil {
		return nil, nil, "", "", err
	}
	be, err := openBacklog(c, root, false, "")
	if err != nil {
		return nil, nil, "", "", err
	}
	id, typ, err := resolveItem(be, ref)
	if err != nil {
		return nil, nil, "", "", err
	}
	wf, err := backlog.WorkflowOf(be)
	if err != nil {
		return nil, nil, "", "", err
	}
	return be, wf, id, typ, nil
}

// ---- item show ------------------------------------------------------------

func itemShow(fs *flag.FlagSet) RunFunc {
	return func(c *Ctx, args []string) (Result, error) {
		if err := argCount(c, args, 1, 1, "item show takes one item ID"); err != nil {
			return Result{}, err
		}
		_, wf, _, _, err := itemFlow(c, args[0])
		if err != nil {
			return backlogFailRead(err)
		}
		st, err := wf.Status(args[0])
		if err != nil {
			return backlogFailRead(err)
		}
		comments := []any{}
		var rows []string
		for _, r := range st.Comments {
			comments = append(comments, jsonObj("who", r.Who, "kind", r.Kind, "text", r.Text))
			rows = append(rows, commentRow(r)...)
		}
		assignees := []any{}
		for _, a := range st.Assignees {
			assignees = append(assignees, a)
		}
		notes := []any{}
		for _, n := range st.Notes {
			notes = append(notes, n)
		}
		cover, err := acceptanceCoverage(c, wf, args[0], st)
		if err != nil {
			return backlogFailRead(err)
		}
		data := jsonObj("id", st.ID, "type", st.Type, "title", st.Title, "status", st.Status,
			"state", nullIfEmpty(st.State), "claimedBy", nullIfEmpty(st.Claim), "assignees", assignees,
			"milestone", nullIfEmpty(st.Milestone), "notes", notes, "acceptance", acceptanceData(cover), "comments", comments)
		word := map[string]string{"B": "bug", "F": "feature", "T": "task"}[st.Type]
		lines := []string{
			fmt.Sprintf("[%s] %s", spellID(st.Type, st.ID), st.Title),
			"type: " + word,
			"status: " + st.Status,
			"state: " + noneIfEmpty(st.State),
			"claimed by: " + noneIfEmpty(st.Claim),
			"assignee: " + noneIfEmpty(strings.Join(st.Assignees, ", ")),
			"milestone: " + noneIfEmpty(st.Milestone),
			"notes: " + noneIfEmpty(strings.Join(st.Notes, ", ")),
			fmt.Sprintf("comments: %d", len(st.Comments)),
		}
		lines = append(lines, acceptanceLines(cover)...)
		return Result{Data: data, Text: strings.Join(append(lines, rows...), "\n")}, nil
	}
}

// spellID is the bullet spelling of an item: "F12", and "repo:F12" for the
// umbrella ID "repo:12".
func spellID(typ, id string) string {
	if repo, n, ok := strings.Cut(id, ":"); ok {
		return repo + ":" + typ + n
	}
	return typ + id
}

// nullIfEmpty is s, or JSON null when it is empty.
func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func noneIfEmpty(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// commentRow is `- <who> · <kind> · <first line>` with the continuation
// lines indented (format_comment_rows).
func commentRow(r backlog.Comment) []string {
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

// checkClaimID checks --as: non-empty, no whitespace, no "-->".
func checkClaimID(as string) error {
	if as == "" || strings.Contains(as, "-->") || strings.IndexFunc(as, pystr.IsSpace) >= 0 {
		return Usage("--as must be a claim ID: non-empty, no whitespace, no -->")
	}
	return nil
}

func itemClaim(fs *flag.FlagSet) RunFunc {
	as := fs.String("as", "", "claim ID")
	return func(c *Ctx, args []string) (Result, error) {
		if err := argCount(c, args, 1, 1, "item claim takes one item ID"); err != nil {
			return Result{}, err
		}
		if err := checkClaimID(*as); err != nil {
			return Result{}, err
		}
		be, wf, id, typ, err := itemFlow(c, args[0])
		if err != nil {
			return backlogFail(err)
		}
		won, holder, err := wf.Claim(args[0], *as)
		if err != nil {
			return backlogFail(err)
		}
		if !won {
			// The loser posted its claim and then its release on the issue, so
			// it did change state on the way to the refusal (contract: changed true).
			return Result{Data: jsonObj("blockedBy", "claimed", "changed", true)},
				Refused("%s is claimed by %s", id, holder)
		}
		issue := be.Capabilities().ClaimWrites
		res := Result{Data: jsonObj("id", id, "type", typ, "claimId", *as, "changed", issue)}
		if issue {
			res.Text = fmt.Sprintf("claimed %s as %s", id, *as)
		}
		return res, nil
	}
}

func itemRelease(fs *flag.FlagSet) RunFunc {
	as := fs.String("as", "", "claim ID")
	return func(c *Ctx, args []string) (Result, error) {
		if err := argCount(c, args, 1, 1, "item release takes one item ID"); err != nil {
			return Result{}, err
		}
		if err := checkClaimID(*as); err != nil {
			return Result{}, err
		}
		_, wf, id, typ, err := itemFlow(c, args[0])
		if err != nil {
			return backlogFail(err)
		}
		changed, err := wf.Release(args[0], *as)
		if err != nil {
			return backlogFail(err)
		}
		res := Result{Data: jsonObj("id", id, "type", typ, "claimId", *as, "changed", changed)}
		if changed {
			res.Text = fmt.Sprintf("released %s as %s", id, *as)
		}
		return res, nil
	}
}

func itemState(fs *flag.FlagSet) RunFunc {
	to := fs.String("to", "", strings.Join(backlog.States, "|"))
	return func(c *Ctx, args []string) (Result, error) {
		if err := argCount(c, args, 1, 1, "item state takes one item ID"); err != nil {
			return Result{}, err
		}
		if !slices.Contains(backlog.States, *to) {
			return Result{}, Usage("--to must be %s", strings.Join(backlog.States, "|"))
		}
		_, wf, id, typ, err := itemFlow(c, args[0])
		if err != nil {
			return backlogFail(err)
		}
		changed, err := wf.SetState(args[0], *to)
		if err != nil {
			return backlogFail(err)
		}
		state := nullIfEmpty(*to)
		if *to == "none" {
			state = nil
		}
		return Result{Data: jsonObj("id", id, "type", typ, "state", state, "changed", changed),
			Text: fmt.Sprintf("%s state: %s", id, *to)}, nil
	}
}

// ---- item note ------------------------------------------------------------

func checkNoteKind(kind string) error {
	if !slices.Contains(backlog.NoteKinds, kind) {
		return Usage("--kind must be %s", strings.Join(backlog.NoteKinds, "|"))
	}
	return nil
}

// checkWritableNoteKind refuses the kinds only a rota verb writes.
func checkWritableNoteKind(kind string) error {
	if slices.Contains(backlog.ReservedNoteKinds, kind) {
		return Usage("the %s note is written by `rota plan pass`; item note add and rm refuse it", kind)
	}
	return nil
}

func itemNoteAdd(fs *flag.FlagSet) RunFunc {
	kind := fs.String("kind", "", strings.Join(backlog.NoteKinds, "|"))
	bodyFile := fs.String("body-file", "", "note text, path or - for stdin")
	return func(c *Ctx, args []string) (Result, error) {
		if err := argCount(c, args, 1, 1, "item note add takes one item ID"); err != nil {
			return Result{}, err
		}
		if err := checkNoteKind(*kind); err != nil {
			return Result{}, err
		}
		if err := checkWritableNoteKind(*kind); err != nil {
			return Result{}, err
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
			return Result{}, Usage("empty note body")
		}
		_, wf, id, typ, err := itemFlow(c, args[0])
		if err != nil {
			return backlogFail(err)
		}
		changed, err := wf.NotePut(args[0], *kind, text)
		if err != nil {
			return backlogFail(err)
		}
		return Result{Data: jsonObj("id", id, "type", typ, "kind", *kind, "changed", changed),
			Text: fmt.Sprintf("wrote %s note on %s", *kind, id)}, nil
	}
}

func itemNoteShow(fs *flag.FlagSet) RunFunc {
	kind := fs.String("kind", "", strings.Join(backlog.NoteKinds, "|"))
	return func(c *Ctx, args []string) (Result, error) {
		if err := argCount(c, args, 1, 1, "item note show takes one item ID"); err != nil {
			return Result{}, err
		}
		if err := checkNoteKind(*kind); err != nil {
			return Result{}, err
		}
		_, wf, id, typ, err := itemFlow(c, args[0])
		if err != nil {
			return backlogFailRead(err)
		}
		body, ok, err := wf.NoteGet(args[0], *kind)
		if err != nil {
			return backlogFailRead(err)
		}
		return Result{Data: jsonObj("id", id, "type", typ, "kind", *kind, "exists", ok, "body", body), Text: body}, nil
	}
}

func itemNoteRm(fs *flag.FlagSet) RunFunc {
	kind := fs.String("kind", "", strings.Join(backlog.NoteKinds, "|"))
	return func(c *Ctx, args []string) (Result, error) {
		if err := argCount(c, args, 1, 1, "item note rm takes one item ID"); err != nil {
			return Result{}, err
		}
		if err := checkNoteKind(*kind); err != nil {
			return Result{}, err
		}
		if err := checkWritableNoteKind(*kind); err != nil {
			return Result{}, err
		}
		_, wf, id, typ, err := itemFlow(c, args[0])
		if err != nil {
			return backlogFail(err)
		}
		changed, err := wf.NoteRm(args[0], *kind)
		if err != nil {
			return backlogFail(err)
		}
		return Result{Data: jsonObj("id", id, "type", typ, "kind", *kind, "changed", changed),
			Text: fmt.Sprintf("removed %s note on %s", *kind, id)}, nil
	}
}
