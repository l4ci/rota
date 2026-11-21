package round

import (
	"context"
	"regexp"
	"strconv"
	"strings"

	"github.com/l4ci/rota/internal/worker"
)

// sectionFile is a section file's name and its leading number.
var sectionFile = regexp.MustCompile(`^(\d+)_`)

// smokeMention is the pattern marking an issue that adds a smoke section: its
// body or criteria say so, or name dir.
func smokeMention(dir string) *regexp.Regexp {
	return regexp.MustCompile(`(?i)smoke[ -]+section|` + regexp.QuoteMeta(dir))
}

// MentionsSmokeSection says whether an issue's text asks for a smoke section.
// dir is test.smokeSectionsDir; empty means the project has none, so no issue asks.
func MentionsSmokeSection(text, dir string) bool {
	return dir != "" && smokeMention(dir).MatchString(text)
}

// sectionsOn is the highest section number under dir on one ref, and
// whether the directory exists there. A ref git cannot resolve counts as none.
func (e Env) sectionsOn(ctx context.Context, root, dir, ref string) (max int, found bool) {
	res, err := e.Git(ctx, root, "ls-tree", "--name-only", ref, strings.TrimSuffix(dir, "/")+"/")
	if err != nil || res.ExitCode != 0 {
		return 0, false
	}
	for _, l := range strings.Split(res.Stdout, "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		found = true
		if m := sectionFile.FindStringSubmatch(l[strings.LastIndex(l, "/")+1:]); m != nil {
			if n, _ := strconv.Atoi(m[1]); n > max {
				max = n
			}
		}
	}
	return max, found
}

// reserveSmokeSection is the next free smoke section number for id's worker,
// 0 when the issue asks for none, the project sets no smoke sections directory
// (test.smokeSectionsDir, passed as dir) or that directory is absent everywhere. Free
// means above every number on the base, on every open PR branch and in-flight
// slot branch (as the local checkout last saw them, `origin/` first), and
// above every number another slot has reserved but not yet pushed. The slot being
// assigned (agent) keeps its number when it already holds id, so a resumed
// assign is stable; the other attempt of a best-of:2 issue takes a new one.
func (e Env) reserveSmokeSection(ctx context.Context, root string, be Board, reg worker.Registry, dir string, roster []string, id, agent string) int {
	text, _, _ := be.Detail(id)
	if !MentionsSmokeSection(text, dir) {
		return 0
	}
	refs := []string{e.Base, "origin/" + e.Base}
	for _, name := range roster {
		s := reg.Slot(name)
		if s == nil {
			continue
		}
		if s.Name() == agent && s.Holds(id) && s.SmokeSection() != 0 {
			return s.SmokeSection()
		}
		if b := s.Branch(); b != "" && !worker.IsPark(b) {
			refs = append(refs, b, "origin/"+b)
		}
	}
	if e.Forge != nil {
		if prs, err := e.Forge.OpenPRs(ctx); err == nil {
			for _, pr := range prs {
				refs = append(refs, pr.Branch, "origin/"+pr.Branch)
			}
		}
	}
	top, any := 0, false
	for _, r := range refs {
		n, ok := e.sectionsOn(ctx, root, dir, r)
		top, any = max(top, n), any || ok
	}
	if !any {
		return 0
	}
	for _, name := range roster {
		if s := reg.Slot(name); s != nil {
			top = max(top, s.SmokeSection())
		}
	}
	return top + 1
}
