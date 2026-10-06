package backlog

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/pystr"
	"github.com/l4ci/rota/internal/section"
)

// Item removal with dependency cleanup: bin/hv-rm and bin/hvlib_crossref.py.

// allSections is ALL_BACKLOG_SECTIONS: the open sections plus Completed.
var allSections = append(append([]string{}, OpenSections...), "Completed")

// XRef is one bullet whose Related field names a removed item.
type XRef struct {
	Section string
	Old     string // the bullet, whitespace-stripped
	New     string // the bullet with the reference stripped
}

// RmItem is what removing one item does or did.
type RmItem struct {
	ID           string
	Type         string // "B" | "F" | "T"
	TodoEntry    bool   // the bullet is in BACKLOG.md
	Archive      bool   // the bullet is in ARCHIVE.md
	CrossRefs    int    // Related fields stripped, in BACKLOG.md and (with scrub) ARCHIVE.md
	DetailFile   string // ".rota/<kind>/<ID>.md" when it exists
	PlanFiles    []string
	ActiveBranch string // set when status.json lists the item on a branch
	Section      string // section that holds the bullet
	Bullet       string
	Scrub        bool // the archive entry is removed too
}

// RmResult is a removal plan or its application.
type RmResult struct {
	Applied bool
	Items   []RmItem
}

var relatedIDRe = regexp.MustCompile(`\[(` + IDPattern(1) + `)\]`)

func parseRelatedIDs(v string) []string {
	var out []string
	for _, m := range relatedIDRe.FindAllStringSubmatch(v, -1) {
		out = append(out, m[1])
	}
	return out
}

// removeIDFromRelated is remove_id_from_related_field: strip [id] from the
// Related field of line, dropping the whole " Related: ..." segment when it
// ends up empty.
func removeIDFromRelated(line, id string) string {
	related := ParseFields(line).Related
	if related == "" {
		return line
	}
	ids := parseRelatedIDs(related)
	has := false
	var keep []string
	for _, x := range ids {
		if x == id {
			has = true
		} else {
			keep = append(keep, x)
		}
	}
	if !has {
		return line
	}
	others := othersOf("Related")
	if len(keep) == 0 {
		return pystr.Rstrip(dropFieldMin(line, "Related:", others, 1))
	}
	parts := make([]string, len(keep))
	for i, x := range keep {
		parts[i] = "[" + x + "]"
	}
	newVal := strings.Join(parts, ", ")
	// `(Related:\s+)(.+?)(?=...)` replaced everywhere, no \b in front.
	var b strings.Builder
	copied := 0
	for from := 0; from < len(line); {
		i := strings.Index(line[from:], "Related:")
		if i < 0 {
			break
		}
		pos := from + i
		gs, e, ok := matchValueMin(line, pos+len("Related:"), others, 1)
		if !ok {
			from = pos + 1
			continue
		}
		b.WriteString(line[copied:gs])
		b.WriteString(newVal)
		copied, from = e, max(e, pos+1)
	}
	b.WriteString(line[copied:])
	return b.String()
}

// collectCrossRefs is collect_cross_refs: for each named section, the bullets
// that list id in their Related field, except id's own bullet.
func collectCrossRefs(content, id string, sections []string) []XRef {
	var hits []XRef
	origin := regexp.MustCompile(`\A- (?:~~)?\*\*\[` + regexp.QuoteMeta(id) + `\]`)
	for _, name := range sections {
		s, e, ok := section.Find(content, name)
		if !ok {
			continue
		}
		for _, raw := range pystr.Splitlines(content[s:e]) {
			line := pystr.Strip(raw)
			if !strings.HasPrefix(line, "- ") || origin.MatchString(line) {
				continue
			}
			for _, r := range parseRelatedIDs(ParseFields(line).Related) {
				if r == id {
					hits = append(hits, XRef{name, line, removeIDFromRelated(line, id)})
					break
				}
			}
		}
	}
	return hits
}

