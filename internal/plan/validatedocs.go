package plan

import (
	"fmt"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/rotatree"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/l4ci/rota/internal/artifact"
	"github.com/l4ci/rota/internal/frontmatter"
	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
)

// Mismatch is a doc-by-path deliverable whose doc home is missing.
type Mismatch struct {
	Path, TargetRepo, Issue, Suggestion string
}

var (
	filesBullet  = regexp.MustCompile(`(?i)^(\s*)-\s+\*{0,2}Files\*{0,2}:\s*(.*)$`)
	subBullet    = regexp.MustCompile(`^(\s*)-\s+(.+)$`)
	placeholder  = regexp.MustCompile(`^_\(.*\)_$`)
	pathSplit    = regexp.MustCompile(`[\s—(]`)
	tokSplit     = regexp.MustCompile(`[,;]`)
	tasksHeading = regexp.MustCompile(`(?m)^## +Tasks\s*$`)
	nextHeading  = regexp.MustCompile(`(?m)^## +`)
)

// ValidateDocs checks the doc-by-path deliverables in a plan's Files: bullets
// against each target repo's docs home, as hv-plan-validate-docs did. Text is
// the old helper's stdout ("" when clean).
func ValidateDocs(root, key string) (mismatches []Mismatch, text string, err error) {
	if err = CheckKey(Files(root), key); err != nil {
		return
	}
	planPath := filepath.Join(rotatree.DirName, rotatree.PlansDir, key+".md")
	content, rerr := fsio.ReadText(filepath.Join(root, planPath))
	if rerr != nil {
		return nil, "", exitcode.Errf(exitcode.ExitResolution, "plan not found: %s", planPath)
	}
	fm, _, body := frontmatter.Parse(content)
	if fm == nil {
		return nil, "", exitcode.Errf(exitcode.ExitInternal, "plan %s has no parseable frontmatter", planPath)
	}

	docsSegment := "docs"
	if cfg, ok := fsio.LoadJSON(rotatree.Config(root), nil).(*jsonx.Object); ok {
		if dv, ok := cfg.Get("docs"); ok {
			if d, ok := dv.(*jsonx.Object); ok {
				if pv, ok := d.Get("path"); ok {
					if p, _ := pv.(string); strings.Trim(p, "/") != "" {
						docsSegment = strings.Trim(p, "/")
					}
				}
			}
		}
	}

	targetRepos := []string{""}
	if csv := strings.TrimSpace(frontmatter.Str(fm, "repo")); csv != "" {
		targetRepos = artifact.SplitCSV(csv)
	}
	allRepos := artifact.Repos(root)

	var candidates []string
	lines := strings.Split(strings.ReplaceAll(tasksSection(body), "\r", ""), "\n")
	for i := 0; i < len(lines); {
		m := filesBullet.FindStringSubmatch(lines[i])
		if m == nil {
			i++
			continue
		}
		baseIndent := len(m[1])
		if tail := strings.TrimSpace(m[2]); tail != "" && !placeholder.MatchString(tail) {
			for _, tok := range tokSplit.Split(tail, -1) {
				if tok = cleanTok(tok); tok != "" {
					candidates = append(candidates, tok)
				}
			}
		}
		j := i + 1
		for j < len(lines) {
			nxt := lines[j]
			if strings.TrimSpace(nxt) == "" {
				j++
				continue
			}
			bm := subBullet.FindStringSubmatch(nxt)
			if bm == nil || len(bm[1]) <= baseIndent {
				break
			}
			if tok := cleanTok(bm[2]); tok != "" && !placeholder.MatchString(tok) {
				candidates = append(candidates, tok)
			}
			j++
		}
		i = j
	}

	var docPaths []string
	for _, c := range candidates {
		bare := normalizePath(c)
		if bare == "" {
			continue
		}
		for _, seg := range strings.Split(bare, "/") {
			if seg == docsSegment {
				docPaths = append(docPaths, bare)
				break
			}
		}
	}

	for _, p := range docPaths {
		for _, repoName := range targetRepos {
			var repoRoot string
			if repoName == "" {
				repoRoot = root
			} else if r, ok := allRepos[repoName]; ok {
				repoRoot = r
			} else {
				mismatches = append(mismatches, Mismatch{Path: p, TargetRepo: repoName,
					Issue: fmt.Sprintf("sub-repo '%s' is not registered in .rota/repos.json", repoName)})
				continue
			}
			docHome := filepath.Join(repoRoot, docsSegment)
			if fi, serr := os.Stat(docHome); serr == nil && fi.IsDir() {
				continue
			}
			sug := ""
			if repoName != "" {
				if sib, ok := allRepos[repoName+"-docs"]; ok {
					sug = fmt.Sprintf("sibling sub-repo '%s-docs' exists at %s", repoName, sib)
				}
			}
			mismatches = append(mismatches, Mismatch{Path: p, TargetRepo: repoName,
				Issue: fmt.Sprintf("expected doc home %s does not exist", docHome), Suggestion: sug})
		}
	}
	if len(mismatches) == 0 {
		return nil, "", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d doc-deliverable mismatch(es) in %s:\n\n", len(mismatches), planPath)
	for _, w := range mismatches {
		fmt.Fprintf(&b, "  - %s\n", w.Path)
		if w.TargetRepo != "" {
			fmt.Fprintf(&b, "      target repo: %s\n", w.TargetRepo)
		}
		fmt.Fprintf(&b, "      %s\n", w.Issue)
		if w.Suggestion != "" {
			fmt.Fprintf(&b, "      suggested alternative: %s\n", w.Suggestion)
		}
	}
	return mismatches, b.String(), nil
}

func tasksSection(body string) string {
	m := tasksHeading.FindStringIndex(body)
	if m == nil {
		return ""
	}
	rest := body[m[1]:]
	if n := nextHeading.FindStringIndex(rest); n != nil {
		return rest[:n[0]]
	}
	return rest
}

func cleanTok(s string) string {
	return strings.TrimSpace(strings.Trim(strings.TrimSpace(s), "`"))
}

// normalizePath pulls a path-shaped substring out of a free-text token, or
// "" when it has no slash.
func normalizePath(tok string) string {
	bare := pathSplit.Split(tok, 2)[0]
	bare = strings.Trim(strings.TrimSpace(bare), ",")
	bare = strings.Trim(bare, ".")
	bare, _, _ = strings.Cut(bare, ":")
	if !strings.Contains(bare, "/") {
		return ""
	}
	return bare
}
