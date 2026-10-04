package backlog

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/pystr"
	"github.com/l4ci/rota/internal/section"
)

// This file holds the file-backend maintenance verbs: archive, Since backfill
// and the refactor counters (hv-archive-old, hv-backfill-since,
// hv-refactor-age, hv-refactor-reset).

// ErrBadDate is wrapped by the error for a done line whose date is not a
// calendar date; the Python helper crashes on it before writing anything.
var ErrBadDate = errors.New("invalid date in ## Completed")

func (f *File) archivePath() string { return f.rota("ARCHIVE.md") }

const archiveHeader = "# Archive\n\nCompleted items older than the active window.\n\n"

// Archive moves the done lines of ## Completed that are older than days
// (done date before today minus days) to .rota/ARCHIVE.md and returns how many
// moved. A missing BACKLOG.md or ## Completed is no work. today is the local
// date, as date.today() in the helper.
//
// The rewritten section keeps the kept lines joined by "\n", with a leading
// "\n" added when missing and no trailing newline, which is how the helper
// leaves it. ARCHIVE.md gains the moved lines in order, after its own text with
// trailing whitespace trimmed, or after the default header when it is new.
func (f *File) Archive(days int, today time.Time) (moved int, err error) {
	path := f.backlogPath()
	cutoff := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -days)
	var old []string
	err = fsio.Locked(path, fsio.LockTimeout, func() error {
		content, err := fsio.ReadText(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		start, end, ok := section.Find(content, "Completed")
		if !ok {
			return nil
		}
		var kept []string
		for _, line := range pystr.Splitlines(content[start:end]) {
			if d, ok := ParseDone(line); ok {
				t, perr := time.Parse("2006-01-02", asciiDigits(d.Date))
				if perr != nil || t.Year() < 1 {
					return fmt.Errorf("%w: %s", ErrBadDate, d.Date)
				}
				if t.Before(cutoff) {
					old = append(old, line)
					continue
				}
			}
			kept = append(kept, line)
		}
		if len(old) == 0 {
			return nil
		}
		sec := strings.Join(kept, "\n")
		if !strings.HasPrefix(sec, "\n") {
			sec = "\n" + sec
		}
		return fsio.WriteFileAtomic(path, []byte(content[:start]+sec+content[end:]))
	})
	if err != nil || len(old) == 0 {
		return 0, err
	}
	ap := f.archivePath()
	err = fsio.Locked(ap, fsio.LockTimeout, func() error {
		existing, err := fsio.ReadText(ap)
		if errors.Is(err, os.ErrNotExist) {
			existing, err = archiveHeader, nil
		}
		if err != nil {
			return err
		}
		return fsio.WriteFileAtomic(ap, []byte(pystr.Rstrip(existing)+"\n"+strings.Join(old, "\n")+"\n"))
	})
	return len(old), err
}

