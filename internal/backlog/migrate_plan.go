package backlog

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/l4ci/rota/internal/frontmatter"
	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/pystr"
	"github.com/l4ci/rota/internal/section"
)

// The planning half of `rota migrate issues` (bin/hv-migrate-issues): what the
// file backlog holds that has to move to the tracker. It reads .rota/ and
// writes nothing.

type migItem struct {
	id, kind, title, tag, desc string
	// fields are Related, Repos, Subsystem and Captured, in that order; an
	// empty value is absent.
	fields    map[string]string
	since     string
	milestone string
	body      *string
	proof     *string
	design    *string
	plan      *string
	unmapped  []string
}

type migSlice struct{ unit, text string }

type migMilestone struct {
	id, title, summary, status, text string
	depends                          []string
	slices                           []migSlice
}

var (
	migSectionKind = map[string]string{"Bugs": "bugs", "Features": "features", "Tasks": "tasks"}
	migKindTags    = map[string][]string{"bugs": {"P0", "P1", "P2", "P3"}, "features": {"Major", "Minor", "Cosmetic"}, "tasks": nil}
	migFieldCut    = regexp.MustCompile(`[` + pystr.SpaceClass + `](?:Detail|Related|Milestone|Repos|Subsystem|Captured|Since):`)
	migMilestoneID = regexp.MustCompile(`\AM\p{Nd}+`)
	migGoalRe      = regexp.MustCompile(`(?ms)^## Goal[` + pystr.SpaceClass + `]*\n+(.+?)(?:\n[` + pystr.SpaceClass + `]*\n|\z)`)
	migHeadingRe   = regexp.MustCompile(`(?m)^# M\p{Nd}+[` + pystr.SpaceClass + `]*[\x{2014}\x{2013}-][` + pystr.SpaceClass + `]*(.+)$`)
	migMSAllRe     = regexp.MustCompile(`M\p{Nd}+`)
)

var migOrder = []string{"Related", "Repos", "Subsystem", "Captured"}

// readOpt is a file's text, or nil when it cannot be read.
func readOpt(path string) *string {
	t, err := fsio.ReadText(path)
	if err != nil {
		return nil
	}
	return &t
}

// planItems is the open Bugs, Features and Tasks bullets of BACKLOG.md with
// their detail, proof, design and plan text. warn gets the drop notices.
func planItems(root, backlogText string, warn func(string)) []*migItem {
	rota := filepath.Join(root, ".rota")
	var items []*migItem
	for _, e := range OpenBullets(backlogText) {
		kind := migSectionKind[e.Section]
		m := openRe.FindStringSubmatch(e.Line)
		if kind == "" || m == nil {
			continue
		}
		tag := m[2]
		if tag != "" && !has(migKindTags[kind], tag) {
			warn(e.ID + ": tag [" + tag + "] is not valid for " + kind + "; dropped")
			tag = ""
		}
		rest := m[4]
		desc := rest
		if loc := migFieldCut.FindStringIndex(rest); loc != nil {
			desc = rest[:loc[0]]
		}
		it := &migItem{id: e.ID, kind: kind, tag: tag, desc: pystr.Strip(desc), fields: map[string]string{}}
		it.title = pystr.Strip(strings.TrimRight(pystr.Strip(m[3]), "."))
		for _, name := range migOrder {
			v := pystr.Strip(strings.TrimRight(pystr.Strip(e.Fields.Get(strings.ToLower(name))), "."))
			if v != "" {
				it.fields[name] = v
			}
		}
		it.milestone = migMilestoneID.FindString(pystr.Strip(e.Fields.Milestone))
		it.since = pystr.Strip(e.Fields.Since)
		if t, ok := TypeByLetter(e.ID[:1]); ok {
			if d := readOpt(filepath.Join(rota, t.Kind, e.ID+".md")); d != nil {
				detail := *d
				if s, end, ok := section.Find(detail, "Proof"); ok {
					if at := strings.LastIndex(detail[:s], "## Proof"); at >= 0 {
						proof := pystr.Strip(detail[at:end])
						it.proof = &proof
						detail = strings.Trim(detail[:at]+detail[end:], "\n")
					}
				}
				if pystr.Strip(detail) != "" {
					b := strings.Trim(detail, "\n")
					it.body = &b
				}
			}
		}
		it.design = readOpt(filepath.Join(rota, "designs", e.ID+".md"))
		plans, _ := os.ReadDir(filepath.Join(rota, "plans"))
		var names []string
		for _, p := range plans {
			names = append(names, p.Name())
		}
		sort.Strings(names)
		planRe := regexp.MustCompile(`\AM\p{Nd}+-` + regexp.QuoteMeta(e.ID) + `\.md\z`)
		for _, n := range names {
			if planRe.MatchString(n) {
				it.plan = readOpt(filepath.Join(rota, "plans", n))
				break
			}
		}
		items = append(items, it)
	}
	// Related refs to IDs that are not migrated (completed or archived
	// items) would dangle and could collide with future issue numbers: drop
	// them from the field and keep them as one body line.
	migrating := map[string]bool{}
	for _, it := range items {
		migrating[it.id] = true
	}
	for _, it := range items {
		rel := it.fields["Related"]
		var toks []string
		seen := map[string]bool{}
		for _, t := range tokenMatches(rel) {
			if !seen[t.id] {
				seen[t.id] = true
				toks = append(toks, t.id)
			}
		}
		for _, t := range toks {
			if !migrating[t] {
				it.unmapped = append(it.unmapped, t)
			}
		}
		if len(it.unmapped) == 0 {
			continue
		}
		var kept []string
		for _, t := range toks {
			if migrating[t] {
				kept = append(kept, "["+t+"]")
			}
		}
		if len(kept) > 0 {
			it.fields["Related"] = strings.Join(kept, ", ")
		} else {
			delete(it.fields, "Related")
		}
		line := "Related before migration (not migrated): " + strings.Join(it.unmapped, ", ")
		if it.body != nil {
			b := *it.body + "\n\n" + line
			it.body = &b
		} else {
			it.body = &line
		}
	}
	return items
}

