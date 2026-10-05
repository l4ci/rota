package backlog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	gitx "github.com/l4ci/rota/internal/git"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/pystr"
	"github.com/l4ci/rota/internal/section"
)

// The write side of the file backend: FileBackend.append, create,
// set_field, complete and uncomplete in bin/hvlib_backend.py.

// Field is one Name=Value pair for CreateInput, in the order it is written.
type Field struct{ Name, Value string }

// CreateInput is one capture (Backend.Create).
type CreateInput struct {
	Kind  string // bugs | features | tasks
	Title string
	Tag   string // bugs P0..P3, features Major|Minor|Cosmetic, tasks none
	Desc  string
	// Fields are written after the description, in this order; a name given
	// twice keeps its first position and its last value.
	Fields  []Field
	Body    []byte // detail file content; used only when HasBody
	HasBody bool
	// Since is a file-backend Since: anchor the issue backend keeps in its
	// fields block (IssueBackend.create(since=), migration only). The file
	// backend stamps its own and ignores this.
	Since string
}

// CreateResult is what Create made.
type CreateResult struct {
	ID     string // "B07"
	Type   string // "B"
	Detail string // ".rota/<kind>/<ID>.md" when a body was written, else ""
}

// CompleteInput is one close (Backend.Complete).
type CompleteInput struct {
	Commit  string // short hash recorded on the Done line
	Date    string // YYYY-MM-DD
	Reason  string // done | handed-off | blocked | dropped
	Note    string // one line; ignored for done
	NoProof bool   // skip the proof gate
}

// Comment is one comment of an item.
type Comment struct {
	Who  string // author (issues) or date (file)
	Kind string
	Text string
}

// CommentKinds are the kinds a comment can have (COMMENT_KINDS).
var CommentKinds = []string{"question", "answer", "decision", "feedback"}

// CreateFields are the fields a capture may set: everything but the ones it
// derives itself (Detail from the body, Since from HEAD).
var CreateFields = []string{"Related", "Milestone", "Repos", "Subsystem", "Captured"}

var (
	kindTags = map[string][]string{
		"bugs":     {"P0", "P1", "P2", "P3"},
		"features": {"Major", "Minor", "Cosmetic"},
		"tasks":    nil,
	}
	kindLetter = map[string]string{"bugs": "B", "features": "F", "tasks": "T"}
	// A refactor commit does not count toward refactor pressure.
	refactorSubject = regexp.MustCompile(`\Arefactor(\(.+?\))?!?:`)
)

// proofRows is the number of proof rows recorded for an item: the rows of the
// "## Proof" section of its detail file, counted by the injected CountProof.
func (f *File) proofRows(id string) (int, error) {
	if f.CountProof == nil {
		return 0, errors.New("backlog: no proof counter injected")
	}
	path := DetailPath(f.Root, id)
	if path == "" {
		return 0, nil
	}
	content, err := fsio.ReadText(path)
	if err != nil || content == "" {
		return 0, nil
	}
	return f.CountProof(content), nil
}

// detailDir is detail_dir_for_id: the detail directory of an ID's prefix
// (upper-cased), "" for a prefix without one.
func detailDir(id string) string {
	if id == "" {
		return ""
	}
	t, ok := TypeByLetter(strings.ToUpper(id[:1]))
	if !ok {
		return ""
	}
	return t.Kind
}

func sectionForDir(dir string) string {
	for _, t := range Types {
		if t.Kind != "" && t.Kind == dir {
			return t.Section
		}
	}
	return "Unknown"
}

func (f *File) backlogPath() string { return f.rota("BACKLOG.md") }

func (f *File) requireBacklog() error {
	if _, err := os.Stat(f.backlogPath()); err != nil {
		return errf(ErrNotFound, ".rota/BACKLOG.md not found")
	}
	return nil
}

