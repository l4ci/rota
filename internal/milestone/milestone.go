// Package milestone ports the hv-vision-* helpers for file mode: milestone
// detail files under .rota/milestones/<MNN>.md, the overview in
// .rota/MILESTONES.md and the managed vision block in the instructions file.
package milestone

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/artifact"
	"github.com/l4ci/rota/internal/counter"
	"github.com/l4ci/rota/internal/frontmatter"
	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/knowledge"
	"github.com/l4ci/rota/internal/section"
)

// Statuses are the legal milestone states, in ROTA_MILESTONE_STATUSES order.
var Statuses = []string{"planned", "active", "shipped", "archived"}

// fmBlock and fmID are private on purpose. Put's id check mirrors
// hvlib_backend.milestone_put exactly: a `---` block at the very start (LF or
// CRLF), then the first `id:` line inside it, taken as typed. The shared
// frontmatter parser keeps the last of a repeated key and trims values, so it
// could accept a text the old helper refused; it is not used here.
var (
	idRe        = regexp.MustCompile(`^M\d{2,}$`)
	milestoneID = regexp.MustCompile(`M\d+`)
	fmBlock     = regexp.MustCompile(`(?s)\A---\r?\n(.*?)\r?\n---[ \t]*(?:\r?\n|\z)`)
	fmID        = regexp.MustCompile(`(?m)^id:[ \t]*(\S+)`)
)

// ValidID reports whether id is M\d{2,}.
func ValidID(id string) bool { return idRe.MatchString(id) }

func checkID(id string) error {
	if !ValidID(id) {
		return artifact.Errf(artifact.ExitUsage, "milestone ID must match M\\d{2,} (e.g. M01, M03), got %q", id)
	}
	return nil
}

// ValidStatus reports whether s is one of the four statuses.
func ValidStatus(s string) bool {
	for _, v := range Statuses {
		if v == s {
			return true
		}
	}
	return false
}

func detailPath(root, id string) string { return filepath.Join(root, ".rota", "milestones", id+".md") }
func overviewPath(root string) string   { return filepath.Join(root, ".rota", "MILESTONES.md") }

func notFound(id string) *artifact.Error {
	return artifact.Errf(artifact.ExitResolution, "milestone %s not found (.rota/milestones/%s.md)", id, id)
}

// Stub is the starter text of a milestone detail file (milestone_stub); the
// issue-mode tracking issue body shares it. It is dated today.
func Stub(id, title, summary string, depends []string) string {
	return StubOn(id, title, summary, depends, time.Now().Format("2006-01-02"))
}

// StubOn is Stub with an explicit created date.
func StubOn(id, title, summary string, depends []string, today string) string {
	return "---\nid: " + id + "\ntitle: " + title + "\nstatus: planned\ndepends: [" + strings.Join(depends, ", ") + "]\ncreated: " + today + "\n---\n\n" +
		"# " + id + " — " + title + "\n\n## Goal\n\n" + summary + "\n\n## Acceptance criteria\n\n- _(define what shipped looks like)_\n\n" +
		"## Rationale\n\n_(why this milestone, why now)_\n\n## Open risks\n\n_(unknowns, technical risks, dependencies that could shift)_\n\n" +
		"## Research findings\n\n_(prior art, references, lessons from /rota-vision web search)_\n\n## Notes\n\n_(free-form brainstorm)_\n"
}

// Add mints the next milestone ID, writes its detail file and appends its
// entry to MILESTONES.md. depends is a comma list; its IDs are not validated.
func Add(root, title, summary, depends string) (string, error) {
	return addFile(root, title, summary, artifact.SplitCSV(depends))
}

