package tracker

import (
	"context"
	"net/url"
	"slices"
	"sort"
	"strings"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/labels"
)

// Label is the metadata a tracker keeps beside a label name. Colors use
// lowercase six-digit hex without a leading #; name-only trackers leave it empty.
type Label = labels.Label

// LabelLister reports labels visible in the configured repository or project.
// Name-only trackers implement this without advertising metadata writes.
type LabelLister interface {
	ListLabels(context.Context) ([]Label, error)
}

// LabelManager creates labels and updates metadata without deleting or renaming.
type LabelManager interface {
	LabelLister
	UpsertLabel(context.Context, Label) error
}

var (
	_ LabelManager = (*GitHub)(nil)
	_ LabelManager = (*GitLab)(nil)
	_ LabelManager = (*Linear)(nil)
	_ LabelLister  = (*Jira)(nil)
	_ LabelLister  = (*Itsaplan)(nil)
)

// labelRule says whether a forge treats label names that differ only in case as
// one label. It is the one place that rule lives: adapters pass their rule to
// sameLabelName, labelKey and hasLabel instead of comparing names themselves.
type labelRule bool

const (
	// labelsFoldCase: GitHub, Linear and It's a Plan match names ignoring case
	// (creating "Bug" next to "bug" is refused or merged by the forge).
	labelsFoldCase labelRule = true
	// labelsExact: GitLab and Jira match names byte for byte, so "Bug" and "bug"
	// are two labels. Changing a forge's rule needs a recorded decision.
	labelsExact labelRule = false
)

// Per-forge rules.
const (
	githubLabelRule   = labelsFoldCase
	gitlabLabelRule   = labelsExact
	linearLabelRule   = labelsFoldCase
	itsaplanLabelRule = labelsFoldCase
	jiraLabelRule     = labelsExact
)

// labelKey is the map key two names share exactly when the rule calls them one label.
func labelKey(r labelRule, name string) string {
	if r == labelsFoldCase {
		return strings.ToLower(name)
	}
	return name
}

