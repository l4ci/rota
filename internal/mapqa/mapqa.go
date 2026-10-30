// Package mapqa ports the .rota/map and .rota/qa helpers (hv-map-*, hv-qa-*):
// per-name reads, the managed index blocks and the map size stats.
package mapqa

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/frontmatter"
	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/pystr"
	"github.com/l4ci/rota/internal/rotatree"
	"github.com/l4ci/rota/internal/section"
	"github.com/l4ci/rota/internal/strutil"
)

// Query returns the body (without frontmatter) of each named file under dir
// ("map" or "qa", below .rota/), in argument order, separated by a blank line.
// Names with no file are returned in missing; an empty body prints nothing.
func Query(root, dir string, names []string) (text string, missing []string, err error) {
	var b strings.Builder
	first := true
	for _, name := range names {
		if name == "" || strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
			missing = append(missing, name)
			continue
		}
		raw, err := fsio.ReadText(rotatree.Doc(root, dir, name))
		if os.IsNotExist(err) {
			missing = append(missing, name)
			continue
		}
		if err != nil {
			return "", nil, err
		}
		_, _, body := frontmatter.Parse(raw)
		body = strings.TrimRight(body, "\n")
		if body == "" {
			continue
		}
		if !first {
			b.WriteString("\n")
		}
		b.WriteString(body + "\n")
		first = false
	}
	return b.String(), missing, nil
}

// Entry is one parsed .rota/map/*.md file.
type Entry struct {
	Name string
	FM   map[string]any
	Body string
	Path string
}

