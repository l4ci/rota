package knowledge

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/section"
)

// ErrAliasCollision: an alias already belongs to another term, or two terms of
// one batch claim it, or a term repeats inside a batch.
var ErrAliasCollision = fmt.Errorf("alias collision")

// ErrManifest: a malformed import line.
var ErrManifest = fmt.Errorf("manifest")

// Term is one nested-bullet entry of the "## Glossary" topic.
type Term struct {
	Name       string
	Definition string
	Aliases    []string
	Nots       []string
	Date       string // "" when the entry has no date comment
	Line       int    // manifest line, 0 outside an import
}

var (
	termHead  = regexp.MustCompile(`^- \*\*([^*]+)\*\*\s*(?:—\s*(.*))?\s*$`)
	aliasLine = regexp.MustCompile(`^- \*\*Aliases:\*\*\s*(.*)$`)
	notLine   = regexp.MustCompile(`^- \*\*Not:\*\*\s*(.*)$`)
	dateLine  = regexp.MustCompile(`^<!--\s*(\d{4}-\d{2}-\d{2})\s*-->\s*$`)
	bulletTop = regexp.MustCompile(`^- \*\*`)
)

// splitKeep is str.splitlines(keepends=True) for \n-terminated text.
func splitKeep(s string) []string {
	var out []string
	for len(s) > 0 {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			out = append(out, s)
			break
		}
		out = append(out, s[:i+1])
		s = s[i+1:]
	}
	return out
}

