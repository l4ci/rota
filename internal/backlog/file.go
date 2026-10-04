package backlog

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/pystr"
	"github.com/l4ci/rota/internal/section"
)

// File is the backlog kept in .rota/BACKLOG.md, with finished items in
// .rota/ARCHIVE.md (FileBackend in hvlib_backend.py).
type File struct {
	Root string // project root, the directory that holds .rota/
}

// Name is "file".
func (f *File) Name() string { return "file" }

func (f *File) rota(parts ...string) string {
	return filepath.Join(append([]string{f.Root, ".rota"}, parts...)...)
}

// Corpus is BACKLOG.md with its trailing newlines trimmed, a newline, then
// ARCHIVE.md: the text an item ID is looked up in (load_backlog_corpus). A
// missing or unreadable file counts as empty.
func (f *File) Corpus() string {
	primary, _ := fsio.ReadText(f.rota("BACKLOG.md"))
	archive, _ := fsio.ReadText(f.rota("ARCHIVE.md"))
	return strings.TrimRight(primary, "\n") + "\n" + archive
}

// Get looks up an item by its exact ID ("B07"; B7 does not find B07), in the
// backlog or the archive. Fields come from the origin line; Closed, Reason and
// Note from the done line. Title is the full bullet title, as in issue mode;
// the old helpers' title cut at the first "." is FindOrigin's, for the verbs
// that must print it.
func (f *File) Get(ref string) (*Item, error) {
	it, ok := itemFromCorpus(f.Corpus(), ref)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, ref)
	}
	return it, nil
}

// itemFromCorpus builds the Item for ref from the BACKLOG+ARCHIVE text; Get
// and List share it.
func itemFromCorpus(corpus, ref string) (*Item, bool) {
	line, title, ok := FindOrigin(corpus, ref)
	if !ok {
		return nil, false
	}
	it := &Item{ID: ref, Fields: ParseFields(line), Line: line, Title: title}
	if r, _ := utf8.DecodeRuneInString(ref); strings.ContainsRune(ItemLetters, r) {
		it.Type = string(r)
	}
	if b, ok := ParseOpen("- " + line); ok {
		it.Tag, it.Title = b.Tag, b.Title
	}
	// The closure reason lives on the done marker, which FindOrigin strips.
	doneLine := regexp.MustCompile(`(?m)^- ~~\*\*\[` + regexp.QuoteMeta(ref) + `\].*$`).FindString(corpus)
	if d, ok := ParseDone(doneLine); ok {
		it.Closed, it.Reason, it.Note, it.ClosedAt = true, d.Reason, d.Note, d.Date
	}
	return it, true
}

// List returns the open items in BACKLOG.md order, then, with includeClosed,
// the done lines of its ## Completed section and of ARCHIVE.md, newest first.
// Both files append, so a later line is newer: the order is by close date,
// and completions that share a date list the later line first, Completed
// before ARCHIVE.md. An ID that occurs more than once is listed once, at its
// newest position. A bullet whose origin line Get cannot find (an indented
// bullet) is left out, so that List()[i] always equals Get(List()[i].ID). A
// missing BACKLOG.md wraps ErrNotFound; a missing ARCHIVE.md is empty.
func (f *File) List(includeClosed bool) ([]Item, error) {
	md, err := f.Markdown(0)
	if err != nil {
		return nil, err
	}
	archive, _ := fsio.ReadText(f.rota("ARCHIVE.md"))
	corpus := strings.TrimRight(md, "\n") + "\n" + archive

	var out []Item
	seen := map[string]bool{}
	add := func(ids []string) {
		for _, id := range ids {
			if seen[id] {
				continue
			}
			seen[id] = true
			if it, ok := itemFromCorpus(corpus, id); ok {
				out = append(out, *it)
			}
		}
	}
	var open []string
	for _, e := range OpenBullets(md) {
		open = append(open, e.ID)
	}
	add(open)
	if includeClosed {
		// Oldest to newest, then reversed: the archive holds what left
		// Completed earlier.
		done := append(doneIDs(archive), doneIDs(sectionBody(md, "Completed"))...)
		slices.Reverse(done)
		n := len(out)
		add(done)
		slices.SortStableFunc(out[n:], func(a, b Item) int { return strings.Compare(b.ClosedAt, a.ClosedAt) })
	}
	return out, nil
}