// git runs git in the project root and returns trimmed stdout.
func (f *File) git(args ...string) (string, bool) {
	res, err := gitx.Repo{Dir: f.Root}.Run(context.Background(), args...)
	return pystr.Strip(res.Stdout), err == nil && res.Code == 0
}

// Append adds line at the end of a section ("## Bugs" or "Bugs") of
// BACKLOG.md. A bullet without a Since field gets `Since: <short HEAD>` when
// the project is a git repo with a commit. A missing BACKLOG.md or section
// wraps ErrNotFound.
func (f *File) Append(sec, line string) error {
	name := strings.TrimPrefix(sec, "## ")
	if strings.HasPrefix(line, "- ") && !strings.Contains(line, "Since:") {
		if head, ok := f.git("rev-parse", "--short", "HEAD"); ok && head != "" {
			line = line + " Since: " + head
		}
	}
	if err := f.requireBacklog(); err != nil {
		return err
	}
	path := f.backlogPath()
	return fsio.Locked(path, fsio.LockTimeout, func() error {
		content, err := fsio.ReadText(path)
		if err != nil {
			return err
		}
		_, end, ok := section.Find(content, name)
		if !ok {
			return errf(ErrNotFound, "section '%s' not found", sec)
		}
		tail := ""
		if content[end:] != "" {
			tail = "\n" + content[end:]
		}
		content = strings.TrimRight(content[:end], "\n") + "\n" + line + "\n" + tail
		return fsio.WriteFileAtomic(path, []byte(content))
	})
}

// checkCreate is _check_create: the cleaned title and fields, or ErrInvalid.
func checkCreate(in CreateInput) (title string, fields []Field, err error) {
	if _, ok := kindLetter[in.Kind]; !ok {
		return "", nil, errf(ErrInvalid, "unknown kind '%s' (expected bugs|features|tasks)", in.Kind)
	}
	title = oneLine(in.Title)
	if title == "" {
		return "", nil, errf(ErrInvalid, "--title is required")
	}
	if in.Tag != "" {
		ok := false
		for _, t := range kindTags[in.Kind] {
			ok = ok || t == in.Tag
		}
		if !ok {
			if len(kindTags[in.Kind]) == 0 {
				return "", nil, errf(ErrInvalid, "invalid tag '%s' for %s (tasks take no tag)", in.Tag, in.Kind)
			}
			return "", nil, errf(ErrInvalid, "invalid tag '%s' for %s (expected %s)", in.Tag, in.Kind, strings.Join(kindTags[in.Kind], "/"))
		}
	}
	idx := map[string]int{}
	for _, fv := range in.Fields {
		known := false
		for _, n := range CreateFields {
			known = known || n == fv.Name
		}
		if !known {
			return "", nil, errf(ErrInvalid, "'%s' is not a settable field (expected %s)", fv.Name, strings.Join(CreateFields, "|"))
		}
		if pystr.Strip(fv.Value) == "" {
			return "", nil, errf(ErrInvalid, "field %s needs a non-empty value", fv.Name)
		}
		v := oneLine(fv.Value)
		if i, seen := idx[fv.Name]; seen {
			fields[i].Value = v
			continue
		}
		idx[fv.Name] = len(fields)
		fields = append(fields, Field{fv.Name, v})
	}
	return title, fields, nil
}