// planMilestones is the planned and active milestones of .rota/milestones with
// their slice plans.
func planMilestones(root string) []*migMilestone {
	rota := filepath.Join(root, ".rota")
	entries, _ := os.ReadDir(filepath.Join(rota, "milestones"))
	var names []string
	for _, e := range entries {
		n := e.Name()
		if strings.HasPrefix(n, "M") && strings.HasSuffix(n, ".md") {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	var out []*migMilestone
	for _, name := range names {
		text := ""
		if t := readOpt(filepath.Join(rota, "milestones", name)); t != nil {
			text = *t
		}
		fm, _, body := frontmatter.Parse(text)
		mid := frontmatter.Str(fm, "id")
		if mid == "" {
			mid = strings.TrimSuffix(name, ".md")
		}
		status := frontmatter.Str(fm, "status")
		if status != "planned" && status != "active" {
			continue
		}
		title := frontmatter.Str(fm, "title")
		if title == "" {
			if hm := migHeadingRe.FindStringSubmatch(body); hm != nil {
				title = pystr.Strip(hm[1])
			} else {
				title = mid
			}
		}
		summary := title
		if gm := migGoalRe.FindStringSubmatch(body); gm != nil {
			summary = strings.Join(strings.FieldsFunc(gm[1], pystr.IsSpace), " ")
		}
		ms := &migMilestone{id: mid, title: title, summary: summary, status: status, text: text}
		switch d := fm["depends"].(type) {
		case string:
			ms.depends = migMSAllRe.FindAllString(d, -1)
		case []string:
			ms.depends = migMSAllRe.FindAllString(strings.Join(d, ", "), -1)
		}
		plans, _ := os.ReadDir(filepath.Join(rota, "plans"))
		var pn []string
		for _, p := range plans {
			pn = append(pn, p.Name())
		}
		sort.Strings(pn)
		sliceRe := regexp.MustCompile(`\A` + regexp.QuoteMeta(mid) + `-(S\p{Nd}+)\.md\z`)
		for _, n := range pn {
			if sm := sliceRe.FindStringSubmatch(n); sm != nil {
				t := readOpt(filepath.Join(rota, "plans", n))
				txt := ""
				if t != nil {
					txt = *t
				}
				ms.slices = append(ms.slices, migSlice{sm[1], txt})
			}
		}
		out = append(out, ms)
	}
	return out
}

// ---- item ID tokens ---------------------------------------------------------

type tokenMatch struct {
	start, end int
	id         string
}

// tokenMatches finds the item IDs in s the way the old TOKEN pattern did:
// (?<![\w/-])([BFT]\d+)(?!\w)(?!\.md).
func tokenMatches(s string) []tokenMatch {
	var out []tokenMatch
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		if n == 1 && strings.IndexByte(ItemLetters, s[i]) >= 0 {
			prev, _ := utf8.DecodeLastRuneInString(s[:i])
			if i == 0 || !(pystr.IsWord(prev) || prev == '/' || prev == '-') {
				j := i + 1
				for j < len(s) {
					d, dn := utf8.DecodeRuneInString(s[j:])
					if !pystr.IsDigit(d) {
						break
					}
					j += dn
				}
				if j > i+1 {
					next, _ := utf8.DecodeRuneInString(s[j:])
					if (j == len(s) || !pystr.IsWord(next)) && !strings.HasPrefix(s[j:], ".md") {
						out = append(out, tokenMatch{i, j, s[i:j]})
						i = j
						continue
					}
				}
			}
		}
		_ = r
		i += n
	}
	return out
}

var bracketedRe = regexp.MustCompile(`\[([` + ItemLetters + `]\p{Nd}+)\]`)