// asciiDigits maps Unicode decimal digits to ASCII, as int() reads them.
func asciiDigits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if pystr.IsDigit(r) {
			b.WriteByte(byte('0' + pystr.DigitValue(r)))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Span is an open bullet line of a section and where it sits in the content.
type Span struct {
	Start, End int // byte offsets of the line, without its newline
	Line       string
	Bullet     Bullet
}

// openSpans finds the open bullets the helpers' regexp finds in body: lines
// that start, without indentation, "- **[ID]".
func openSpans(body string) []Span {
	var out []Span
	for pos := 0; pos <= len(body); {
		eol := strings.IndexByte(body[pos:], '\n')
		end := len(body)
		if eol >= 0 {
			end = pos + eol
		}
		line := body[pos:end]
		if b, ok := ParseOpen(line); ok {
			out = append(out, Span{Start: pos, End: end, Line: line, Bullet: b})
		}
		if eol < 0 {
			break
		}
		pos = end + 1
	}
	return out
}

// OpenSpan is a Span with the position of its section body in the file text.
type OpenSpan struct {
	Span
	Section string
	// Abs is the offset of the line in the file, found as the helper finds it:
	// where the section body first occurs in the text.
	Abs int
}

// openBulletSpans lists the unindented open bullets of the open sections.
func openBulletSpans(content string) []OpenSpan {
	var out []OpenSpan
	for _, name := range OpenSections {
		s, e, ok := section.Find(content, name)
		if !ok {
			continue
		}
		body := content[s:e]
		base := strings.Index(content, body)
		for _, sp := range openSpans(body) {
			out = append(out, OpenSpan{Span: sp, Section: name, Abs: base + sp.Start})
		}
	}
	return out
}

// BackfillSince stamps "Since: <head>" on every open bullet without a Since
// field and returns how many it stamped. A missing BACKLOG.md is no work.
func (f *File) BackfillSince(head string) (stamped int, err error) {
	path := f.backlogPath()
	err = fsio.Locked(path, fsio.LockTimeout, func() error {
		content, err := fsio.ReadText(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		type edit struct {
			start, end int
			text       string
		}
		var edits []edit
		for _, sp := range openBulletSpans(content) {
			if pystr.Strip(ParseFields(sp.Line).Since) != "" {
				continue
			}
			edits = append(edits, edit{sp.Abs, sp.Abs + (sp.End - sp.Start), pystr.Rstrip(sp.Line) + " Since: " + head})
		}
		if len(edits) == 0 {
			return nil
		}
		sort.Slice(edits, func(i, j int) bool { return edits[i].start > edits[j].start })
		for i := range edits { // back to front, so earlier offsets stay valid
			e := edits[i]
			content = content[:e.start] + e.text + content[e.end:]
		}
		stamped = len(edits)
		return fsio.WriteFileAtomic(path, []byte(content))
	})
	if err != nil {
		return 0, err
	}
	return stamped, nil
}

// ---- refactor counters ----------------------------------------------------------

func (f *File) countersPath() string { return f.rota("counters.json") }

var zero = json.Number("0")

// ErrCounters is wrapped by the error for a counters.json whose
// since_refactor is not the object the helpers write.
var ErrCounters = errors.New("counters.json since_refactor is not an object")

func sinceRefactor(d *jsonx.Object) (feat, bugs any, err error) {
	feat, bugs = any(zero), any(zero)
	raw, ok := d.Get("since_refactor")
	if !ok {
		return feat, bugs, nil
	}
	sr, ok := raw.(*jsonx.Object)
	if !ok {
		return nil, nil, ErrCounters
	}
	if v, ok := sr.Get("features"); ok {
		feat = v
	}
	if v, ok := sr.Get("bugs"); ok {
		bugs = v
	}
	return feat, bugs, nil
}

// RefactorAge is hv-refactor-age: the features and bugs completed since the
// last refactor, from counters.json since_refactor; 0 for a missing file or
// key. The values are the stored JSON numbers.
func (f *File) RefactorAge() (features, bugs any, err error) {
	d, ok := fsio.LoadJSON(f.countersPath(), jsonx.NewObject()).(*jsonx.Object)
	if !ok {
		return nil, nil, errors.New("counters.json is not a JSON object")
	}
	return sinceRefactor(d)
}

func nonZero(v any) bool {
	if n, ok := v.(json.Number); ok {
		fl, err := strconv.ParseFloat(string(n), 64)
		return err != nil || fl != 0
	}
	return v != nil && v != false && v != ""
}

// RefactorReset is hv-refactor-reset: it zeroes since_refactor to
// {"features": 0, "bugs": 0}. counters.json is rewritten even when it already
// holds zeros, and created when missing. changed is whether either count was
// non-zero.
func (f *File) RefactorReset() (changed bool, err error) {
	err = fsio.UpdateJSON(f.countersPath(), jsonx.NewObject(), func(v any) (any, error) {
		d, ok := v.(*jsonx.Object)
		if !ok {
			return nil, errors.New("counters.json is not a JSON object")
		}
		// A since_refactor that is not an object is replaced, as in the helper.
		feat, bugs, err := sinceRefactor(d)
		changed = err != nil || nonZero(feat) || nonZero(bugs)
		sr := jsonx.NewObject()
		sr.Set("features", zero)
		sr.Set("bugs", zero)
		d.Set("since_refactor", sr)
		return d, nil
	})
	return changed, err
}