func addFile(root, title, summary string, deps []string) (string, error) {
	id, err := counter.Next(root, "milestones")
	if err != nil {
		return "", err
	}
	detail := detailPath(root, id)
	if err := os.MkdirAll(filepath.Dir(detail), 0o777); err != nil {
		return "", err
	}
	if err := fsio.WriteFileAtomic(detail, []byte(Stub(id, title, summary, deps))); err != nil {
		return "", err
	}
	overview := "—"
	if len(deps) > 0 {
		overview = strings.Join(deps, ", ")
	}
	entry := fmt.Sprintf("\n### %s — %s\n\n**Status:** planned · **Depends:** %s\n\n%s\n\n[Full plan: `.rota/milestones/%s.md`]\n", id, title, overview, summary, id)
	ms := overviewPath(root)
	err = fsio.Locked(ms, fsio.LockTimeout, func() error {
		content, rerr := fsio.ReadText(ms)
		if rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
			return artifact.Errf(artifact.ExitInternal, "cannot read %s: %v", ms, rerr)
		}
		content = section.Append(content, "Milestones", entry)
		if !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		return fsio.WriteFileAtomic(ms, []byte(content))
	})
	return id, err
}

// Entry is one milestone as List reports it.
type Entry struct {
	ID, Title, Status string
	Depends           []string
	Ready             bool
}

// List reads .rota/milestones/*.md in name order. ready is true when every
// dependency is shipped.
func List(root string) ([]Entry, error) {
	docs, err := artifact.ListDocs(filepath.Join(root, ".rota", "milestones"))
	if err != nil {
		return nil, err
	}
	out := []Entry{}
	shipped := map[string]bool{}
	for _, d := range docs {
		e := Entry{ID: frontmatter.Str(d.FM, "id"), Title: frontmatter.Str(d.FM, "title"),
			Status: frontmatter.Str(d.FM, "status"), Depends: []string{}}
		if e.ID == "" {
			e.ID = d.Stem
		}
		if e.Status == "" {
			e.Status = "planned"
		}
		switch v := d.FM["depends"].(type) {
		case []string:
			e.Depends = append(e.Depends, v...)
		case string:
			e.Depends = append(e.Depends, milestoneID.FindAllString(v, -1)...)
		}
		if e.Status == "shipped" {
			shipped[e.ID] = true
		}
		out = append(out, e)
	}
	for i := range out {
		out[i].Ready = true
		for _, d := range out[i].Depends {
			if !shipped[d] {
				out[i].Ready = false
			}
		}
	}
	return out, nil
}

// Active lists the IDs of active milestones in List order.
func Active(root string) ([]string, error) {
	list, err := List(root)
	if err != nil {
		return nil, err
	}
	return ActiveIDs(list), nil
}

// Show is the stored milestone file, verbatim.
func Show(root, id string) (string, error) {
	if err := checkID(id); err != nil {
		return "", err
	}
	b, err := os.ReadFile(detailPath(root, id))
	if err != nil {
		return "", notFound(id)
	}
	return string(b), nil
}

// Put replaces a milestone's text. The text must carry frontmatter with
// `id: <id>`, else the write is refused (exit 4). changed is false when the
// text is identical.
func Put(root, id, text string) (changed bool, err error) {
	if err = checkID(id); err != nil {
		return
	}
	p := detailPath(root, id)
	if _, serr := os.Stat(p); serr != nil {
		return false, notFound(id).WithHint("rota milestone add --title <text> --summary <text>")
	}
	var got string
	if m := fmBlock.FindStringSubmatch(text); m != nil {
		if im := fmID.FindStringSubmatch(m[1]); im != nil {
			got = im[1]
		}
	}
	if got != id {
		return false, artifact.Errf(artifact.ExitRefused, "milestone text needs frontmatter with 'id: %s'", id)
	}
	err = fsio.Locked(p, fsio.LockTimeout, func() error {
		old, rerr := os.ReadFile(p)
		if rerr != nil {
			return notFound(id)
		}
		if string(old) == text {
			return nil
		}
		changed = true
		return fsio.WriteFileAtomic(p, []byte(text))
	})
	return
}

// h2Re matches a level-2 heading line.
var h2Re = regexp.MustCompile(`(?m)^## `)

