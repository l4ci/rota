package cli

import (
	"flag"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/l4ci/rota/internal/acceptance"
	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/blockedby"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/plan"
	"github.com/l4ci/rota/internal/proof"
)

var acIDRe = regexp.MustCompile(`^AC-\d+$`)

// passItemRef is the item a plan key names: M01-B07 is B07, #7, B7 and
// PROJ-123 stand for themselves. A slice key names no item.
func passItemRef(verb, key string) (string, error) {
	r, ok := plan.Resolve(key)
	switch {
	case !ok:
		return "", Usage("key must look like #7, B7, PROJ-123 or M01-B07, got %q", key)
	case r.Slice:
		return "", Usage("%s works on an item's criteria; %s is a slice plan", verb, key)
	}
	return r.Item.ID, nil
}

// planPass marks one acceptance criterion met. The mark lives in the item's
// `acceptance` note and rests on a PASS proof row; nothing else writes it.
func planPass(fs *flag.FlagSet) RunFunc {
	proofRef := fs.String("proof", "", "the proof row, <sha>:<check> (as `rota proof show` prints them)")
	decisionRef := fs.String("decision", "", "instead of --proof (the two are exclusive): the id or URL of a decision-marker comment, by an author with write access, that settles the criterion")
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
		byDecision := strings.TrimSpace(*decisionRef) != ""
		sha, check, _ := strings.Cut(*proofRef, ":")
		switch {
		case byDecision && strings.TrimSpace(*proofRef) != "":
			return Result{}, Usage("--proof and --decision are exclusive")
		case byDecision:
			sha, check = acceptance.DecisionSha, commentRefID(*decisionRef)
		case strings.TrimSpace(sha) == "" || strings.TrimSpace(check) == "":
			return Result{}, Usage("--proof must be <sha>:<check> (or give --decision <comment>)")
		}
		sha = strings.TrimSpace(sha)
		root, issue, err := modeRoot(c)
		if err != nil {
			return Result{}, err
		}
		if !issue {
			return Result{Data: jsonx.Obj("blockedBy", blockedby.Backend, "changed", false)},
				Refused("%s needs item notes, which only the issue backend has (issues.source is file)", c.Path)
		}
		be, wf, id, _, err := itemTrack(c, ref, "plan pass", backlog.FileNoteHint)
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

		var row proof.Row
		if byDecision {
			ids, err := decisionIDs(be, ref)
			if err != nil {
				return backlogFail(err)
			}
			if !slices.Contains(ids, check) {
				return Result{Data: jsonx.Obj("blockedBy", blockedby.UnmetAcceptance, "changed", false)},
					Refused("cannot mark %s met: comment %s on %s is not a decision by someone with write access (it needs the rota decision marker, and a tracker that reports the author's access; see `rota item comment list %s --kind decision`)", ac, check, id, id)
			}
			row = proof.Row{Sha: sha, Check: check}
		} else {
			_, store, _, err := openProof(c, ref)
			if err != nil {
				return Result{}, err
			}
			rows, _, err := proof.Show(store, ref)
			if err != nil {
				return failAny(err)
			}
			r, ok := proof.Latest(rows, sha, check)
			if !ok || r.Result != "PASS" {
				why := "no proof row for that sha and check"
				if ok {
					why = "the latest proof row for that sha and check is " + r.Result
				}
				return Result{Data: jsonx.Obj("blockedBy", blockedby.Proof, "changed", false)},
					Refused("cannot mark %s met: %s (see `rota proof show %s`)", ac, why, id)
			}
			row = r
		}

		changed := false
		if renumbered {
			ed, can := backlog.Optional[backlog.BodyEditor](be, func(c backlog.Capabilities) bool { return c.BodyEdit })
			if !can {
				return Result{}, Failed("this backend cannot write acceptance ids into the body")
			}
			if changed, err = ed.SetBody(ref, numbered); err != nil {
				return backlogFail(err)
			}
		}
		put, err := markAcceptance(c, root, id, acceptance.Mark{
			ID: ac, Date: todayDate(c), Sha: row.Sha, Check: row.Check, Text: crit.Text})
		if err != nil {
			return backlogFail(err)
		}
		changed = changed || put
		ref2 := fmt.Sprintf("%s:%s", sha, check)
		if byDecision {
			ref2 = "decision " + check
		}
		return Result{
			Data: jsonx.Obj("key", key, "item", id, "ac", ac, "proof", ref2, "changed", changed),
			Text: fmt.Sprintf("%s met (%s)", ac, ref2),
		}, nil
	}
}

// acceptanceCoverage is the coverage of the item's criteria: its body against
// the marks in the acceptance note and the proof rows they rest on. The proof
// rows are read only when there are marks to check.
func acceptanceCoverage(c *Ctx, be backlog.Backend, wf backlog.ItemTracker, ref string, st *backlog.Status) ([]acceptance.Status, error) {
	note, _, err := wf.NoteGet(ref, "acceptance")
	if err != nil {
		return nil, err
	}
	marks := acceptance.ParseMarks(note)
	var rows []proof.Row
	var decisions []string
	for _, m := range marks {
		if m.Sha == acceptance.DecisionSha {
			if decisions, err = decisionIDs(be, ref); err != nil {
				return nil, err
			}
			break
		}
	}
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
	return acceptance.Coverage(st.Body, marks, acceptance.WithDecisions(func(sha, check string) (string, bool) {
		r, ok := proof.Latest(rows, sha, check)
		return r.Result, ok
	}, decisions)), nil
}

// decisionIDs are the ids of the item's decision comments whose author has write
// access; a backend without foreign authors (the file backend) takes the marker.
func decisionIDs(be backlog.Backend, ref string) ([]string, error) {
	if t, ok := be.(interface {
		DecisionIDs(string) ([]string, error)
	}); ok {
		return t.DecisionIDs(ref)
	}
	rows, err := be.Comments(ref, "decision")
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	return ids, nil
}

var commentURLRe = regexp.MustCompile(`issuecomment-(\d+)$`)

// commentRefID is the comment id a --decision value names: the bare id or the
// comment's URL (…#issuecomment-<id>).
func commentRefID(ref string) string {
	ref = strings.TrimSpace(ref)
	if m := commentURLRe.FindStringSubmatch(ref); m != nil {
		return m[1]
	}
	return ref
}

// acceptanceData is data.acceptance of item show: [] when there are no criteria.
func acceptanceData(cover []acceptance.Status) []any {
	out := []any{}
	for _, s := range cover {
		out = append(out, jsonx.Obj("id", s.ID, "text", s.Text, "met", s.Met,
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