// sameLabelName reports whether a and b name the same label under the rule.
func sameLabelName(r labelRule, a, b string) bool {
	if r == labelsFoldCase {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// hasLabel reports whether names holds want under the rule.
func hasLabel(r labelRule, names []string, want string) bool {
	return slices.ContainsFunc(names, func(n string) bool { return sameLabelName(r, n, want) })
}

// sourceLabelRule is the rule of the tracker an issues.source or forge provider
// names, and whether the source is listed. TestSourceLabelRuleCoversSources
// fails when a source an IssueActor can resolve is missing here.
func sourceLabelRule(source string) (labelRule, bool) {
	switch source {
	case config.SourceGitHub:
		return githubLabelRule, true
	case config.SourceGitLab:
		return gitlabLabelRule, true
	case config.SourceLinear:
		return linearLabelRule, true
	case config.SourceItsaplan:
		return itsaplanLabelRule, true
	case config.SourceJira:
		return jiraLabelRule, true
	}
	return labelsExact, false
}

// HasLabel reports whether names holds want under the label rule of the
// tracker the actor resolved, so a caller outside this package never compares
// label names itself. An unlisted source matches exactly.
func (a IssueActor) HasLabel(names []string, want string) bool {
	r, _ := sourceLabelRule(a.Source)
	return hasLabel(r, names, want)
}

func orderedLabels(ls []Label) []Label {
	for i := range ls {
		ls[i].Color = labels.NormalizeColor(ls[i].Color)
	}
	sort.Slice(ls, func(i, j int) bool { return ls[i].Name < ls[j].Name })
	return ls
}

func sameLabelMetadata(a, b Label) bool {
	return a.Description == b.Description && labels.NormalizeColor(a.Color) == labels.NormalizeColor(b.Color)
}

func (g *GitHub) ListLabels(ctx context.Context) ([]Label, error) {
	var out []Label
	if err := g.pages(ctx, "repos/{owner}/{repo}/labels?per_page=100", &out); err != nil {
		return nil, err
	}
	return orderedLabels(out), nil
}

func (g *GitHub) UpsertLabel(ctx context.Context, want Label) error {
	have, err := g.ListLabels(ctx)
	if err != nil {
		return err
	}
	for _, lb := range have {
		if sameLabelName(githubLabelRule, lb.Name, want.Name) {
			if sameLabelMetadata(lb, want) {
				return nil
			}
			_, err := g.run(ctx, []string{"label", "edit", lb.Name, "--description", want.Description, "--color", labels.NormalizeColor(want.Color)}, "")
			return err
		}
	}
	_, err = g.run(ctx, []string{"label", "create", want.Name, "--description", want.Description, "--color", labels.NormalizeColor(want.Color)}, "")
	return err
}

func (g *GitLab) ListLabels(ctx context.Context) ([]Label, error) {
	var out []Label
	if err := g.pages(ctx, "projects/:id/labels?per_page=100", &out); err != nil {
		return nil, err
	}
	return orderedLabels(out), nil
}

func (g *GitLab) UpsertLabel(ctx context.Context, want Label) error {
	have, err := g.ListLabels(ctx)
	if err != nil {
		return err
	}
	method, path := "POST", "projects/:id/labels"
	args := []string{}
	for _, lb := range have {
		if sameLabelName(gitlabLabelRule, lb.Name, want.Name) {
			if sameLabelMetadata(lb, want) {
				return nil
			}
			method, path = "PUT", path+"/"+url.PathEscape(lb.Name)
			break
		}
	}
	args = append(args, "api", "-X", method, path)
	if method == "POST" {
		args = append(args, "-f", "name="+want.Name)
	}
	args = append(args, "-f", "description="+want.Description, "-f", "color=#"+labels.NormalizeColor(want.Color))
	_, err = g.run(ctx, args, "")
	return err
}

func (l *Linear) ListLabels(ctx context.Context) ([]Label, error) {
	if err := l.loadLabels(ctx); err != nil {
		return nil, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Label, 0, len(l.labels))
	for _, lb := range l.labels {
		out = append(out, Label{Name: lb.Name, Description: lb.Description, Color: lb.Color})
	}
	return orderedLabels(out), nil
}

func (l *Linear) UpsertLabel(ctx context.Context, want Label) error {
	if err := l.loadLabels(ctx); err != nil {
		return err
	}
	l.mu.Lock()
	lb, exists := l.labels[labelKey(linearLabelRule, want.Name)]
	teamID := l.teamID
	l.mu.Unlock()
	if exists && sameLabelMetadata(Label{Description: lb.Description, Color: lb.Color}, want) {
		return nil
	}
	in := map[string]any{"description": want.Description, "color": "#" + labels.NormalizeColor(want.Color)}
	if exists {
		var d struct {
			IssueLabelUpdate struct {
				Success bool `json:"success"`
			} `json:"issueLabelUpdate"`
		}
		if err := l.mutate(ctx, "LabelUpdate", `($id: String!, $input: IssueLabelUpdateInput!) { issueLabelUpdate(id: $id, input: $input) { success } }`, map[string]any{"id": lb.ID, "input": in}, &d); err != nil {
			return err
		}
		if err := succeeded("LabelUpdate", d.IssueLabelUpdate.Success); err != nil {
			return err
		}
	} else {
		in["name"], in["teamId"] = want.Name, teamID
		var d struct {
			IssueLabelCreate struct {
				Success    bool        `json:"success"`
				IssueLabel linearLabel `json:"issueLabel"`
			} `json:"issueLabelCreate"`
		}
		if err := l.mutate(ctx, "LabelCreate", `($input: IssueLabelCreateInput!) { issueLabelCreate(input: $input) { success issueLabel { id name description color team { id } } } }`, map[string]any{"input": in}, &d); err != nil {
			return err
		}
		if err := succeeded("LabelCreate", d.IssueLabelCreate.Success); err != nil {
			return err
		}
		lb = d.IssueLabelCreate.IssueLabel
	}
	lb.Description, lb.Color = want.Description, "#"+labels.NormalizeColor(want.Color)
	l.mu.Lock()
	l.labels[labelKey(linearLabelRule, want.Name)] = lb
	l.mu.Unlock()
	return nil
}

// ListLabels reports the names used on visible issues in this project. Jira
// labels have neither independent definitions nor description/color metadata.
func (j *Jira) ListLabels(ctx context.Context) ([]Label, error) {
	issues, err := j.List(ctx, ListFilter{State: "all"})
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	out := []Label{}
	for _, is := range issues {
		for _, name := range is.Labels {
			if !seen[name] {
				seen[name] = true
				out = append(out, Label{Name: name})
			}
		}
	}
	return orderedLabels(out), nil
}

// ListLabels reports the name-only project definitions documented by the API.
func (t *Itsaplan) ListLabels(ctx context.Context) ([]Label, error) {
	m, err := t.projectMeta(ctx)
	if err != nil {
		return nil, err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]Label, 0, len(m.Labels))
	for _, lb := range m.Labels {
		out = append(out, Label{Name: lb.Name})
	}
	return orderedLabels(out), nil
}

func labelMetadata(name string, entries []labels.Entry) (Label, bool) {
	if entries == nil {
		return labels.Lookup(name, nil)
	}
	return labels.Find(entries, name)
}