// SetOverview replaces the overview text of MILESTONES.md: everything between
// the optional "# " title line and the first "## " heading. The rest of the file is
// untouched. A body that carries a heading of its own is refused (exit 4). A missing MILESTONES.md is
// exit 3. changed is false when the text is already in place.
func SetOverview(root, text string) (changed bool, err error) {
	p := overviewPath(root)
	if _, serr := os.Stat(p); serr != nil {
		return false, artifact.Errf(artifact.ExitResolution, "%s not found", p).WithHint("rota milestone add --title <text> --summary <text>")
	}
	text = strings.Trim(text, "\n")
	if strings.TrimSpace(text) == "" {
		return false, artifact.Errf(artifact.ExitUsage, "overview text is empty")
	}
	if h2Re.MatchString(text) || strings.HasPrefix(text, "# ") || strings.Contains(text, "\n# ") {
		return false, artifact.Errf(artifact.ExitRefused, "overview text must not contain headings")
	}
	err = fsio.Locked(p, fsio.LockTimeout, func() error {
		old, rerr := fsio.ReadText(p)
		if rerr != nil {
			return artifact.Errf(artifact.ExitInternal, "cannot read %s: %v", p, rerr)
		}
		head, rest := "", old
		if nl := strings.Index(old, "\n"); strings.HasPrefix(old, "# ") && nl >= 0 {
			head, rest = old[:nl+1]+"\n", old[nl+1:]
		}
		if m := h2Re.FindStringIndex(rest); m != nil {
			rest = rest[m[0]:]
		} else {
			rest = ""
		}
		next := head + text + "\n"
		if rest != "" {
			next += "\n" + rest
		}
		if next == old {
			return nil
		}
		changed = true
		return fsio.WriteFileAtomic(p, []byte(next))
	})
	return
}

// SetStatus changes a milestone's frontmatter status, then regenerates the
// overview and the vision block (Index), as hv-vision-status did on every
// call. changed reports the status line only.
func SetStatus(root, id, status string) (changed bool, err error) {
	if changed, err = setStatus(root, id, status); err != nil {
		return false, err
	}
	_, err = Index(root)
	return changed, err
}

// setStatus is SetStatus without the Index pass.
func setStatus(root, id, status string) (changed bool, err error) {
	if err = checkID(id); err != nil {
		return
	}
	if !ValidStatus(status) {
		return false, artifact.Errf(artifact.ExitUsage, "--to must be one of: %s", strings.Join(Statuses, ", "))
	}
	p := detailPath(root, id)
	if _, serr := os.Stat(p); serr != nil {
		return false, notFound(id)
	}
	err = fsio.Locked(p, fsio.LockTimeout, func() error {
		content, rerr := fsio.ReadText(p)
		if rerr != nil {
			return notFound(id)
		}
		updated, found := frontmatter.UpdateField(content, "status", status)
		if !found {
			return artifact.Errf(artifact.ExitInternal, "status field not found in .rota/milestones/%s.md", id)
		}
		if updated == content {
			return nil
		}
		changed = true
		return fsio.WriteFileAtomic(p, []byte(updated))
	})
	return changed, err
}

var (
	activeHeadRe = regexp.MustCompile(`(?m)^## Milestones[ \t\r\f\v]*$`)
)

// updateStatusLine is hvlib_repos.update_milestone_status_line: the first
// "### <id> — <title>\n\n**Status:** <status>" gets the new status.
func updateStatusLine(content, id, status string) string {
	re := regexp.MustCompile(`(### ` + regexp.QuoteMeta(id) + ` — [^\n]+\n\n\*\*Status:\*\* )(?:planned|active|shipped|archived)`)
	loc := re.FindStringSubmatchIndex(content)
	if loc == nil {
		return content
	}
	return content[:loc[3]] + status + content[loc[1]:]
}

// Index regenerates the "## Active milestones" section and each **Status:**
// line in MILESTONES.md, then the vision block in the instructions file,
// from the milestone frontmatter. changed is true when either file changed.
func Index(root string) (changed bool, err error) {
	items, err := List(root)
	if err != nil {
		return false, err
	}
	return IndexFrom(root, items, false)
}

// seed is the MILESTONES.md an issue-mode index starts from when there is none
// (hv-bootstrap's text).
const seed = "# Milestones\n\n_(no vision yet \u2014 run `/rota-vision` to brainstorm milestones)_\n\n" +
	"## Active milestones\n\n_(none active \u2014 set with `/rota-vision`)_\n\n## Milestones\n"