// Create captures one item: it mints the ID, appends the bullet (Since
// stamped by Append) and, with a body, writes .rota/<kind>/<ID>.md with every
// "{ID}" replaced. As in the old helper the counter is bumped before the
// bullet is appended, so a missing section still burns an ID.
func (f *File) Create(in CreateInput) (CreateResult, error) {
	title, fields, err := checkCreate(in)
	if err != nil {
		return CreateResult{}, err
	}
	if err := f.requireBacklog(); err != nil {
		return CreateResult{}, err
	}
	id, err := f.NextID(in.Kind)
	if err != nil {
		return CreateResult{}, err
	}
	head := "- **[" + id + "] "
	if in.Tag != "" {
		head += "[" + in.Tag + "] "
	}
	head += title
	if !strings.ContainsRune(".!?", rune(title[len(title)-1])) {
		head += "."
	}
	parts := []string{head + "**"}
	if d := pystr.Strip(in.Desc); d != "" {
		parts = append(parts, d)
	}
	res := CreateResult{ID: id, Type: kindLetter[in.Kind]}
	rel := ".rota/" + in.Kind + "/" + id + ".md"
	if in.HasBody {
		parts = append(parts, "Detail: `"+rel+"`")
	}
	for _, fv := range fields {
		parts = append(parts, fv.Name+": "+fv.Value)
	}
	if err := f.Append(sectionName(in.Kind), strings.Join(parts, " ")); err != nil {
		return CreateResult{}, err
	}
	if in.HasBody {
		path := filepath.Join(f.Root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
			return CreateResult{}, err
		}
		body := strings.ReplaceAll(string(in.Body), "{ID}", id)
		if err := fsio.WriteFileAtomic(path, []byte(body)); err != nil {
			return CreateResult{}, err
		}
		res.Detail = rel
	}
	return res, nil
}

func sectionName(kind string) string { return sectionForDir(kind) }

// SetField sets, replaces or clears a trailing field on the open bullet of
// ref (FileBackend.set_field). An item that exists but is completed or
// archived is a RefusedError wrapping ErrClosed; an unknown one wraps
// ErrNotFound.
func (f *File) SetField(ref, field, value string) (bool, error) {
	if err := f.requireBacklog(); err != nil {
		return false, err
	}
	path := f.backlogPath()
	changed := false
	err := fsio.Locked(path, fsio.LockTimeout, func() error {
		content, err := fsio.ReadText(path)
		if err != nil {
			return err
		}
		re := regexp.MustCompile(`(?m)^- \*\*\[` + regexp.QuoteMeta(ref) + `\].*$`)
		m := re.FindStringIndex(content)
		if m == nil {
			if _, gerr := f.Get(ref); gerr == nil {
				return refused("closed item", ErrClosed,
					"[%s] has no open bullet in .rota/BACKLOG.md (completed or archived)", ref)
			}
			return errf(ErrNotFound, "[%s] has no open bullet in .rota/BACKLOG.md (unknown, completed, or archived)", ref)
		}
		raw := content[m[0]:m[1]]
		if field == "detail" && pystr.Strip(value) != "" {
			rel := strings.Trim(value, "` \t")
			if rel == "" {
				return errf(ErrInvalid, `detail path is empty; pass "" to clear the pointer`)
			}
			if !f.isFile(rel) {
				return errf(ErrNotFound, "detail file %s does not exist", rel)
			}
		}
		line, err := SetField(raw, field, value)
		if err != nil {
			return errf(ErrInvalid, "%s", err.Error())
		}
		if line == raw {
			return nil
		}
		changed = true
		return fsio.WriteFileAtomic(path, []byte(content[:m[0]]+line+content[m[1]:]))
	})
	return changed, err
}

// isFile is Path(rel).is_file(), tried against the project root first (the
// detail pointers are root-relative) and then the working directory.
func (f *File) isFile(rel string) bool {
	for _, p := range []string{filepath.Join(f.Root, rel), rel} {
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
			return true
		}
	}
	return false
}

