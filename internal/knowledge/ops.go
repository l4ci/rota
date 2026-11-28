package knowledge

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/l4ci/rota/internal/bulletfmt"
	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/section"
)

// Sentinels for the failures the verbs map to exit codes.
var (
	// ErrNotFound: a topic, bullet or file named by the caller does not exist.
	ErrNotFound = bulletfmt.ErrNotFound
	// ErrExists: the target of a rename, or a new bullet title, is already taken.
	ErrExists = bulletfmt.ErrExists
	// ErrAmbiguous: a fragment matches in more than one file.
	ErrAmbiguous = errors.New("ambiguous")
	// ErrMultiMatch: a fragment, title or legacy tier key can match multiple bullets.
	ErrMultiMatch = bulletfmt.ErrMultiMatch
	// ErrInvalid: a title or date no bullet can carry; shared with the external store.
	ErrInvalid = bulletfmt.ErrInvalid
)

// lockFiles runs fn holding the sidecar lock of every path, taken in the
// order given (callers pass a fixed order: umbrella before sub-repo). A verb
// reads, edits and rewrites KNOWLEDGE.md, so without the lock N concurrent
// verbs each write back their own stale read and the last one wins. The tier
// sidecar's own lock is always taken inside this one, never the reverse.
func lockFiles(paths []string, fn func() error) error {
	if len(paths) == 0 {
		return fn()
	}
	return fsio.Locked(paths[0], fsio.LockTimeout, func() error {
		return lockFiles(paths[1:], fn)
	})
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func notFound(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrNotFound, fmt.Sprintf(format, a...))
}

// AddResult reports Add.
type AddResult struct{ Changed bool }

// shaRe is a commit sha as stored beside a bullet's date.
var shaRe = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

// NormalizeSha lowercases sha and checks it is 7 to 40 hex digits; "" is valid
// and means the bullet cites no commit.
func NormalizeSha(sha string) (string, error) {
	sha = strings.ToLower(strings.TrimSpace(sha))
	if sha != "" && !shaRe.MatchString(sha) {
		return "", fmt.Errorf("invalid sha %q: want 7 to 40 hex digits", sha)
	}
	return sha, nil
}

// Add prepends "- **title** — body <!-- date -->" (or "<!-- date sha:sha -->"
// when sha is not empty) under "## topic" in scope's
// KNOWLEDGE.md. A bullet whose title already exists (case-insensitive) is a
// no-op. A new bullet is also registered as provisional in the tier sidecar.
func (s Store) Add(scope, topic, title, body, date, sha string, paths ...string) (AddResult, error) {
	if err := bulletfmt.CheckTitle(title); err != nil {
		return AddResult{}, err
	}
	title = strings.TrimSpace(title)
	if err := bulletfmt.CheckDate(date); err != nil {
		return AddResult{}, err
	}
	if err := bulletfmt.CheckLine("body", strings.TrimRight(body, "\r\n")); err != nil {
		return AddResult{}, err
	}
	field, err := PathField(paths)
	if err != nil {
		return AddResult{}, err
	}
	if field != "" {
		body = strings.TrimRight(body, "\n") + " " + field
	}
	target, err := s.KnowledgePath(scope)
	if err != nil {
		return AddResult{}, err
	}
	var res AddResult
	err = lockFiles([]string{target}, func() (err error) {
		res, err = s.addLocked(scope, target, topic, title, body, date, sha)
		return err
	})
	return res, err
}

func (s Store) addLocked(scope, target, topic, title, body, date, sha string) (AddResult, error) {
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
	if stored, ok := bulletfmt.FindTitle(content[start:end], title); ok {
		// A repeat still registers the stored title in the
		// sidecar, so a bullet that predates tiering gets tracked.
		return AddResult{}, s.initTierUnlessGlossary(scope, topic, stored)
	}
	if tiered(topic) {
		if err := s.preflightTier(scope, func(sc *Sidecar) error { return sc.checkIdentity(topic, title) }); err != nil {
			return AddResult{}, err
		}
	}
	bullet := renderBullet(title, body, date, sha)
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
	if !tiered(topic) {
		return nil
	}
	return s.initTier(scope, topic, title)
}

