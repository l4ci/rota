package backlog

import (
	"context"
	"errors"
	gitx "github.com/l4ci/rota/internal/git"
	"os"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/pystr"
)

// Target is one git repo drift walks: a registered sub-repo, or the project
// itself (Name "") outside umbrella mode.
type Target struct{ Name, Dir string }

// DriftCommit is a commit whose subject names a still-open item.
type DriftCommit struct{ Repo, Hash, Subject string }

// DriftItem is an open item that commit subjects already mention.
type DriftItem struct {
	ID, Type string
	Commits  []DriftCommit // oldest first
}

// SymbolDrift is an open item whose title and description name code symbols
// that entered the tree after the item was captured.
type SymbolDrift struct {
	ID, Type string
	Symbols  []string
	Files    []string // at most 5
}

// git runs git in dir. code is git's exit status, -1 when it did not run.
func git(dir string, args ...string) (stdout string, code int) {
	res, err := gitx.Repo{Dir: dir}.Run(context.Background(), args...)
	if err != nil {
		return "", -1
	}
	return res.Stdout, res.ExitCode
}

// reachable is the set of commits in <since>..HEAD of dir, or nil when since
// does not name a commit there. An anchor that starts with "-" is never
// passed to git.
func reachable(dir, since string) map[string]bool {
	if strings.HasPrefix(since, "-") {
		return nil
	}
	if _, rc := git(dir, "cat-file", "-e", since+"^{commit}"); rc != 0 {
		return nil
	}
	out, rc := git(dir, "rev-list", since+"..HEAD")
	if rc != 0 {
		return nil
	}
	set := map[string]bool{}
	for _, h := range strings.Fields(out) {
		set[h] = true
	}
	return set
}

// Drift is hv-todo-drift: it finds open items that commits already mention
// (commit subjects carrying [B07] and the like, newer than the item's Since
// anchor when it has one) and open, Since-anchored items whose described
// symbols appeared in the tree after capture. A missing BACKLOG.md, or one
// with no open items, gives nothing.
func (f *File) Drift(targets []Target) ([]DriftItem, []SymbolDrift, error) {
	content, err := fsio.ReadText(f.backlogPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	open := map[string]bool{}
	since := map[string]string{}
	bullet := map[string]string{}
	for _, sp := range openBulletSpans(content) {
		id := sp.Bullet.ID
		open[id] = true
		bullet[id] = sp.Line
		if s := pystr.Strip(ParseFields(sp.Line).Since); s != "" {
			since[id] = s
		}
	}
	if len(open) == 0 {
		return nil, nil, nil
	}

	hits := map[string][]DriftCommit{}
	for _, t := range targets {
		reach := map[string]map[string]bool{}
		for _, anchor := range since {
			if _, done := reach[anchor]; !done {
				reach[anchor] = reachable(t.Dir, anchor)
			}
		}
		out, rc := git(t.Dir, "log", "--pretty=%H%x09%s")
		if rc != 0 {
			continue
		}
		for _, line := range pystr.Splitlines(out) {
			hash, subject, ok := strings.Cut(line, "\t")
			if !ok {
				continue
			}
			for _, id := range FindItemIDs(subject, "") {
				if !open[id] {
					continue
				}
				if anchor := since[id]; anchor != "" {
					if r := reach[anchor]; r == nil || !r[hash] {
						continue // the commit predates the anchor, or the anchor is unknown here
					}
				}
				hits[id] = append(hits[id], DriftCommit{Repo: t.Name, Hash: hash[:min(7, len(hash))], Subject: subject})
			}
		}
	}
	var drift []DriftItem
	for _, id := range sortedKeys(hits) {
		c := hits[id]
		for i, j := 0, len(c)-1; i < j; i, j = i+1, j-1 {
			c[i], c[j] = c[j], c[i]
		}
		drift = append(drift, DriftItem{ID: id, Type: id[:1], Commits: c})
	}

	var syms []SymbolDrift
	for _, id := range sortedKeys(since) {
		anchor := since[id]
		var matched, files []string
		for _, sym := range ExtractSymbols(bullet[id], id) {
			for _, t := range targets {
				now, ok := grepFound(t.Dir, sym, "HEAD")
				if !ok || len(now) == 0 {
					continue
				}
				then, ok := grepFound(t.Dir, sym, anchor)
				if !ok || len(then) > 0 {
					continue // the anchor does not resolve here, or the symbol was already there
				}
				if !contains(matched, sym) {
					matched = append(matched, sym)
				}
				for _, p := range now {
					if p != "" && !contains(files, p) {
						files = append(files, p)
					}
				}
			}
		}
		if len(matched) > 0 {
			syms = append(syms, SymbolDrift{ID: id, Type: id[:1], Symbols: matched, Files: files[:min(5, len(files))]})
		}
	}
	return drift, syms, nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if x == y {
			return true
		}
	}
	return false
}