func sectionBody(content, name string) string {
	s, e, ok := section.Find(content, name)
	if !ok {
		return ""
	}
	return content[s:e]
}

// doneIDs is the IDs of the done lines in text, in order.
func doneIDs(text string) []string {
	var ids []string
	for _, raw := range pystr.Splitlines(text) {
		if d, ok := ParseDone(pystr.Strip(raw)); ok && d.ID != "" {
			ids = append(ids, d.ID)
		}
	}
	return ids
}

// Markdown returns BACKLOG.md verbatim; closedLimit is ignored.
func (f *File) Markdown(int) (string, error) {
	text, err := fsio.ReadText(f.rota("BACKLOG.md"))
	if errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("%w: .rota/BACKLOG.md does not exist", ErrNotFound)
	}
	return text, err
}

// Detail returns the content of the item's detail file,
// .rota/<kind>/<ID>.md. ok is false when the ID has no type, the file is
// missing or it cannot be read.
func (f *File) Detail(ref string) (string, bool, error) {
	if ref == "" {
		return "", false, nil
	}
	r, _ := utf8.DecodeRuneInString(ref)
	t, ok := TypeByLetter(strings.ToUpper(string(r)))
	if !ok || t.Kind == "" {
		return "", false, nil
	}
	text, err := fsio.ReadText(f.rota(t.Kind, ref+".md"))
	if err != nil {
		return "", false, nil
	}
	return text, true, nil
}

var kindPrefix = map[string]string{"bugs": "B", "features": "F", "tasks": "T", "milestones": "M"}

// NextID bumps the counter for kind (bugs, features, tasks or milestones) in
// .rota/counters.json and returns the new zero-padded ID such as "B07". The
// counter never lags the highest ID already in BACKLOG.md or ARCHIVE.md.
// Existing keys keep their position and a new key is appended, so the file
// matches what the Python helper writes.
func (f *File) NextID(kind string) (string, error) {
	prefix, ok := kindPrefix[kind]
	if !ok {
		return "", fmt.Errorf("unknown counter kind %q (want bugs|features|tasks|milestones)", kind)
	}
	pat := regexp.MustCompile(`\[` + prefix + `(\p{Nd}+)\]`)
	highest := 0
	for _, name := range []string{"BACKLOG.md", "ARCHIVE.md"} {
		text, err := fsio.ReadText(f.rota(name))
		if err != nil {
			continue
		}
		for _, m := range pat.FindAllStringSubmatch(text, -1) {
			n, err := atoi(m[1])
			if err != nil {
				return "", err
			}
			highest = max(highest, n)
		}
	}
	var next int
	err := fsio.UpdateJSON(f.rota("counters.json"), jsonx.NewObject(), func(v any) (any, error) {
		d, ok := v.(*jsonx.Object)
		if !ok {
			return nil, errors.New("counters.json is not a JSON object")
		}
		cur := 0
		if raw, ok := d.Get(kind); ok {
			num, _ := raw.(json.Number)
			n, err := strconv.Atoi(string(num))
			if err != nil {
				// Python's max() lets a fractional counter through when an ID
				// is higher; otherwise it writes the float and crashes. rota
				// refuses before writing.
				fl, ferr := strconv.ParseFloat(string(num), 64)
				if ferr != nil || fl >= float64(highest) {
					return nil, fmt.Errorf("counters.json: %s is not an integer", kind)
				}
				n = highest
			}
			cur = n
		}
		next = max(cur, highest) + 1
		d.Set(kind, json.Number(strconv.Itoa(next)))
		return d, nil
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s%02d", prefix, next), nil
}