// applyCrossRefs replaces each old line by its new one, once, where the whole
// line matches (apply_cross_ref_strips).
func applyCrossRefs(content string, xrefs []XRef) string {
	for _, x := range xrefs {
		re := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(x.Old) + `$`)
		if m := re.FindStringIndex(content); m != nil {
			content = content[:m[0]] + strings.TrimRightFunc(x.New, pystr.IsSpace) + content[m[1]:]
		}
	}
	return content
}

// findBulletIn is find_bullet_in_content: the section and line of id, open
// bullets before done ones within a section.
func findBulletIn(content, id string, sections []string) (sec, line string, ok bool) {
	q := regexp.QuoteMeta(id)
	open := regexp.MustCompile(`(?m)^- \*\*\[` + q + `\].*$`)
	done := regexp.MustCompile(`(?m)^- ~~\*\*\[` + q + `\].*$`)
	for _, name := range sections {
		s, e, found := section.Find(content, name)
		if !found {
			continue
		}
		body := content[s:e]
		if m := open.FindString(body); m != "" {
			return name, m, true
		}
		if m := done.FindString(body); m != "" {
			return name, m, true
		}
	}
	return "", "", false
}

// stripBullet drops one whole bullet line, accepting EOF as a line terminator
// and preserving any blank line after the bullet.
func stripBullet(content, bullet string) string {
	re := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(bullet) + `(?:\n(\n)?|\z)`)
	m := re.FindStringSubmatchIndex(content)
	if m == nil {
		return content
	}
	repl := ""
	if m[2] >= 0 {
		repl = "\n"
	}
	return content[:m[0]] + repl + content[m[1]:]
}

var archiveHeadRe = regexp.MustCompile(`(?m)^## (.+)$`)

// activeBranches maps item IDs to the branch of the first active entry of
// status.json that lists them.
func (f *File) activeBranches() map[string]string {
	out := map[string]string{}
	st, ok := fsio.LoadJSON(f.rota("status.json"), nil).(*jsonx.Object)
	if !ok {
		return out
	}
	list, _ := st.Get("active")
	entries, _ := list.([]any)
	for _, e := range entries {
		obj, ok := e.(*jsonx.Object)
		if !ok {
			continue
		}
		branch := "(unknown)"
		if b, has := obj.Get("branch"); has {
			if s, ok := b.(string); ok {
				branch = s
			} else {
				branch = pyBranch(b)
			}
		}
		for _, id := range activeItems(obj) {
			if _, seen := out[id]; !seen {
				out[id] = branch
			}
		}
	}
	return out
}

func pyBranch(v any) string {
	b, _ := jsonx.MarshalCompact(v)
	return string(b)
}