// initTier registers a bullet as provisional.
func (s Store) initTier(scope, topic, title string) error {
	p, err := s.TierPath(scope)
	if err != nil {
		return err
	}
	return Update(p, func(sc *Sidecar) (bool, error) { return sc.Init(topic, title, s.today()) })
}

// preflightTier checks an operation against the sidecar without writing it.
func (s Store) preflightTier(scope string, check func(*Sidecar) error) error {
	p, err := s.TierPath(scope)
	if err != nil {
		return err
	}
	sc, err := LoadSidecar(p)
	if err != nil {
		return err
	}
	if sc.saveErr != nil {
		return sc.saveErr
	}
	return check(sc)
}

// Amend appends text to the body of the bullet under "## topic" that contains
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

	if err := bulletfmt.CheckLine("text", strings.TrimRight(text, "\r\n")); err != nil {
		return "", false, err
	}
	var (
		file    string
		changed bool
	)
	err := lockFiles(candidates, func() (err error) {
		file, changed, err = s.amendLocked(candidates, explicit, topic, fragment, text)
		return err
	})
	return file, changed, err
}

func (s Store) amendLocked(candidates []string, explicit bool, topic, fragment, text string) (string, bool, error) {
	type hit struct {
		file, content string
		line          int // index into the topic body's lines
	}
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
		switch in := bulletfmt.SelectFragment(strings.Split(content[st:en], "\n"), fragment); len(in) {
		case 0:
		case 1:
			hits = append(hits, hit{f, content, in[0]})
		default:
			return "", false, fmt.Errorf("%w: fragment '%s' matches %d bullets under topic '%s' in %s; use a longer fragment", ErrMultiMatch, fragment, len(in), topic, f)
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
	lines := strings.Split(h.content[st:en], "\n")
	lines[h.line] = appendToBullet(lines[h.line], text)
	body := strings.Join(lines, "\n")
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

// selectBullet is the one title lookup RenameTopic's bullet mode and Remove
// share: bulletfmt's title grammar and bulletfmt.ResolveTitle's case rule. A
// miss is ErrNotFound and a title two bullets share is ErrMultiMatch naming
// every hit (both from bulletfmt.ResolveTitle), so the verbs cannot disagree
// on what a title is. topic is the heading the bullets sit under, km the file,
// for the messages.
func selectBullet(body, title, topic, km string) (bulletfmt.Bullet, error) {
	all := bulletfmt.Bullets(body)
	titles := make([]string, len(all))
	for i, b := range all {
		titles[i] = b.Title
	}
	i, err := bulletfmt.ResolveTitle(titles, title)
	if err != nil {
		return bulletfmt.Bullet{}, fmt.Errorf("%w (under '## %s' in %s)", err, topic, km)
	}
	return all[i], nil
}

// storedTitle resolves a typed bullet title to the title the file stores, so a
// case-variant reaches the sidecar record. A file or section it cannot read
// leaves the typed title for renameLocked to report.
func storedTitle(km, topic, title string) (string, error) {
	content, err := ReadFile(km)
	if err != nil {
		return "", err
	}
	st, en, ok := section.Find(content, topic)
	if !ok {
		return title, nil
	}
	hit, err := selectBullet(content[st:en], title, topic, km)
	if err != nil {
		return "", err
	}
	return hit.Title, nil
}

// checkMoveTarget refuses a bullet move into a topic that already holds title.
// A missing file or topic is left for renameLocked to report.
func checkMoveTarget(km, topic, title string) error {
	content, err := ReadFile(km)
	if err != nil {
		return err
	}
	st, en, ok := section.Find(content, topic)
	if !ok {
		return nil
	}
	if err := bulletfmt.CheckFree(strings.Split(content[st:en], "\n"), -1, title); err != nil {
		return fmt.Errorf("%w (under '## %s' in %s)", err, topic, km)
	}
	return nil
}

// RenameTopic renames the "## from" heading to "## to" and re-keys the
// sidecar entries (topic mode, title empty), or moves the single bullet
// titled title from one topic to the other and re-keys its entry (bullet
// mode; "## to" must exist and hold no bullet of that title, else
// ErrExists). from == to is a no-op.
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
	var res RenameResult
	err = lockFiles([]string{km}, func() (err error) {
		// The sidecar is keyed by the stored title, not the typed one.
		if mode == "bullet" {
			if title, err = storedTitle(km, from, title); err != nil {
				return err
			}
		}
		// Before the preflight and the hit fold: a refusal writes nothing.
		if mode == "bullet" {
			if err := checkMoveTarget(km, to, title); err != nil {
				return err
			}
		}
		if err := s.preflightTier(scope, func(sc *Sidecar) error {
			if title == "" {
				_, err := sc.RekeyTopic(from, to)
				return err
			}
			_, err := sc.Rekey(from, title, to)
			return err
		}); err != nil {
			return err
		}
		// Buffered hits are keyed by the old names: fold them under the lock,
		// right before the re-key, so none lands in the gap between the two.
		if _, err := s.FoldHits(scope); err != nil {
			return err
		}
		res, err = s.renameLocked(km, sidecar, from, to, title, mode)
		return err
	})
	return res, err
}

func (s Store) renameLocked(km, sidecar, from, to, title, mode string) (RenameResult, error) {
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

	rekey := func(fn func(*Sidecar) (bool, error)) error {
		if !fileExists(sidecar) {
			return nil
		}
		return Update(sidecar, fn)
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
		if err := rekey(func(sc *Sidecar) (bool, error) { n, err := sc.RekeyTopic(from, to); return n > 0, err }); err != nil {
			return RenameResult{}, err
		}
		return RenameResult{Mode: mode, Changed: true}, nil
	}

	if !dstOK {
		return RenameResult{}, notFound("target topic '%s' not found in %s — append `## %s` heading before moving bullets", to, km, to)
	}
	srcBody := content[srcStart:srcEnd]
	hit, err := selectBullet(srcBody, title, from, km)
	if err != nil {
		return RenameResult{}, err
	}
	bStart := hit.Start
	bEnd := bulletfmt.EndOffset(srcBody, bStart)
	block := strings.TrimRight(srcBody[bStart:bEnd], "\n")
	// The collapse of blank lines around the removed bullet is bulletfmt's, as
	// in Remove; the target section is then read from the shortened text.
	next := bulletfmt.DeleteSpan(content, srcStart+bStart, srcStart+bEnd)
	dstStart, dstEnd, _ = section.Find(next, to)
	dstBody := next[dstStart:dstEnd]
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
	next = next[:dstStart] + newDst + next[dstEnd:]
	if err := writeText(km, next, fsio.WriteFileAtomic); err != nil {
		return RenameResult{}, err
	}
	if err := rekey(func(sc *Sidecar) (bool, error) { return sc.Rekey(from, hit.Title, to) }); err != nil {
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
// bullet is its "- " line plus indented continuations (see bulletfmt.End), so
// wrapped bullets match as a whole and prose after them is never touched. It refuses with ErrMultiMatch when old sits in more
// than one bullet and returns ErrNotFound when it sits in none. When the edit
// changes the bullet's bold title, the tier entry is re-keyed to the new one, or
// deleted when the edit removes the title; a title another bullet of the topic
// holds is ErrExists.
// old == new is a no-op.
func (s Store) Replace(scope, topic, old, new string) (ReplaceResult, error) {
	if err := bulletfmt.CheckLine("replacement", new); err != nil {
		return ReplaceResult{}, err
	}
	km, err := s.KnowledgePath(scope)
	if err != nil {
		return ReplaceResult{}, err
	}
	var res ReplaceResult
	err = lockFiles([]string{km}, func() (err error) {
		res, err = s.replaceLocked(scope, km, topic, old, new)
		return err
	})
	return res, err
}

func (s Store) replaceLocked(scope, km, topic, old, new string) (ReplaceResult, error) {
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
	lines := strings.Split(body, "\n")
	hits := bulletfmt.SelectFragment(lines, old)
	switch {
	case len(hits) == 0:
		return res, notFound("no bullet under '%s' contains '%s' in %s", topic, old, km)
	case len(hits) > 1:
		return res, fmt.Errorf("%w: '%s' matches %d bullets under '%s'; use a longer fragment", ErrMultiMatch, old, len(hits), topic)
	}
	h := hits[0]
	end := bulletfmt.End(lines, h)
	edited := bulletfmt.ReplaceIn(lines[h:end], old, new)
	if err := bulletfmt.CheckMarker(edited[0]); err != nil {
		return res, err
	}
	oldTitle, _ := bulletfmt.Title(lines[h])
	newTitle, _ := bulletfmt.Title(edited[0])
	// A case change in place introduces no title.
	if !strings.EqualFold(oldTitle, newTitle) {
		if err := bulletfmt.CheckFree(lines, h, newTitle); err != nil {
			return res, fmt.Errorf("%w (under '## %s' in %s)", err, topic, km)
		}
	}
	newBody := strings.Join(append(append(lines[:h:h], edited...), lines[end:]...), "\n")
	// What the edit does to the old title's tier record: it follows a new
	// title, and goes with a removed one unless a twin on disk still holds it.
	var retier func(*Sidecar) (bool, error)
	switch {
	case oldTitle == "" || oldTitle == newTitle:
	case newTitle != "":
		retier = func(sc *Sidecar) (bool, error) { return sc.Retitle(topic, oldTitle, newTitle) }
	case !slices.ContainsFunc(bulletfmt.Bullets(newBody), func(b bulletfmt.Bullet) bool { return b.Title == oldTitle }):
		retier = func(sc *Sidecar) (bool, error) { return sc.Delete(topic, oldTitle) }
	}
	if retier != nil {
		if err := s.preflightTier(scope, func(sc *Sidecar) error { _, err := retier(sc); return err }); err != nil {
			return res, err
		}
		// Buffered hits are keyed by the old title: fold them before the write.
		if _, err := s.FoldHits(scope); err != nil {
			return res, err
		}
	}
	next := content[:st] + newBody + content[en:]
	if err := writeText(km, next, fsio.WriteFileAtomic); err != nil {
		return res, err
	}
	res.Changed = true

	if retier == nil {
		return res, nil
	}
	sidecar, err := s.TierPath(scope)
	if err != nil {
		return res, err
	}
	if fileExists(sidecar) {
		err = Update(sidecar, retier)
	}
	return res, err
}

// RemoveResult reports Remove.
type RemoveResult struct{ File string }

// Remove deletes the bullet titled title under "## topic" in scope's
// KNOWLEDGE.md and its tier entry. A bullet is its "- " line plus indented
// continuations (see bulletfmt.End); the last bullet of a section keeps the blank
// line that separates it from the next heading. It returns ErrNotFound when the topic or
// the bullet is missing, leaving both files untouched.
func (s Store) Remove(scope, topic, title string) (RemoveResult, error) {
	km, err := s.KnowledgePath(scope)
	if err != nil {
		return RemoveResult{}, err
	}
	sidecar, err := s.TierPath(scope)
	if err != nil {
		return RemoveResult{}, err
	}
	res := RemoveResult{File: km}
	err = lockFiles([]string{km}, func() error {
		content, err := ReadFile(km)
		if err != nil {
			return err
		}
		st, en, ok := section.Find(content, topic)
		if !ok {
			return notFound("topic '%s' not found in %s", topic, km)
		}
		body := content[st:en]
		hit, err := selectBullet(body, title, topic, km)
		if err != nil {
			return err
		}
		// The tier entry is keyed by the stored title, not the typed one.
		title = hit.Title
		a := st + hit.Start
		if err := s.preflightTier(scope, func(sc *Sidecar) error { _, err := sc.Delete(topic, title); return err }); err != nil {
			return err
		}
		// Buffered hits are keyed by this bullet: fold them before the write
		// so a failed fold changes nothing and no later fold recreates the entry.
		if _, err := s.FoldHits(scope); err != nil {
			return err
		}
		next := bulletfmt.DeleteSpan(content, a, st+bulletfmt.EndOffset(body, hit.Start))
		if err := writeText(km, next, fsio.WriteFileAtomic); err != nil {
			return err
		}
		if !fileExists(sidecar) {
			return nil
		}
		return Update(sidecar, func(sc *Sidecar) (bool, error) { return sc.Delete(topic, title) })
	})
	return res, err
}