// IndexFrom is Index over milestones the caller already listed. In issue mode
// (issue true) the list comes from the tracking issues, a missing
// MILESTONES.md is seeded, the per-milestone **Status:** lines are left
// alone, and the vision block points at the tracking issues.
func IndexFrom(root string, items []Entry, issue bool) (changed bool, err error) {
	var active []Entry
	shipped := map[string]bool{}
	for _, i := range items {
		if i.Status == "active" {
			active = append(active, i)
		}
		if i.Status == "shipped" {
			shipped[i.ID] = true
		}
	}

	ms := overviewPath(root)
	if issue {
		if _, serr := os.Stat(ms); serr != nil {
			if err := os.MkdirAll(filepath.Dir(ms), 0o777); err != nil {
				return false, err
			}
			if err := fsio.WriteFileAtomic(ms, []byte(seed)); err != nil {
				return false, err
			}
			changed = true
		}
	}
	if _, serr := os.Stat(ms); serr == nil {
		err = fsio.Locked(ms, fsio.LockTimeout, func() error {
			original, rerr := fsio.ReadText(ms)
			if rerr != nil {
				return artifact.Errf(artifact.ExitInternal, "cannot read %s: %v", ms, rerr)
			}
			text := original
			if !issue {
				for _, i := range items {
					text = updateStatusLine(text, i.ID, i.Status)
				}
			}
			body := "_(none active — set with `/rota-vision`)_"
			if len(active) > 0 {
				lines := make([]string, len(active))
				for k, i := range active {
					lines[k] = fmt.Sprintf("- %s — %s", i.ID, i.Title)
				}
				body = strings.Join(lines, "\n")
			}
			if _, _, ok := section.Find(text, "Active milestones"); ok {
				text = section.Replace(text, "Active milestones", "\n"+body+"\n\n")
			} else {
				block := "## Active milestones\n\n" + body + "\n"
				if m := activeHeadRe.FindStringIndex(text); m != nil {
					text = strings.TrimRight(text[:m[0]], "\n") + "\n\n" + block + "\n" + text[m[0]:]
				} else {
					text = strings.TrimRight(text, "\n") + "\n\n" + block
				}
			}
			if !strings.HasSuffix(text, "\n") {
				text += "\n"
			}
			if text == original {
				return nil
			}
			changed = true
			return fsio.WriteFileAtomic(ms, []byte(text))
		})
		if err != nil {
			return false, err
		}
	}

	var intro, body string
	switch {
	case len(active) > 0:
		lines := make([]string, len(active))
		for k, i := range active {
			deps := "—"
			if len(i.Depends) > 0 {
				deps = strings.Join(i.Depends, ", ")
			}
			flag := ""
			for _, d := range i.Depends {
				if !shipped[d] {
					flag = " ⚠ blocked"
					break
				}
			}
			lines[k] = fmt.Sprintf("- **%s** — %s (depends: %s)%s", i.ID, i.Title, deps, flag)
		}
		body = strings.Join(lines, "\n")
		where := "`.rota/milestones/MNN.md`"
		if issue {
			where = "the tracking issues (`rota milestone show MNN`)"
		}
		intro = "Active milestones live in `.rota/MILESTONES.md` (detail in " + where + "). Tag captured items with their milestone via the `Milestone:` field where applicable."
	case len(items) > 0:
		planned := 0
		for _, i := range items {
			if i.Status == "planned" {
				planned++
			}
		}
		if planned > 0 {
			body = fmt.Sprintf("_(no active milestones — %d planned; set one active with `/rota-vision`)_", planned)
		} else {
			body = "_(no active milestones — all shipped or archived; run `/rota-vision` to plan more)_"
		}
		intro = "Project milestones live in `.rota/MILESTONES.md`."
	default:
		body = "_(no milestones yet — run `/rota-vision` to brainstorm)_"
		intro = "Project milestones live in `.rota/MILESTONES.md`."
	}
	target := section.InstructionsFile(root)
	before, _ := os.ReadFile(target)
	if _, err := (knowledge.Store{Root: root}).WriteCustomBlock("vision", "## Project Vision\n\n"+intro+"\n\n"+body); err != nil {
		return changed, err
	}
	if after, _ := os.ReadFile(target); string(after) != string(before) {
		changed = true
	}
	return changed, nil
}
