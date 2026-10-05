package knowledge

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/section"
)

// Sentinels for the failures the verbs map to exit codes.
var (
	// ErrNotFound: a topic, bullet or file named by the caller does not exist.
	ErrNotFound = errors.New("not found")
	// ErrExists: the target of a rename already exists.
	ErrExists = errors.New("already exists")
	// ErrAmbiguous: a fragment matches in more than one file.
	ErrAmbiguous = errors.New("ambiguous")
	// ErrMultiMatch: a replace fragment matches more than one bullet.
	ErrMultiMatch = errors.New("matches several bullets")
)

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func notFound(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrNotFound, fmt.Sprintf(format, a...))
}

var titleRe = regexp.MustCompile(`^- \*\*([^*]+)\*\*`)

// AddResult reports Add.
type AddResult struct{ Changed bool }

// Add prepends "- **title** — body <!-- date -->" under "## topic" in scope's
// KNOWLEDGE.md. A bullet whose title already exists (case-insensitive) is a
// no-op. A new bullet is also registered as provisional in the tier sidecar.
func (s Store) Add(scope, topic, title, body, date string) (AddResult, error) {
	target, err := s.KnowledgePath(scope)
	if err != nil {
		return AddResult{}, err
	}
	if date == "" {
		date = s.today()
	}
	body = strings.TrimRight(body, "\n")
	content, err := ReadFile(target)
	if err != nil {
		return AddResult{}, err
	}
	start, end, ok := section.Find(content, topic)
	if !ok {
		return AddResult{}, notFound("topic '%s' not found in %s", topic, target)
	}
	lower := strings.ToLower(title)
	for _, line := range section.Lines(content[start:end]) {
		if m := titleRe.FindStringSubmatch(strings.TrimSpace(line)); m != nil && strings.ToLower(m[1]) == lower {
			// Like the old helper, a repeat still registers the title in the
			// sidecar, so a bullet that predates tiering gets tracked.
			return AddResult{}, s.initTierUnlessGlossary(scope, topic, title)
		}
	}
	bullet := fmt.Sprintf("- **%s** — %s <!-- %s -->", title, body, date)
	rest := strings.TrimLeft(content[start:end], "\n")
	newBody := "\n" + bullet + "\n" + rest
	if !strings.HasSuffix(newBody, "\n") {
		newBody += "\n"
	}
	if err := writeText(target, section.Replace(content, topic, newBody), fsio.WriteFileAtomic); err != nil {
		return AddResult{}, err
	}
	return AddResult{Changed: true}, s.initTierUnlessGlossary(scope, topic, title)
}

func (s Store) initTierUnlessGlossary(scope, topic, title string) error {
	if topic == GlossaryTopic {
		return nil
	}
	return s.initTier(scope, topic, title)
}

// initTier registers a bullet as provisional. A failure here is not fatal to
// the caller: the bullet already landed, and the next read initializes lazily.
func (s Store) initTier(scope, topic, title string) error {
	p, err := s.TierPath(scope)
	if err != nil {
		return err
	}
	// The old helper swallowed every error from hv-knowledge-tier --init.
	_ = Update(p, func(sc *Sidecar) (bool, error) { return sc.Init(topic, title, s.today()), nil })
	return nil
}