// Entries lists the map files under root that declare a `subsystem:`, sorted by it.
func Entries(root string) []Entry {
	files := fsio.GlobDir(rotatree.File(root, rotatree.MapDir), "*.md")
	sort.Strings(files)
	var out []Entry
	for _, f := range files {
		raw, err := fsio.ReadText(f)
		if err != nil {
			continue
		}
		fm, _, body := frontmatter.Parse(raw)
		if name := frontmatter.Str(fm, "subsystem"); name != "" {
			out = append(out, Entry{name, fm, body, f})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Touched is the effective touch date of e: its `touched:` frontmatter when
// that parses as a date, else the date of the file's last git commit in root;
// ok is false when neither gives one.
func (e Entry) Touched(root string) (time.Time, bool) {
	if d, ok := pystr.ParseDate(frontmatter.Str(e.FM, "touched")); ok {
		return d, true
	}
	rel, err := filepath.Rel(root, e.Path)
	if err != nil {
		rel = e.Path
	}
	out, ok, err := git.Repo{Dir: root}.LastCommitDate(context.Background(), rel)
	if err != nil || !ok {
		return time.Time{}, false
	}
	return pystr.ParseDate(out)
}

// MapIndexBlock is the body of the rota-map managed block.
func MapIndexBlock(root string) string {
	entries := Entries(root)
	bullets := "- _(no subsystems yet — write `.rota/map/<name>.md` as you discover subsystems)_"
	if len(entries) > 0 {
		lines := make([]string, len(entries))
		for i, e := range entries {
			lines[i] = fmt.Sprintf("- **%s** — %s", e.Name, strutil.OrDefault(frontmatter.Str(e.FM, "summary"), "(no summary)"))
		}
		bullets = strings.Join(lines, "\n")
	}
	return "## Project Map\n\nSubsystems live in `.rota/MAP.md` (detail in `.rota/map/<name>.md`). Pull with `rota map query <name>`.\n\n" + bullets
}

// QAIndexBlock is the body of the rota-qa managed block.
func QAIndexBlock(root string) string {
	files := fsio.GlobDir(rotatree.File(root, rotatree.QADir), "*.md")
	sort.Strings(files)
	bullets := "- _(no QA strategy yet — run `/rota-qa first-run` to scaffold)_"
	if len(files) > 0 {
		var lines []string
		for _, f := range files {
			raw, err := fsio.ReadText(f)
			if err != nil {
				continue
			}
			fm, _, _ := frontmatter.Parse(raw)
			name := strings.TrimSuffix(filepath.Base(f), ".md")
			lines = append(lines, fmt.Sprintf("- **%s** (%s) — %s", name, strutil.OrDefault(frontmatter.Str(fm, "surface"), "?"), strutil.OrDefault(frontmatter.Str(fm, "summary"), "(no summary)")))
		}
		bullets = strings.Join(lines, "\n")
	}
	return "## Project QA\n\nQA strategies live in `.rota/QA.md` (detail in `.rota/qa/<target>.md`). Pull with `rota qa query <target>`. `/rota-qa run` consumes these; the skill never hardcodes runners.\n\n" + bullets
}

// WriteIndex upserts body as the key block of the project's instructions file.
func WriteIndex(root, key, body string) (string, error) {
	return section.UpsertManaged(root, key, strings.TrimRight(body, "\n"))
}

// Subsystem is one row of Stats.
type Subsystem struct {
	Name        string
	Bytes       int64
	Touched     string
	EntryPoints int
	BrokenRefs  int
	// CommitsBehind is how many commits HEAD is past the file's
	// last_mapped_commit; nil when the file has none or git cannot count.
	CommitsBehind *int
}

var entryRe = regexp.MustCompile(`(?m)^- ([^:\s]+):(\d+)\b`)

// Stats measures every map file: size, last touch, and how many of its
// "Entry points" references no longer resolve to a line of a file.
func Stats(root string) []Subsystem {
	out := []Subsystem{}
	for _, e := range Entries(root) {
		fi, err := os.Stat(e.Path)
		if err != nil {
			continue
		}
		touched := ""
		if d, ok := e.Touched(root); ok {
			touched = d.Format("2006-01-02")
		}
		refs := entryRe.FindAllStringSubmatch(section.Body(e.Body, "Entry points"), -1)
		broken := 0
		for _, r := range refs {
			if refBroken(root, r[1], r[2]) {
				broken++
			}
		}
		out = append(out, Subsystem{e.Name, fi.Size(), touched, len(refs), broken, commitsBehind(root, frontmatter.Str(e.FM, "last_mapped_commit"))})
	}
	return out
}

var shaRe = regexp.MustCompile(`^[0-9a-f]{4,64}$`)

// commitsBehind counts the commits past sha; nil when sha is empty, not a hash
// or not in this history (a rewritten branch), so the subsystem is skipped.
func commitsBehind(root, sha string) *int {
	if !shaRe.MatchString(sha) {
		return nil
	}
	n, ok, _ := git.Repo{Dir: root}.CommitsSince(context.Background(), sha)
	if !ok {
		return nil
	}
	return &n
}

func refBroken(root, file, line string) bool {
	p := file
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	raw, err := fsio.ReadText(p)
	if err != nil {
		return true
	}
	n, err := strconv.Atoi(line)
	if err != nil {
		return true
	}
	lines := strings.Count(raw, "\n")
	if len(raw) > 0 && raw[len(raw)-1] != '\n' {
		lines++
	}
	return n < 1 || n > lines
}

// SoftCap reads map.softcap_subsystems from cfg, the loaded project config: the subsystem
// count at which the map nudge fires. A value below 1 or not an integer gives
// the schema default.
func SoftCap(cfg any) int {
	return config.IntOr(cfg, "map.softcap_subsystems", 1, config.MaxInt)
}

// StaleCommits reads map.staleCommits from cfg, the loaded project config: 0 disables the
// staleness warning; a negative or non-integer value gives the schema default.
func StaleCommits(cfg any) int {
	return config.IntOr(cfg, "map.staleCommits", 0, config.MaxInt)
}

// StaleNote is the warning for a subsystem whose map is behind HEAD by n commits.
func StaleNote(name string, n, limit int) string {
	return fmt.Sprintf("map %s is %d commits behind HEAD (map.staleCommits %d); re-run /rota-refactor --map", name, n, limit)
}

// CapNote is the nudge printed when count reaches cap.
func CapNote(count, cap int) string {
	return fmt.Sprintf("project map has %d subsystems (cap %d); consider merging or retiring stale .rota/map/<name>.md entries", count, cap)
}
