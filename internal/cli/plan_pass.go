package cli

import (
	"flag"
	"fmt"
	"regexp"
	"strings"

	"github.com/l4ci/rota/internal/acceptance"
	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/blockedby"
	"github.com/l4ci/rota/internal/plan"
	"github.com/l4ci/rota/internal/proof"
)

var acIDRe = regexp.MustCompile(`^AC-\d+$`)

// passItemRef is the item a plan key names: M01-B07 is B07, #7 and B7 stand
// for themselves. A slice key names no item.
func passItemRef(verb, key string) (string, error) {
	switch {
	case plan.ValidKey(key):
		unit := key[strings.LastIndex(key, "-")+1:]
		if strings.HasPrefix(strings.ToUpper(unit), "S") {
			return "", Usage("%s works on an item's criteria; %s is a slice plan", verb, key)
		}
		return unit, nil
	case plan.ItemOnlyKey(key):
		return key, nil
	}
	return "", Usage("key must look like #7, B7 or M01-B07, got %q", key)
}

// planPass marks one acceptance criterion met. The mark lives in the item's
// `acceptance` note and rests on a PASS proof row; nothing else writes it.
func planPass(fs *flag.FlagSet) RunFunc {
	proofRef := fs.String("proof", "", "the proof row, <sha>:<check> (as `rota proof show` prints them)")
	return func(c *Ctx, args []string) (Result, error) {
		if len(args) != 2 {
			return Result{}, Usage("plan pass takes a plan key and an AC id")
		}
		key, ac := args[0], args[1]
		ref, err := passItemRef("plan pass", key)
		if err != nil {
			return Result{}, err
		}
		if !acIDRe.MatchString(ac) {
			return Result{}, Usage("criterion must look like AC-1, got %q", ac)
		}
		sha, check, _ := strings.Cut(*proofRef, ":")
		if sha = strings.TrimSpace(sha); sha == "" || strings.TrimSpace(check) == "" {
			return Result{}, Usage("--proof must be <sha>:<check>")
		}
		_, issue, err := modeRoot(c)
		if err != nil {
			return Result{}, err
		}
		if !issue {
			return Result{Data: jsonObj("blockedBy", blockedby.Backend, "changed", false)},
				Refused("%s needs item notes, which only the issue backend has (issues.source is file)", c.Path)
		}
		_, wf, id, _, err := itemTrack(c, ref, "plan pass", backlog.FileNoteHint)
		if err != nil {
			return backlogFail(err)
		}
		st, err := wf.Status(ref)
		if err != nil {
			return backlogFail(err)
		}
		numbered, renumbered := acceptance.Number(st.Body)
		crits, dups := acceptance.Parse(numbered)
		if len(dups) > 0 {
			return Result{}, Usage("%s repeats %s; fix the body first", key, strings.Join(dups, ", "))
		}
		var crit *acceptance.Criterion
		for i := range crits {
			if crits[i].ID == ac {
				crit = &crits[i]
			}
		}
		if crit == nil {
			return Result{}, Usage("%s has no criterion %s", key, ac)
		}

		_, store, _, err := openProof(c, ref)
		if err != nil {
			return Result{}, err
		}
		rows, _, err := proof.Show(store, ref)
		if err != nil {
			return failAny(err)
		}
		row, ok := proof.Latest(rows, sha, check)
		if !ok || row.Result != "PASS" {
			why := "no proof row for that sha and check"
			if ok {
				why = "the latest proof row for that sha and check is " + row.Result
			}
			return Result{Data: jsonObj("blockedBy", blockedby.Proof, "changed", false)},
				Refused("cannot mark %s met: %s (see `rota proof show %s`)", ac, why, id)
		}

		changed := false
		if renumbered {
			ed, can := wf.(backlog.BodyEditor)
			if !can {
				return Result{}, Failed("this backend cannot write acceptance ids into the body")
			}
			if changed, err = ed.SetBody(ref, numbered); err != nil {
				return backlogFail(err)
			}
		}
		note, _, err := wf.NoteGet(ref, "acceptance")
		if err != nil {
			return backlogFail(err)
		}
		marks := acceptance.Upsert(acceptance.ParseMarks(note), acceptance.Mark{
			ID: ac, Date: todayDate(c), Sha: row.Sha, Check: row.Check, Text: crit.Text})
		put, err := wf.NotePut(ref, "acceptance", acceptance.RenderMarks(marks))
		if err != nil {
			return backlogFail(err)
		}
		changed = changed || put
		ref2 := fmt.Sprintf("%s:%s", sha, check)
		return Result{
			Data: jsonObj("key", key, "item", id, "ac", ac, "proof", ref2, "changed", changed),
			Text: fmt.Sprintf("%s met (%s)", ac, ref2),
		}, nil
	}
}

// acceptanceCoverage is the coverage of the item's criteria: its body against
// the marks in the acceptance note and the proof rows they rest on. The proof
// rows are read only when there are marks to check.
func acceptanceCoverage(c *Ctx, wf backlog.ItemTracker, ref string, st *backlog.Status) ([]acceptance.Status, error) {
	note, _, err := wf.NoteGet(ref, "acceptance")
	if err != nil {
		return nil, err
	}
	marks := acceptance.ParseMarks(note)
	var rows []proof.Row
	if len(marks) > 0 {
		_, store, _, err := openProof(c, ref)
		if err != nil {
			return nil, err
		}
		pr, _, err := proof.Show(store, ref)
		if err != nil {
			return nil, err
		}
		rows = pr
	}
	return acceptance.Coverage(st.Body, marks, func(sha, check string) (string, bool) {
		r, ok := proof.Latest(rows, sha, check)
		return r.Result, ok
	}), nil
}

// acceptanceData is data.acceptance of item show: [] when there are no criteria.
func acceptanceData(cover []acceptance.Status) []any {
	out := []any{}
	for _, s := range cover {
		out = append(out, jsonObj("id", s.ID, "text", s.Text, "met", s.Met,
			"proof", nullIfEmpty(s.Proof), "flag", nullIfEmpty(s.Flag)))
	}
	return out
}

// acceptanceLines is the text of item show: a count, then a line per
// criterion that carries a flag.
func acceptanceLines(cover []acceptance.Status) []string {
	if len(cover) == 0 {
		return nil
	}
	met := 0
	for _, s := range cover {
		if s.Met {
			met++
		}
	}
	lines := []string{fmt.Sprintf("acceptance: %d/%d met", met, len(cover))}
	for _, s := range cover {
		if s.Flag != "" {
			lines = append(lines, fmt.Sprintf("  %s %s: %s", s.ID, s.Flag, s.Text))
		}
	}
	return lines
}