// Amend appends text after the bullet under "## topic" that contains
// fragment (case-sensitive). With explicit set, only scope's file is searched;
// otherwise the umbrella file and, for a sub-repo scope, that sub-repo's file
// are searched and a match in both is ErrAmbiguous. It returns the file
// that was amended.
func (s Store) Amend(scope string, explicit bool, topic, fragment, text string) (string, bool, error) {
	var candidates []string
	if explicit {
		p, err := s.KnowledgePath(scope)
		if err != nil {
			return "", false, err
		}
		candidates = []string{p}
	} else {
		u, _ := s.KnowledgePath(Umbrella)
		candidates = []string{u}
		if scope != Umbrella && scope != "" {
			if p, err := s.KnowledgePath(scope); err == nil {
				candidates = append(candidates, p)
			}
		}
	}

	type hit struct{ file, content, line string }
	var hits []hit
	for _, f := range candidates {
		content, err := ReadFile(f)
		if err != nil {
			return "", false, err
		}
		st, en, ok := section.Find(content, topic)
		if !ok {
			if explicit {
				return "", false, notFound("topic '%s' not found in %s", topic, f)
			}
			continue
		}
		for _, line := range section.Lines(content[st:en]) {
			stripped := strings.TrimSpace(line)
			if strings.HasPrefix(stripped, "- ") && strings.Contains(stripped, fragment) {
				hits = append(hits, hit{f, content, stripped})
				break
			}
		}
	}
	switch {
	case len(hits) == 0:
		return "", false, notFound("no bullet in '%s' contains fragment '%s' (searched: %s)", topic, fragment, strings.Join(candidates, ", "))
	case len(hits) > 1:
		files := make([]string, len(hits))
		for i, h := range hits {
			files[i] = h.file
		}
		return "", false, fmt.Errorf("%w: fragment '%s' under topic '%s' matches in multiple files: %s; pass --repo to disambiguate", ErrAmbiguous, fragment, topic, strings.Join(files, ", "))
	}
	h := hits[0]
	st, en, _ := section.Find(h.content, topic)
	body := h.content[st:en]
	idx := strings.Index(body, h.line)
	if idx < 0 {
		return "", false, fmt.Errorf("internal: could not re-locate target line in section body")
	}
	body = body[:idx] + h.line + " " + text + body[idx+len(h.line):]
	next := h.content[:st] + body + h.content[en:]
	if next == h.content {
		return h.file, false, nil
	}
	return h.file, true, writeText(h.file, next, fsio.WriteFileAtomic)
}

// RenameResult reports RenameTopic.
type RenameResult struct {
	Mode    string // "topic" or "bullet"
	Changed bool
}

var (
	bulletStart  = regexp.MustCompile(`(?m)^- \*\*(.+?)\*\*`)
	siblingStart = regexp.MustCompile(`(?m)^- \*\*`)
)

// RenameTopic renames the "## from" heading to "## to" and re-keys the
// sidecar entries (topic mode, title empty), or moves the single bullet
// titled title from one topic to the other and re-keys its entry (bullet
// mode; "## to" must exist). from == to is a no-op.
func (s Store) RenameTopic(scope, from, to, title string) (RenameResult, error) {
	mode := "topic"
	if title != "" {
		mode = "bullet"
	}
	if from == to {
		return RenameResult{Mode: mode}, nil
	}
	km, err := s.KnowledgePath(scope)
	if err != nil {
		return RenameResult{}, err
	}
	sidecar, err := s.TierPath(scope)
	if err != nil {
		return RenameResult{}, err
	}
	content, err := ReadFile(km)
	if err != nil {
		return RenameResult{}, err
	}
	if content == "" {
		return RenameResult{}, notFound("%s not found or empty", km)
	}
	srcStart, srcEnd, ok := section.Find(content, from)
	if !ok {
		return RenameResult{}, notFound("source topic '%s' not found in %s", from, km)
	}
	dstStart, dstEnd, dstOK := section.Find(content, to)

	rekey := func(fn func(*Sidecar) bool) error {
		if !fileExists(sidecar) {
			return nil
		}
		return Update(sidecar, func(sc *Sidecar) (bool, error) { return fn(sc), nil })
	}

	if title == "" {
		if dstOK {
			return RenameResult{}, fmt.Errorf("%w: target topic '%s' already exists in %s — use a bullet title to move bullets individually", ErrExists, to, km)
		}
		re := regexp.MustCompile(`(?m)^## ` + regexp.QuoteMeta(from) + `\s*$`)
		loc := re.FindStringIndex(content)
		if loc == nil {
			return RenameResult{}, fmt.Errorf("failed to rewrite '## %s' heading", from)
		}
		next := content[:loc[0]] + "## " + to + content[loc[1]:]
		if err := writeText(km, next, fsio.WriteFileAtomic); err != nil {
			return RenameResult{}, err
		}
		if err := rekey(func(sc *Sidecar) bool { return sc.RekeyTopic(from, to) > 0 }); err != nil {
			return RenameResult{}, err
		}
		return RenameResult{Mode: mode, Changed: true}, nil
	}

	if !dstOK {
		return RenameResult{}, notFound("target topic '%s' not found in %s — append `## %s` heading before moving bullets", to, km, to)
	}
	srcBody := content[srcStart:srcEnd]
	var tm []int
	for _, m := range bulletStart.FindAllStringSubmatchIndex(srcBody, -1) {
		if srcBody[m[2]:m[3]] == title {
			tm = m
			break
		}
	}
	if tm == nil {
		return RenameResult{}, notFound("bullet titled '%s' not found under '## %s' in %s", title, from, km)
	}
	bStart := tm[0]
	bEnd := len(srcBody)
	if n := siblingStart.FindStringIndex(srcBody[tm[1]:]); n != nil {
		bEnd = tm[1] + n[0]
	}
	block := strings.TrimRight(srcBody[bStart:bEnd], "\n")
	newSrc := srcBody[:bStart] + srcBody[bEnd:]

	dstBody := content[dstStart:dstEnd]
	rest := strings.TrimLeft(dstBody, "\n")
	lead := dstBody[:len(dstBody)-len(rest)]
	sep := ""
	if rest != "" {
		sep = "\n"
	}
	newDst := lead + block + "\n" + sep + rest
	if !strings.HasSuffix(newDst, "\n") {
		newDst += "\n"
	}
	var next string
	if srcStart < dstStart {
		next = content[:srcStart] + newSrc + content[srcEnd:dstStart] + newDst + content[dstEnd:]
	} else {
		next = content[:dstStart] + newDst + content[dstEnd:srcStart] + newSrc + content[srcEnd:]
	}
	if err := writeText(km, next, fsio.WriteFileAtomic); err != nil {
		return RenameResult{}, err
	}
	if err := rekey(func(sc *Sidecar) bool { return sc.Rekey(from, title, to) }); err != nil {
		return RenameResult{}, err
	}
	return RenameResult{Mode: mode, Changed: true}, nil
}