// activeItems is active_items: entry["items"] as a list, or a CSV string split.
func activeItems(entry *jsonx.Object) []string {
	raw, _ := entry.Get("items")
	switch v := raw.(type) {
	case []any:
		var out []string
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		var out []string
		for _, p := range strings.Split(v, ",") {
			if p = pystr.Strip(p); p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	return nil
}

func (f *File) planFiles(id string) []string {
	entries, err := os.ReadDir(f.rota("plans"))
	if err != nil {
		return []string{}
	}
	names := []string{}
	for _, e := range entries {
		name := e.Name()
		ext := filepath.Ext(name)
		if ext != ".md" || name == ".md" {
			continue
		}
		if strings.HasSuffix(strings.TrimSuffix(name, ext), "-"+id) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = ".rota/plans/" + n
	}
	return out
}

// Remove removes items from the backlog with their cross-references, detail
// file and plan files (hv-rm). Without apply it only computes the plan. IDs
// are exact ("B07"). An ID in neither BACKLOG.md nor ARCHIVE.md wraps
// ErrNotFound; with apply, an ID that status.json lists on a branch is an
// *ActiveError and nothing is written. An archived item's ARCHIVE.md entry is
// removed only with scrubArchive.
func (f *File) Remove(ids []string, scrubArchive, apply bool) (RmResult, error) {
	var res RmResult
	run := func() error {
		todo, _ := fsio.ReadText(f.rota("BACKLOG.md"))
		archive, _ := fsio.ReadText(f.rota("ARCHIVE.md"))
		infos := map[string]RmItem{}
		for _, id := range ids {
			if sec, line, ok := findBulletIn(todo, id, allSections); ok {
				infos[id] = RmItem{ID: id, TodoEntry: true, Section: sec, Bullet: line}
				continue
			}
			if archive != "" {
				if sec, line, ok := findBulletIn(archive, id, allSections); ok {
					infos[id] = RmItem{ID: id, Archive: true, Section: sec, Bullet: line}
					continue
				}
			}
			return errf(ErrNotFound, "[%s] not found in .rota/BACKLOG.md or .rota/ARCHIVE.md", id)
		}
		active := f.activeBranches()
		if apply {
			for _, id := range ids {
				if br, ok := active[id]; ok {
					return &ActiveError{ID: id, Branch: br}
				}
			}
		}

		newTodo, newArchive := todo, archive
		for _, id := range ids {
			info := infos[id]
			if info.TodoEntry {
				newTodo = stripBullet(newTodo, info.Bullet)
			} else if info.Archive && scrubArchive {
				newArchive = stripBullet(newArchive, info.Bullet)
			}
		}
		// Validate the complete removal before writing documents or deleting
		// artifacts. Archive entries intentionally retained without scrub are exempt.
		for _, id := range ids {
			info := infos[id]
			content, path := newTodo, "BACKLOG.md"
			if info.Archive {
				if !scrubArchive {
					continue
				}
				content, path = newArchive, "ARCHIVE.md"
			}
			if _, _, found := findBulletIn(content, id, allSections); found {
				return errf(ErrInvalid, "[%s] remains in .rota/%s after removal; no changes applied", id, path)
			}
		}
		var detail, plans []string
		for _, id := range ids {
			info := infos[id]
			xt := collectCrossRefs(newTodo, id, allSections)
			newTodo = applyCrossRefs(newTodo, xt)
			var xa []XRef
			if scrubArchive && newArchive != "" {
				var secs []string
				for _, m := range archiveHeadRe.FindAllStringSubmatch(newArchive, -1) {
					secs = append(secs, m[1])
				}
				xa = collectCrossRefs(newArchive, id, secs)
				newArchive = applyCrossRefs(newArchive, xa)
			}
			info.CrossRefs = len(xt) + len(xa)
			info.Scrub = scrubArchive
			if len(id) > 0 {
				info.Type = strings.ToUpper(id[:1])
			}
			if dir := detailDir(id); dir != "" {
				if _, err := os.Stat(f.rota(dir, id+".md")); err == nil {
					info.DetailFile = ".rota/" + dir + "/" + id + ".md"
					detail = append(detail, info.DetailFile)
				}
			}
			info.PlanFiles = f.planFiles(id)
			plans = append(plans, info.PlanFiles...)
			info.ActiveBranch = active[id]
			infos[id] = info
			res.Items = append(res.Items, info)
		}
		if !apply {
			return nil
		}
		if newTodo != todo {
			if err := fsio.WriteFileAtomic(f.rota("BACKLOG.md"), []byte(newTodo)); err != nil {
				return err
			}
		}
		if newArchive != archive && scrubArchive {
			if err := fsio.WriteFileAtomic(f.rota("ARCHIVE.md"), []byte(newArchive)); err != nil {
				return err
			}
		}
		for _, rel := range append(detail, plans...) {
			if err := os.Remove(filepath.Join(f.Root, filepath.FromSlash(rel))); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		res.Applied = true
		return nil
	}
	if !apply {
		return res, run()
	}
	err := fsio.Locked(f.rota("BACKLOG.md"), fsio.LockTimeout, run)
	return res, err
}