// grepFound lists the files that contain symbol at treeish, with the treeish
// prefix removed and .rota/ excluded (the backlog's own text names the symbol).
// ok is false when the grep failed, for instance because treeish does not
// resolve. An anchor that starts with "-" is not passed to git.
func grepFound(dir, symbol, treeish string) (files []string, ok bool) {
	if strings.HasPrefix(treeish, "-") {
		return nil, false
	}
	out, rc := git(dir, "grep", "-I", "-l", "-F", "-e", symbol, treeish, "--", ".", ":(exclude).rota/**")
	switch rc {
	case 0:
	case 1:
		return nil, true
	default:
		return nil, false
	}
	for _, raw := range pystr.Splitlines(out) {
		raw = pystr.Strip(raw)
		if raw == "" {
			continue
		}
		if _, after, found := strings.Cut(raw, ":"); found {
			raw = after
		}
		files = append(files, raw)
	}
	return files, true
}

// ---- symbol extraction ------------------------------------------------------------
//
// The helper's patterns use \b and \w, which are Unicode in Python and ASCII
// in Go. Each pattern is run on a copy of the text where every non-ASCII word
// character is one placeholder byte that no character class of that pattern
// accepts, and every other non-ASCII character is a space; the match offsets
// map back to the original text.

var (
	metaMarkerRe = regexp.MustCompile(`[` + pystr.SpaceClass + `]+(?:Detail|Related|Milestone|Repos|Subsystem|Since|Captured|GH|GL):`)
	leadDashRe   = regexp.MustCompile(`\A[` + pystr.SpaceClass + `]*-[` + pystr.SpaceClass + `]*`)
	idTagRe      = regexp.MustCompile(`\[[A-Za-z]\p{Nd}+\]`)
	bracketRe    = regexp.MustCompile(`\[[^\]]+\]`)
	backtickRe   = regexp.MustCompile("`([^`]+)`")

	camelRe     = regexp.MustCompile(`\b[A-Z][a-z0-9]+(?:[A-Z][a-z0-9]+)+\b`)
	snakeRe     = regexp.MustCompile(`\b[a-z][a-z0-9]*(?:_[a-z0-9]+)+\b`)
	rotaKebabRe = regexp.MustCompile(`\brota-[a-z0-9-]+\b`)
	pathRe      = regexp.MustCompile(`\b[\w.-]*/[\w./-]+\b`)
	fileExtRe   = regexp.MustCompile(`\b[\w-]+\.[a-z]{1,4}\b`)
)

// foldWord returns s with each non-ASCII rune replaced by ph (when it is a
// Python word character) or a space, and the original offset of every byte.
func foldWord(s string, ph byte) (string, []int) {
	var b strings.Builder
	offs := make([]int, 0, len(s)+1)
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		switch {
		case r < utf8.RuneSelf && n == 1:
			b.WriteByte(s[i])
		case pystr.IsWord(r) && !(r == utf8.RuneError && n == 1):
			b.WriteByte(ph)
		default:
			b.WriteByte(' ')
		}
		offs = append(offs, i)
		i += n
	}
	offs = append(offs, len(s))
	return b.String(), offs
}

func findAllFolded(re *regexp.Regexp, text string, ph byte) []string {
	folded, offs := foldWord(text, ph)
	var out []string
	for _, m := range re.FindAllStringIndex(folded, -1) {
		out = append(out, text[offs[m[0]]:offs[m[1]]])
	}
	return out
}

// ExtractSymbols is extract_symbols of hv-todo-drift: up to eight distinctive
// code symbols (backtick spans, CamelCase, snake_case, rota-* names, paths and
// file names) from a bullet's title and description, never its trailing fields.
func ExtractSymbols(bullet, id string) []string {
	text := bullet
	head := bullet
	if loc := metaMarkerRe.FindStringIndex(text); loc != nil {
		text, head = text[:loc[0]], bullet[:loc[0]]
	}
	text = strings.ReplaceAll(text, "**", " ")
	text = leadDashRe.ReplaceAllString(text, "")
	text = idTagRe.ReplaceAllString(text, " ")
	text = bracketRe.ReplaceAllString(text, " ")

	var cands []string
	for _, m := range backtickRe.FindAllStringSubmatch(head, -1) {
		cands = append(cands, m[1])
	}
	cands = append(cands, findAllFolded(camelRe, text, '_')...)
	cands = append(cands, findAllFolded(snakeRe, text, 'Z')...)
	cands = append(cands, findAllFolded(rotaKebabRe, text, 'Z')...)
	cands = append(cands, findAllFolded(pathRe, text, '_')...)
	cands = append(cands, findAllFolded(fileExtRe, text, '_')...)

	seen := map[string]bool{}
	var out []string
	for _, raw := range cands {
		sym := strings.Trim(strings.Trim(pystr.Strip(raw), "`"), ".,;:()[]{}\"'")
		if utf8.RuneCountInString(sym) <= 3 {
			continue
		}
		if sym == id || strings.TrimRight(strings.TrimLeft(sym, "["), "]") == id {
			continue
		}
		if seen[sym] {
			continue
		}
		seen[sym] = true
		out = append(out, sym)
		if len(out) >= 8 {
			break
		}
	}
	return out
}