// ReplaceResult reports Replace.
type ReplaceResult struct {
	File    string
	Changed bool
}

// Replace swaps every occurrence of old for new inside the one bullet under
// "## topic" in scope's KNOWLEDGE.md that contains old (case-sensitive). A
// bullet runs from its "- " line to the next sibling bullet, so wrapped
// bullets match as a whole. It refuses with ErrMultiMatch when old sits in more
// than one bullet and returns ErrNotFound when it sits in none. When the edit
// changes the bullet's bold title, the tier entry is re-keyed to the new one.
// old == new is a no-op.
func (s Store) Replace(scope, topic, old, new string) (ReplaceResult, error) {
	km, err := s.KnowledgePath(scope)
	if err != nil {
		return ReplaceResult{}, err
	}
	content, err := ReadFile(km)
	if err != nil {
		return ReplaceResult{}, err
	}
	st, en, ok := section.Find(content, topic)
	if !ok {
		return ReplaceResult{}, notFound("topic '%s' not found in %s", topic, km)
	}
	res := ReplaceResult{File: km}
	if old == new {
		return res, nil
	}
	body := content[st:en]
	starts := siblingStart.FindAllStringIndex(body, -1)
	type span struct{ a, b int }
	var hits []span
	for i, m := range starts {
		b := len(body)
		if i+1 < len(starts) {
			b = starts[i+1][0]
		}
		if strings.Contains(body[m[0]:b], old) {
			hits = append(hits, span{m[0], b})
		}
	}
	switch {
	case len(hits) == 0:
		return res, notFound("no bullet under '%s' contains '%s' in %s", topic, old, km)
	case len(hits) > 1:
		return res, fmt.Errorf("%w: '%s' matches %d bullets under '%s'; use a longer fragment", ErrMultiMatch, old, len(hits), topic)
	}
	h := hits[0]
	block := body[h.a:h.b]
	edited := strings.ReplaceAll(block, old, new)
	next := content[:st] + body[:h.a] + edited + body[h.b:] + content[en:]
	if err := writeText(km, next, fsio.WriteFileAtomic); err != nil {
		return res, err
	}
	res.Changed = true

	oldTitle, newTitle := bulletTitle(block), bulletTitle(edited)
	if oldTitle != "" && newTitle != "" && oldTitle != newTitle {
		sidecar, err := s.TierPath(scope)
		if err != nil {
			return res, err
		}
		if fileExists(sidecar) {
			err = Update(sidecar, func(sc *Sidecar) (bool, error) { return sc.Retitle(topic, oldTitle, newTitle), nil })
		}
		return res, err
	}
	return res, nil
}

// bulletTitle is the bold title opening a bullet block, or "".
func bulletTitle(block string) string {
	if m := bulletStart.FindStringSubmatch(block); m != nil {
		return m[1]
	}
	return ""
}