// Complete moves the open bullet of ref to ## Completed with a Done marker
// (FileBackend.complete). An already completed item is a no-op (false). Unless
// the commit subject is a refactor form, counters.json since_refactor counts
// the close for bugs and features.
func (f *File) Complete(ref string, in CompleteInput) (bool, error) {
	if err := f.requireBacklog(); err != nil {
		return false, err
	}
	path := f.backlogPath()
	moved := false
	err := fsio.Locked(path, fsio.LockTimeout, func() error {
		content, err := fsio.ReadText(path)
		if err != nil {
			return err
		}
		q := regexp.QuoteMeta(ref)
		active := regexp.MustCompile(`(?m)^- \*\*\[` + q + `\].*$`)
		completed := regexp.MustCompile(`(?m)^- ~~\*\*\[` + q + `\].*~~[` + pystr.SpaceClass + `]+Done[` +
			pystr.SpaceClass + `]+\p{Nd}{4}-\p{Nd}{2}-\p{Nd}{2}`)
		m := active.FindStringIndex(content)
		if m == nil {
			if completed.MatchString(content) {
				return nil
			}
			return errf(ErrNotFound, "[%s] not found", ref)
		}
		if in.Reason == "done" && !in.NoProof {
			n, err := f.proofRows(ref)
			if err != nil {
				return err
			}
			if n == 0 {
				return refused("proof missing", ErrProofMissing,
					"[%s] no proof recorded, pass --no-proof to override", ref)
			}
		}
		line := content[m[0]:m[1]]
		rest := ""
		if m[1]+1 <= len(content) {
			rest = content[m[1]+1:]
		}
		content = content[:m[0]] + rest
		done, err := FormatDone(line, in.Date, in.Commit, in.Reason, in.Note)
		if err != nil {
			return errf(ErrInvalid, "%s", err.Error())
		}
		if _, end, ok := section.Find(content, "Completed"); !ok {
			content = strings.TrimRight(content, "\n") + "\n\n## Completed\n\n" + done + "\n"
		} else {
			content = strings.TrimRight(content[:end], "\n") + "\n" + done + "\n" + content[end:]
		}
		moved = true
		return fsio.WriteFileAtomic(path, []byte(content))
	})
	if err != nil || !moved {
		return false, err
	}
	if ref != "" && strings.Contains("BF", ref[:1]) {
		if key := detailDir(ref); key != "" && !f.isRefactor(in.Commit) {
			if err := f.bumpSinceRefactor(key, 1); err != nil {
				return true, err
			}
		}
	}
	return true, nil
}

// isRefactor is whether the commit's subject is a `refactor:` form; a hash git
// cannot resolve is not.
func (f *File) isRefactor(hash string) bool {
	subj, ok := f.git("log", "-1", "--format=%s", hash)
	return ok && refactorSubject.MatchString(subj)
}

// bumpSinceRefactor adds delta to counters.json since_refactor[key], never
// below 0, creating the object with its two default keys when absent.
func (f *File) bumpSinceRefactor(key string, delta int) error {
	return fsio.UpdateJSON(f.rota("counters.json"), jsonx.NewObject(), func(v any) (any, error) {
		d, ok := v.(*jsonx.Object)
		if !ok {
			return nil, errors.New("counters.json is not a JSON object")
		}
		var sr *jsonx.Object
		if raw, has := d.Get("since_refactor"); has {
			if sr, ok = raw.(*jsonx.Object); !ok {
				return nil, errors.New("counters.json: since_refactor is not an object")
			}
		} else {
			sr = jsonx.NewObject()
			sr.Set("features", json.Number("0"))
			sr.Set("bugs", json.Number("0"))
			d.Set("since_refactor", sr)
		}
		cur := 0
		if raw, has := sr.Get(key); has {
			num, _ := raw.(json.Number)
			n, err := strconv.Atoi(string(num))
			if err != nil {
				return nil, fmt.Errorf("counters.json: since_refactor.%s is not an integer", key)
			}
			cur = n
		}
		sr.Set(key, json.Number(strconv.Itoa(max(0, cur+delta))))
		return d, nil
	})
}