// SplitCSV splits a comma-separated cell into trimmed, non-empty tokens.
func SplitCSV(s string) []string {
	out := []string{}
	for _, x := range strings.Split(s, ",") {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

// ParseGlossary reads the entries of a "## Glossary" body in document order.
// leading is the prose before the first entry (or the "(no terms yet)" note).
func ParseGlossary(body string) (entries []Term, leading string) {
	lines := splitKeep(body)
	i := 0
	var lead strings.Builder
	for i < len(lines) && !bulletTop.MatchString(lines[i]) {
		lead.WriteString(lines[i])
		i++
	}
	for i < len(lines) {
		m := termHead.FindStringSubmatch(strings.TrimRight(lines[i], "\n"))
		if m == nil {
			i++
			continue
		}
		t := Term{Name: strings.TrimSpace(m[1]), Aliases: []string{}, Nots: []string{}}
		var parts []string
		if rest := strings.TrimRight(m[2], " \t\r\f\v"); rest != "" {
			parts = append(parts, rest)
		}
		i++
		for i < len(lines) {
			sub := lines[i]
			if bulletTop.MatchString(sub) {
				break
			}
			stripped := strings.TrimSpace(sub)
			switch {
			case stripped == "":
			case aliasLine.MatchString(stripped):
				if v := strings.TrimSpace(aliasLine.FindStringSubmatch(stripped)[1]); v != "" && v != "_none_" {
					t.Aliases = SplitCSV(v)
				}
			case notLine.MatchString(stripped):
				if v := strings.TrimSpace(notLine.FindStringSubmatch(stripped)[1]); v != "" {
					t.Nots = SplitCSV(v)
				}
			case dateLine.MatchString(stripped):
				t.Date = dateLine.FindStringSubmatch(stripped)[1]
			default:
				parts = append(parts, stripped)
			}
			i++
		}
		t.Definition = strings.TrimSpace(strings.Join(parts, " "))
		entries = append(entries, t)
	}
	return entries, lead.String()
}

func buildEntry(t Term, today string) string {
	lines := []string{fmt.Sprintf("- **%s** — %s", t.Name, t.Definition)}
	aliases := "_none_"
	if len(t.Aliases) > 0 {
		aliases = strings.Join(t.Aliases, ", ")
	}
	lines = append(lines, "  - **Aliases:** "+aliases)
	if len(t.Nots) > 0 {
		lines = append(lines, "  - **Not:** "+strings.Join(t.Nots, ", "))
	}
	date := t.Date
	if date == "" {
		date = today
	}
	lines = append(lines, "  <!-- "+date+" -->")
	return strings.Join(lines, "\n")
}

// CheckAliasCollisions returns one message per conflict: an alias owned by
// another existing term, or claimed by two terms of the batch.
func CheckAliasCollisions(existing, candidates []Term) []string {
	var out []string
	owner := map[string]string{}
	for _, e := range existing {
		for _, a := range e.Aliases {
			owner[strings.ToLower(a)] = strings.ToLower(e.Name)
		}
	}
	type claim struct {
		key  string
		line int
	}
	batch := map[string]claim{}
	for _, c := range candidates {
		ck := strings.ToLower(c.Name)
		prefix := ""
		if c.Line > 0 {
			prefix = fmt.Sprintf("line %d: ", c.Line)
		}
		for _, a := range c.Aliases {
			al := strings.ToLower(a)
			if o, ok := owner[al]; ok && o != ck {
				ownerName := o
				for _, e := range existing {
					if strings.ToLower(e.Name) == o {
						ownerName = e.Name
						break
					}
				}
				out = append(out, fmt.Sprintf("%salias '%s' on '%s' collides with existing term '%s'", prefix, a, c.Name, ownerName))
				continue
			}
			if p, ok := batch[al]; ok && p.key != ck {
				priorName := p.key
				for _, x := range candidates {
					if strings.ToLower(x.Name) == p.key {
						priorName = x.Name
						break
					}
				}
				if p.line > 0 {
					out = append(out, fmt.Sprintf("%salias '%s' on '%s' collides with '%s' on line %d (intra-batch)", prefix, a, c.Name, priorName, p.line))
				} else {
					out = append(out, fmt.Sprintf("%salias '%s' on '%s' collides with '%s' (intra-batch)", prefix, a, c.Name, priorName))
				}
				continue
			}
			batch[al] = claim{ck, c.Line}
		}
	}
	return out
}

func union(a, b []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, x := range append(append([]string{}, a...), b...) {
		if l := strings.ToLower(x); !seen[l] {
			seen[l] = true
			out = append(out, x)
		}
	}
	return out
}

// mergeTerms folds candidates into byKey (lowercased name → entry).
func mergeTerms(byKey map[string]Term, candidates []Term, today string, touch, notsProvided bool) {
	for _, c := range candidates {
		k := strings.ToLower(c.Name)
		prev, ok := byKey[k]
		if !ok {
			byKey[k] = Term{Name: c.Name, Definition: c.Definition, Aliases: append([]string{}, c.Aliases...), Nots: append([]string{}, c.Nots...), Date: today}
			continue
		}
		nots := append([]string{}, prev.Nots...)
		if notsProvided {
			nots = union(prev.Nots, c.Nots)
		}
		date := prev.Date
		if touch || date == "" {
			date = today
		}
		byKey[k] = Term{Name: prev.Name, Definition: c.Definition, Aliases: union(prev.Aliases, c.Aliases), Nots: nots, Date: date}
	}
}

var noTermsNote = regexp.MustCompile(`(?m)^_\(no terms yet[^)]*\)_\s*$\n?`)

// renderGlossaryBody renders entries sorted by lowercased name, keeping the
// leading prose and dropping the empty-glossary placeholder.
func renderGlossaryBody(byKey map[string]Term, leading, today string) string {
	keys := make([]string, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = buildEntry(byKey[k], today)
	}
	rendered := strings.Join(parts, "\n\n")
	clean := strings.TrimRight(noTermsNote.ReplaceAllString(leading, ""), "\n")
	if strings.TrimSpace(clean) != "" {
		return "\n" + strings.TrimSpace(clean) + "\n\n" + rendered + "\n"
	}
	return "\n" + rendered + "\n"
}

// glossaryTarget loads scope's KNOWLEDGE.md and the span of its Glossary body.
func (s Store) glossaryTarget(scope string) (path, text string, start, end int, err error) {
	path, err = s.KnowledgePath(scope)
	if err != nil {
		return
	}
	raw, rerr := readTextBytes(path)
	if rerr != nil {
		if os.IsNotExist(rerr) {
			err = notFound("%s missing — run rota init first", path)
		} else {
			err = rerr
		}
		return
	}
	text = string(raw)
	var ok bool
	if start, end, ok = section.Find(text, GlossaryTopic); !ok {
		err = notFound("## Glossary topic missing from %s — run rota init or add the heading manually", path)
	}
	return
}

// GlossaryWrite adds or updates one term. An existing term (matched
// case-insensitively) keeps its name and date unless touch, replaces its
// definition, and unions aliases and, when notsProvided, the Not list.
// Entries are rewritten sorted by name. It regenerates the scope's knowledge
// block afterwards.
func (s Store) GlossaryWrite(scope string, t Term, touch, notsProvided bool) (name string, changed bool, err error) {
	path, text, st, en, err := s.glossaryTarget(scope)
	if err != nil {
		return "", false, err
	}
	entries, leading := ParseGlossary(text[st:en])
	if c := CheckAliasCollisions(entries, []Term{t}); len(c) > 0 {
		return "", false, fmt.Errorf("%w: %s", ErrAliasCollision, aliasMessage(c[0]))
	}
	today := Today()
	byKey := map[string]Term{}
	for _, e := range entries {
		byKey[strings.ToLower(e.Name)] = e
	}
	mergeTerms(byKey, []Term{t}, today, touch, notsProvided)
	next := section.Replace(text, GlossaryTopic, renderGlossaryBody(byKey, leading, today))
	if next != text {
		if err := fsio.WriteFileAtomic(path, []byte(next)); err != nil {
			return "", false, err
		}
	}
	if _, err := s.RegenerateBlock("knowledge", scope); err != nil {
		return "", false, err
	}
	return byKey[strings.ToLower(t.Name)].Name, next != text, nil
}

var aliasMsg = regexp.MustCompile(`alias '([^']+)' on '[^']+' collides with existing term '([^']+)'`)

// aliasMessage words a single-write conflict as the old helper did.
func aliasMessage(conflict string) string {
	if m := aliasMsg.FindStringSubmatch(conflict); m != nil {
		return fmt.Sprintf("alias '%s' is already an alias of term '%s' — refuse to write", m[1], m[2])
	}
	return conflict
}

// ParseManifest reads import lines "term<TAB>def<TAB>aliases<TAB>nots"; blank
// and "#" lines are skipped. A missing term or definition is ErrManifest, a
// repeated term is ErrAliasCollision (the batch is refused).
func ParseManifest(text string) ([]Term, error) {
	var out []Term
	seen := map[string]int{}
	for i, raw := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		lineno := i + 1
		line := strings.TrimRight(raw, "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimLeft(line, " \t"), "#") {
			continue
		}
		cells := strings.Split(line, "\t")
		for len(cells) < 4 {
			cells = append(cells, "")
		}
		term, def := strings.TrimSpace(cells[0]), strings.TrimSpace(cells[1])
		if term == "" || def == "" {
			return nil, fmt.Errorf("%w: manifest line %d: term and def are required (got term='%s', def='%s')", ErrManifest, lineno, term, def)
		}
		k := strings.ToLower(term)
		if first, dup := seen[k]; dup {
			return nil, fmt.Errorf("%w: manifest line %d: term '%s' appears earlier on line %d — refusing batch", ErrAliasCollision, lineno, term, first)
		}
		seen[k] = lineno
		out = append(out, Term{Name: term, Definition: def, Aliases: SplitCSV(cells[2]), Nots: SplitCSV(cells[3]), Line: lineno})
	}
	return out, nil
}

// GlossaryImport adds every term of a manifest atomically: any conflict
// refuses the whole batch and writes nothing. It returns the term names.
func (s Store) GlossaryImport(scope, manifest string, touch bool) (terms []string, changed bool, err error) {
	path, text, st, en, err := s.glossaryTarget(scope)
	if err != nil {
		return nil, false, err
	}
	cands, err := ParseManifest(manifest)
	if err != nil {
		return nil, false, err
	}
	if len(cands) == 0 {
		// Nothing to import; the old helper still refreshed the block.
		_, err := s.RegenerateBlock("knowledge", scope)
		return nil, false, err
	}
	entries, leading := ParseGlossary(text[st:en])
	if c := CheckAliasCollisions(entries, cands); len(c) > 0 {
		return nil, false, fmt.Errorf("%w: batch alias collisions — refusing entire batch:\n  %s", ErrAliasCollision, strings.Join(c, "\n  "))
	}
	today := Today()
	byKey := map[string]Term{}
	for _, e := range entries {
		byKey[strings.ToLower(e.Name)] = e
	}
	mergeTerms(byKey, cands, today, touch, true)
	next := section.Replace(text, GlossaryTopic, renderGlossaryBody(byKey, leading, today))
	if err := fsio.WriteFileAtomic(path, []byte(next)); err != nil {
		return nil, false, err
	}
	for _, c := range cands {
		terms = append(terms, c.Name)
	}
	if _, err := s.RegenerateBlock("knowledge", scope); err != nil {
		return nil, false, err
	}
	return terms, next != text, nil
}

// GlossaryRead prints the entries named by terms from the Glossary of the
// umbrella and, for a sub-repo scope, of the sub-repo, each under a
// "> from:" line. A term with no match prints nothing.
func (s Store) GlossaryRead(scope string, terms []string) (string, []string, error) {
	wanted := map[string]bool{}
	for _, t := range terms {
		wanted[strings.ToLower(strings.TrimSpace(t))] = true
	}
	type src struct{ path, label string }
	up, err := s.KnowledgePath(Umbrella)
	if err != nil {
		return "", nil, err
	}
	srcs := []src{{up, ".rota/KNOWLEDGE.md"}}
	if scope != "" && scope != Umbrella {
		sp, err := s.KnowledgePath(scope)
		if err != nil {
			return "", nil, err
		}
		srcs = append(srcs, src{sp, ".rota/knowledge/" + scope + "/KNOWLEDGE.md"})
	}
	var b strings.Builder
	printed := false
	matched := map[string]bool{}
	for _, sc := range srcs {
		raw, err := readTextBytes(sc.path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", nil, err
		}
		for _, e := range glossaryBlocks(string(raw)) {
			if !wanted[strings.ToLower(e.name)] {
				continue
			}
			matched[strings.ToLower(e.name)] = true
			if printed {
				b.WriteString("\n")
			}
			b.WriteString("> from: " + sc.label + " (## Glossary)\n")
			b.WriteString(e.block + "\n")
			printed = true
		}
	}
	var missing []string
	seen := map[string]bool{}
	for _, t := range terms {
		if k := strings.ToLower(strings.TrimSpace(t)); k != "" && !matched[k] && !seen[k] {
			seen[k] = true
			missing = append(missing, t)
		}
	}
	return b.String(), missing, nil
}

type glossaryBlock struct{ name, block string }

var blockHead = regexp.MustCompile(`^- \*\*([^*]+)\*\*`)

// glossaryBlocks splits the Glossary body into (name, raw block) pairs.
func glossaryBlocks(text string) []glossaryBlock {
	body := section.Body(text, GlossaryTopic)
	lines := splitKeep(body)
	var out []glossaryBlock
	for i := 0; i < len(lines); {
		m := blockHead.FindStringSubmatch(lines[i])
		if m == nil {
			i++
			continue
		}
		block := lines[i]
		i++
		for i < len(lines) && !bulletTop.MatchString(lines[i]) {
			block += lines[i]
			i++
		}
		out = append(out, glossaryBlock{strings.TrimSpace(m[1]), strings.TrimRight(block, "\n")})
	}
	return out
}

// TermEntry is the parsed body of a legacy CONTEXT.md "## <term>" section.
type TermEntry struct {
	Definition string
	Aliases    []string
	Nots       []string
}

// ParseTermEntry extracts the definition, aliases and Not list from the body
// of a CONTEXT.md term section: everything before the first marker line
// (**Aliases:**, **Not:**, or a <!-- comment -->) is the definition.
func ParseTermEntry(body string) TermEntry {
	e := TermEntry{Aliases: []string{}, Nots: []string{}}
	var def []string
	seenMarker := false
	for _, line := range strings.Split(body, "\n") {
		s := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(s, "**Aliases:**"):
			if v := strings.TrimSpace(s[len("**Aliases:**"):]); v != "" && v != "_none_" {
				e.Aliases = SplitCSV(v)
			}
			seenMarker = true
		case strings.HasPrefix(s, "**Not:**"):
			if v := strings.TrimSpace(s[len("**Not:**"):]); v != "" {
				e.Nots = SplitCSV(v)
			}
			seenMarker = true
		case strings.HasPrefix(s, "<!--") && strings.HasSuffix(s, "-->"):
			seenMarker = true
		case !seenMarker:
			def = append(def, line)
		}
	}
	e.Definition = strings.TrimSpace(strings.Join(def, "\n"))
	return e
}

// TermEntryNamed is a TermEntry with the term it belongs to.
type TermEntryNamed struct {
	Name string
	TermEntry
}

// TopicEntries parses every "## <term>" section of a legacy CONTEXT.md.
func TopicEntries(content string) []TermEntryNamed {
	var out []TermEntryNamed
	for _, t := range section.Topics(content) {
		out = append(out, TermEntryNamed{t.Name, ParseTermEntry(t.Body)})
	}
	return out
}
