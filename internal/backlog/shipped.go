package backlog

import (
	"context"
	gitx "github.com/l4ci/rota/internal/git"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/l4ci/rota/internal/pystr"
)

// The duplicate-shipped audit behind `rota item shipped` (bin/hv-capture-audit):
// cheap heuristics only, distinctive tokens grepped against git log subjects
// plus filesystem path checks.

// Hit levels.
const (
	HitStrong = "strong"
	HitMedium = "medium"
	HitPath   = "path"
)

// Hit is one piece of ship evidence for a title.
type Hit struct {
	Level   string
	Hash    string   // strong, medium
	Subject string   // strong, medium: the commit subject without its hash
	Tokens  []string // strong, medium
	Token   string   // path
	Path    string   // path: relative to the repo root
}

// TitleAudit is the evidence found for one title.
type TitleAudit struct {
	Title string
	Hits  []Hit // strong, then medium, then path hits
}

var stopwords = func() map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(`
the a an and or but in on at to for of with by from into through during before after
above below between against is are was were be been being have has had do does did will
would should could may might can must this that these those then than when where why how
all any both each few more most other some such no not only own same so too very just now
add fix update remove change support allow enable disable new old via use using used
should would could`) {
		m[w] = true
	}
	return m
}()

var tokenRe = regexp.MustCompile("`([^`]+)`|([A-Za-z][A-Za-z0-9_./-]{2,})")

func dedupe(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

// extractTokens splits a title into distinctive tokens (quoted, or holding
// one of "-_/.") and common ones (4+ characters, lower-cased), both
// deduplicated in order.
func extractTokens(title string) (distinctive, common []string) {
	for _, m := range tokenRe.FindAllStringSubmatch(title, -1) {
		tok, quoted := m[1], m[1] != ""
		if !quoted {
			tok = m[2]
		}
		low := strings.ToLower(tok)
		if stopwords[low] {
			continue
		}
		if quoted || strings.ContainsAny(tok, "-_/.") {
			distinctive = append(distinctive, tok)
		} else if utf8.RuneCountInString(tok) >= 4 {
			common = append(common, low)
		}
	}
	return dedupe(distinctive), dedupe(common)
}

// grepCommits is `git log --oneline --grep=<tok> -i -10` in dir.
func grepCommits(dir, tok string) []string {
	res, err := gitx.Repo{Dir: dir}.Run(context.Background(), "log", "--oneline", "--grep="+tok, "-i", "-10")
	if err != nil || res.Code != 0 {
		return nil
	}
	out := res.Stdout
	var lines []string
	for _, l := range pystr.Splitlines(out) {
		if l = pystr.Strip(l); l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

// pathExists is path_exists: the token as a path under root, relative and
// normalized the way pathlib does, or "".
func pathExists(tok, root string) string {
	p := strings.TrimLeft(strings.Trim(tok, "`"), "/")
	if !strings.ContainsAny(p, "/.") || strings.Contains(p, " ") {
		return ""
	}
	var parts []string
	for _, c := range strings.Split(p, "/") {
		if c != "" && c != "." {
			parts = append(parts, c)
		}
	}
	rel := strings.Join(parts, "/")
	if rel == "" {
		rel = "." // root / "./" is the root itself
	}
	if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
		return ""
	}
	return rel
}

type commitHit struct {
	subject string // the oneline: "<hash> <subject>"
	tokens  []string
}

// AuditTitle gathers the evidence for one title in the git repo at dir.
func AuditTitle(dir, root, title string) TitleAudit {
	distinctive, common := extractTokens(title)
	var paths []Hit
	for _, tok := range distinctive {
		if p := pathExists(tok, root); p != "" {
			paths = append(paths, Hit{Level: HitPath, Token: tok, Path: p})
		}
	}
	commits := map[string]*commitHit{}
	var order []string
	for _, tok := range append(append([]string{}, distinctive...), common...) {
		for _, line := range grepCommits(dir, tok) {
			sha := strings.Fields(line)[0]
			c, ok := commits[sha]
			if !ok {
				c = &commitHit{subject: line}
				commits[sha] = c
				order = append(order, sha)
			}
			c.tokens = append(c.tokens, tok)
		}
	}
	isDistinct := map[string]bool{}
	for _, d := range distinctive {
		isDistinct[d] = true
	}
	var strong, medium []Hit
	for _, sha := range order {
		c := commits[sha]
		uniq := dedupe(c.tokens)
		d := 0
		for _, t := range uniq {
			if isDistinct[t] {
				d++
			}
		}
		h := Hit{Hash: sha, Tokens: uniq}
		h.Subject = pystr.Strip(strings.TrimPrefix(c.subject, sha))
		switch {
		case len(uniq) >= 3 || d >= 2:
			h.Level = HitStrong
			strong = append(strong, h)
		case len(uniq) >= 2:
			h.Level = HitMedium
			medium = append(medium, h)
		}
	}
	res := TitleAudit{Title: title}
	res.Hits = append(res.Hits, strong[:min(3, len(strong))]...)
	res.Hits = append(res.Hits, medium[:min(3, len(medium))]...)
	res.Hits = append(res.Hits, paths...)
	return res
}

// GitRoot is `git rev-parse --show-toplevel` in dir, or dir itself when it is
// not in a repo.
func GitRoot(dir string) string {
	top, ok, err := gitx.Repo{Dir: dir}.Toplevel(context.Background())
	if err != nil || !ok {
		return dir
	}
	return pystr.Strip(top)
}

// Audit runs the audit for each non-blank title in the git repo around dir.
// Titles without evidence are listed with no hits.
func Audit(dir string, titles []string) []TitleAudit {
	root := GitRoot(dir)
	var out []TitleAudit
	for _, t := range titles {
		if pystr.Strip(t) == "" {
			continue
		}
		out = append(out, AuditTitle(dir, root, t))
	}
	return out
}
