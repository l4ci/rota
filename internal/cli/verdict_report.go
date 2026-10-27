package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/verdict"
	"github.com/l4ci/rota/internal/worker"
)

// reportTargets is resolved at posting time: a verdict may precede its PR.
// File backends only need the linked items; issue backends also need the PR.
type reportTargets struct {
	be      backlog.Backend
	forge   tracker.Forge
	pr      int
	ids     []string
	pending []error
}

func resolveReportTargets(c *Ctx, t verdictBranch, prRef string, bodies []verdict.ItemVerdict) reportTargets {
	var dst reportTargets
	cfg := c.deps().Config(t.root)
	source, _ := config.IssueSource(cfg)
	keySource := source == "jira" || source == "linear" || source == "itsaplan"
	add := func(id string) {
		if key, ok := tracker.ParseKey(id); ok {
			id = key.String()
		}
		if id != "" && !slices.Contains(dst.ids, id) {
			dst.ids = append(dst.ids, id)
		}
	}
	issue, err := issueBackend(c.deps(), t.root)
	if err != nil {
		dst.pending = append(dst.pending, err)
		return dst
	}
	for _, it := range bodies {
		add(it.ID)
	}
	if issue {
		if keySource {
			for _, id := range reportKeyItems(t.branch, "") {
				add(id)
			}
		} else {
			add(backlog.BranchIssueRef(t.branch))
		}
	}
	// Match the existing review scope: file item IDs and issue closing references
	// come from commit bodies, including the branch's leading issue in issue mode.
	if bt, err := resolveBranch(c, []string{t.ref}); err == nil {
		if log, err := reviewGit(c.Context(), c.deps(), t.dir, "log", "--format=%B", bt.Base+".."+t.ref); err == nil {
			if issue {
				for _, id := range backlog.FindIssueRefs(log) {
					add(id)
				}
			} else {
				for _, id := range backlog.FindItemIDs(log, backlog.ItemLetters) {
					add(id)
				}
			}
		} else {
			dst.pending = append(dst.pending, err)
		}
	} else {
		dst.pending = append(dst.pending, err)
	}
	f, err := c.deps().forge(c.Context(), cfg, "", t.dir)
	if err != nil {
		// A file-backed project with no forge still has its durable detail reports.
		if issue || !errors.Is(err, tracker.ErrLocalForge) {
			dst.pending = append(dst.pending, err)
		}
	} else {
		dst.forge = f
		if prRef != "" {
			dst.pr, _ = worker.PRRefNumber(prRef)
		}
		var body, title string
		if dst.pr > 0 {
			info, err := f.PRView(c.Context(), dst.pr)
			if err != nil {
				dst.pending = append(dst.pending, err)
			} else {
				body, title = info.Body, info.Title
			}
		} else if prs, err := f.OpenPRs(c.Context()); err != nil {
			dst.pending = append(dst.pending, err)
		} else {
			for _, pr := range prs {
				if pr.Branch == t.branch && pr.SameRepository {
					dst.pr, body, title = pr.Number, pr.Body, pr.Title
					break
				}
			}
		}
		if dst.pr == 0 {
			dst.pending = append(dst.pending, fmt.Errorf("no PR yet for %s", t.branch))
		}
		if issue {
			if keySource {
				for _, id := range reportKeyItems(t.branch, title) {
					add(id)
				}
			}
			for _, n := range f.ClosedNumbers(body) {
				add(tracker.NumKey(n).String())
			}
			for _, id := range backlog.FindIssueRefs(body) {
				add(id)
			}
		} else {
			for _, id := range backlog.FindItemIDs(body, backlog.ItemLetters) {
				add(id)
			}
		}
	}
	if len(dst.ids) == 0 {
		dst.pending = append(dst.pending, errors.New("no linked item found"))
	} else {
		dst.be, err = openBacklog(c, t.root, false, "")
		if err != nil {
			dst.pending = append(dst.pending, err)
		}
	}
	return dst
}

func (dst reportTargets) post(c *Ctx, markdown string) error {
	errs := append([]error{}, dst.pending...)
	if dst.forge != nil && dst.pr > 0 {
		if err := tracker.PostReport(c.Context(), dst.forge, dst.pr, markdown); err != nil {
			errs = append(errs, fmt.Errorf("PR %d: %w", dst.pr, err))
		}
	}
	if dst.be != nil {
		for _, id := range dst.ids {
			if err := backlog.PostReport(dst.be, id, markdown); err != nil {
				errs = append(errs, fmt.Errorf("item %s: %w", id, err))
			}
		}
	}
	return errors.Join(errs...)
}

// Only existing cited evidence enters the artifact list. The Body schema stays
// unchanged: producers put commands and paths in summary/findings as before.
var qaArtifactRE = regexp.MustCompile(`\.rota/qa-runs/[^\s\x60"'<>\)\],;]+`)

func verdictArtifacts(root string, r verdict.Record) []string {
	text := r.Summary + " " + r.Transcript
	for _, f := range r.Findings {
		text += " " + f.File + " " + f.Detail
	}
	var paths []string
	for _, path := range qaArtifactRE.FindAllString(text, -1) {
		path = strings.TrimRight(path, ".:")
		clean := filepath.Clean(path)
		if !strings.HasPrefix(filepath.ToSlash(clean), ".rota/qa-runs/") {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, clean)); err == nil && !slices.Contains(paths, path) {
			paths = append(paths, path)
		}
	}
	return paths
}

func postVerdictReport(c *Ctx, t verdictBranch, r verdict.Record) error {
	return postVerdictReportTo(c, t, r, "")
}

func postVerdictReportTo(c *Ctx, t verdictBranch, r verdict.Record, prRef string) error {
	return resolveReportTargets(c, t, prRef, r.Items).post(c, verdict.RenderReport(r, verdictArtifacts(t.root, r)))
}

func setReportStatus(d *jsonx.Object, err error) {
	d.Set("report", "posted")
	if err != nil {
		d.Set("report", "pending")
		d.Set("reportReason", err.Error())
	}
}

// A known PR ref lets ship deliver pre-PR verdicts without a discovery race.
func postLatestVerdictReports(c *Ctx, t verdictBranch, prRef string) ([]any, error) {
	list := verdict.Load(t.root).Branches[t.key]
	reports := []any{}
	var errs []error
	for _, kind := range verdict.BranchKinds {
		if r, ok := verdict.Latest(list, kind); ok {
			err := postVerdictReportTo(c, t, r, prRef)
			row := jsonx.NewObject()
			row.Set("kind", kind)
			row.Set("sha", r.Sha)
			setReportStatus(row, err)
			reports = append(reports, row)
			if err != nil {
				errs = append(errs, err)
			}
		}
	}
	return reports, errors.Join(errs...)
}

func verdictPost(c *Ctx, args []string) (Result, error) {
	t, err := resolveVerdictBranch(c, args)
	if err != nil {
		return Result{}, err
	}
	reports, postErr := postLatestVerdictReports(c, t, "")
	d := jsonx.NewObject()
	d.Set("branch", t.branch)
	d.Set("reports", reports)
	d.Set("changed", len(reports) > 0)
	setReportStatus(d, postErr)
	return Result{Data: d, Text: fmt.Sprintf("%d report(s) retried for %s", len(reports), t.branch)}, nil
}