// Reopen restores the Done line of ref (BACKLOG.md ## Completed first, then
// ARCHIVE.md) to its type section (FileBackend.uncomplete). An item that is
// already active is a no-op (false).
func (f *File) Reopen(ref string) (bool, error) {
	if err := f.requireBacklog(); err != nil {
		return false, err
	}
	path := f.backlogPath()
	apath := f.rota("ARCHIVE.md")
	restored := false
	doneHash, dirName := "", ""
	err := fsio.Locked(path, fsio.LockTimeout, func() error {
		content, err := fsio.ReadText(path)
		if err != nil {
			return err
		}
		cs, ce, hasC := section.Find(content, "Completed")
		activeRe := regexp.MustCompile(`(?m)^- \*\*\[` + regexp.QuoteMeta(ref) + `\]`)
		for _, m := range activeRe.FindAllStringIndex(content, -1) {
			if !hasC || !(cs <= m[0] && m[0] < ce) {
				return nil // already active
			}
		}

		source := ""
		var done Done
		var archive string
		if hasC {
			if lstart, lend, d, ok := findDoneIn(content[cs:ce], ref); ok {
				done = d
				absStart, absEnd := cs+lstart, cs+lend
				if strings.HasPrefix(content[absEnd:], "\n") {
					absEnd++
				}
				content = content[:absStart] + content[absEnd:]
				source = "backlog"
			}
		}
		if source == "" {
			if raw, err := fsio.ReadText(apath); err == nil {
				if lstart, lend, d, ok := findDoneIn(raw, ref); ok {
					done = d
					cut := lend
					if strings.HasPrefix(raw[lend:], "\n") {
						cut++
					}
					archive = raw[:lstart] + raw[cut:]
					source = "archive"
				}
			}
		}
		if source == "" {
			return errf(ErrNotFound, "[%s] not found in BACKLOG.md (## Completed) or .rota/ARCHIVE.md", ref)
		}
		activeLine := "- " + done.Inner
		dirName = detailDir(ref)
		target := sectionForDir(dirName)
		if target == "Unknown" {
			return errf(ErrInvalid, "[%s] has unsupported prefix (expected B/F/T)", ref)
		}
		if s, e, ok := section.Find(content, target); !ok {
			block := "## " + target + "\n" + activeLine + "\n\n"
			if cs2, _, ok := section.Find(content, "Completed"); !ok {
				content = strings.TrimRight(content, "\n") + "\n\n" + block
			} else {
				h := strings.LastIndex(content[:cs2], "## Completed")
				content = content[:h] + block + content[h:]
			}
		} else {
			body := content[s:e]
			hasBullet := false
			for _, ln := range pystr.Splitlines(body) {
				hasBullet = hasBullet || strings.HasPrefix(ln, "- ")
			}
			nb := "\n" + activeLine + "\n\n"
			if hasBullet {
				nb = strings.TrimRight(body, "\n") + "\n" + activeLine + "\n\n"
			}
			content = content[:s] + nb + content[e:]
		}
		if err := fsio.WriteFileAtomic(path, []byte(content)); err != nil {
			return err
		}
		if source == "archive" {
			if err := fsio.WriteFileAtomic(apath, []byte(archive)); err != nil {
				return err
			}
		}
		restored, doneHash = true, done.Hash
		return nil
	})
	if err != nil || !restored {
		return false, err
	}
	// Mirror Complete's bump in reverse; tasks and refactor commits carry no
	// count, and a hash git cannot resolve is skipped.
	if dirName != "tasks" {
		if subj, ok := f.git("log", "-1", "--format=%s", doneHash); ok && !refactorSubject.MatchString(subj) {
			if err := f.bumpSinceRefactor(dirName, -1); err != nil {
				return true, err
			}
		}
	}
	return true, nil
}

// findDoneIn finds the Done line of id in text: its offsets (line end
// excludes the newline) and the parse.
func findDoneIn(text, id string) (start, end int, d Done, ok bool) {
	pos := 0
	for _, raw := range strings.Split(text, "\n") {
		if p, pok := ParseDone(raw); pok && p.ID == id {
			return pos, pos + len(raw), p, true
		}
		pos += len(raw) + 1
	}
	return 0, 0, Done{}, false
}
