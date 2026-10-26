package extknowledge

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// SplitSubtopic is one subtopic of a split plan: its short name, the hook of
// its sub-index line and the bold titles of the bullets that move into it.
type SplitSubtopic struct {
	Name   string   `json:"name"`
	Hook   string   `json:"hook"`
	Titles []string `json:"titles"`
}

// PlanShape is the JSON a split plan takes.
const PlanShape = `{"subtopics":[{"name":"…","hook":"…","titles":["…"]}]}`

// SplitPlan says how a topic's bullets divide into subtopics. Bullets the plan
// does not list stay in the topic's README, below the sub-index.
type SplitPlan struct {
	Subtopics []SplitSubtopic `json:"subtopics"`
}

// SplitIn is one split. Plan may be nil only for a topic that is already
// split, to fold in a stray flat file.
type SplitIn struct {
	Topic string
	Plan  *SplitPlan
}

// SplitSub reports one planned subtopic as it stands after the split.
type SplitSub struct {
	Name    string
	File    string
	Bullets int
}

// SplitResult reports Split. Left counts the bullets still in the README;
// Folded counts the stray-file bullets moved into the folder or dropped as a
// byte-identical copy of one already there.
type SplitResult struct {
	Topic     string
	Subtopics []SplitSub
	Left      int
	Folded    int
}

// unit is one bullet of a topic file: its "- " line plus its indented
// continuation lines, kept byte for byte.
type unit struct {
	title string // the bold title, empty when the line has none
	text  string
}

// fileParts is a topic file taken apart: its "# " title line, the prose
// outside any bullet, a split README's sub-index lines and the bullets.
type fileParts struct {
	heading  string
	prose    []string
	subIndex []string
	units    []unit
}

func parseParts(body string, readme bool) fileParts {
	var p fileParts
	lines := strings.Split(body, "\n")
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		switch {
		case strings.TrimSpace(l) == "":
		case strings.HasPrefix(l, "- ") && readme && isSubIndexLine(l):
			p.subIndex = append(p.subIndex, l)
		case strings.HasPrefix(l, "- "):
			u := []string{l}
			for i+1 < len(lines) && lines[i+1] != "" && (lines[i+1][0] == ' ' || lines[i+1][0] == '\t') {
				i++
				u = append(u, lines[i])
			}
			var title string
			if m := titleRe.FindStringSubmatch(l); m != nil {
				title = m[1]
			}
			p.units = append(p.units, unit{title: title, text: strings.Join(u, "\n")})
		case p.heading == "" && strings.HasPrefix(l, "# "):
			p.heading = l
		default:
			p.prose = append(p.prose, l)
		}
	}
	return p
}

func unitTexts(us []unit) []string {
	out := make([]string, len(us))
	for i, u := range us {
		out[i] = u.text
	}
	return out
}

// render writes a topic file: heading, prose, sub-index lines, then bullets.
func (p fileParts) render() string {
	var b strings.Builder
	b.WriteString(p.heading + "\n")
	if len(p.prose) > 0 {
		b.WriteString("\n" + strings.Join(p.prose, "\n") + "\n")
	}
	if len(p.subIndex) > 0 {
		b.WriteString("\n" + strings.Join(p.subIndex, "\n") + "\n")
	}
	if len(p.units) > 0 {
		b.WriteString("\n" + strings.Join(unitTexts(p.units), "\n") + "\n")
	}
	return b.String()
}

// planSub is a SplitSubtopic after validation.
type planSub struct {
	name, slug, hook string
	titles           []string
}

// checkPlan validates the plan on its own: names, slugs, hooks and titles. It
// reads nothing from the clone.
func checkPlan(parent string, plan *SplitPlan) ([]planSub, error) {
	if plan == nil {
		return nil, fmt.Errorf("%w: splitting %q needs --plan <file>: %s; /rota-learn --external-review drafts one", ErrUsage, parent, PlanShape)
	}
	if len(plan.Subtopics) == 0 {
		return nil, fmt.Errorf("%w: the plan lists no subtopics: %s", ErrUsage, PlanShape)
	}
	var out []planSub
	slugs := map[string]string{}
	titles := map[string]string{}
	for _, s := range plan.Subtopics {
		name := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s.Name), parent+": "))
		if name == "" {
			return nil, fmt.Errorf("%w: a subtopic has an empty name", ErrUsage)
		}
		if strings.ContainsAny(name, "\r\n[]") || strings.Contains(name, ": ") {
			return nil, fmt.Errorf("%w: subtopic name %q must be one line without [, ] or %q", ErrUsage, name, ": ")
		}
		slug := Slugify(name)
		if slug == "" || slug == "readme" {
			return nil, fmt.Errorf("%w: subtopic %q has no usable file name", ErrUsage, name)
		}
		if prev, dup := slugs[slug]; dup {
			return nil, fmt.Errorf("%w: subtopics %q and %q share the file name %s.md", ErrUsage, prev, name, slug)
		}
		slugs[slug] = name
		hook := strings.TrimSpace(s.Hook)
		if err := checkLine("hook", hook); err != nil {
			return nil, err
		}
		if hook == "" {
			hook = name
		}
		if len(s.Titles) == 0 {
			return nil, fmt.Errorf("%w: subtopic %q lists no titles", ErrUsage, name)
		}
		ps := planSub{name: name, slug: slug, hook: hook}
		for _, t := range s.Titles {
			if strings.TrimSpace(t) == "" {
				return nil, fmt.Errorf("%w: subtopic %q lists an empty title", ErrUsage, name)
			}
			if at, dup := titles[t]; dup {
				return nil, fmt.Errorf("%w: title %q is in both %q and %q", ErrUsage, t, at, name)
			}
			titles[t] = name
			ps.titles = append(ps.titles, t)
		}
		out = append(out, ps)
	}
	return out, nil
}

// Split moves a topic's bullets into subtopic files behind a sub-index in the
// topic's README, in one commit. A flat topic becomes a folder
// (topics/<slug>/README.md plus topics/<slug>/<sub>.md) and its flat file goes;
// on a topic that is already split the plan adds subtopics or moves more
// README bullets. A stray flat file an older rota left beside a split topic
// folds into the README (or into the plan's subtopics) and goes with its root
// line. Every bullet line is kept; the plan is validated in full before
// anything is written. A rerun of the same plan changes nothing.
func (c Clone) Split(ctx context.Context, in SplitIn) (SplitResult, WriteResult, error) {
	if err := c.syncForWrite(ctx); err != nil {
		return SplitResult{}, WriteResult{}, err
	}
	pol, err := c.Policy()
	if err != nil {
		return SplitResult{}, WriteResult{}, err
	}
	topics, err := c.Topics()
	if err != nil {
		return SplitResult{}, WriteResult{}, err
	}
	t, ok := exact(in.Topic, topics)
	if !ok {
		return SplitResult{}, WriteResult{}, fmt.Errorf("%w: topic %q is not in %s", ErrNotFound, in.Topic, c.indexName())
	}
	if t.Parent != "" {
		return SplitResult{}, WriteResult{}, fmt.Errorf("%w: %q is a subtopic; name its parent %q", ErrUsage, t.Name, t.Parent)
	}
	res := SplitResult{Topic: t.Name}
	var subs []planSub
	if in.Plan != nil || !t.Split {
		if subs, err = checkPlan(t.Name, in.Plan); err != nil {
			return res, WriteResult{}, err
		}
	}
	read := func(rel string) (string, bool, error) {
		raw, err := c.readRegular(filepath.Join(c.Dir, filepath.FromSlash(rel)))
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return string(raw), err == nil, err
	}
	body, present, err := read(t.File)
	if err != nil {
		return res, WriteResult{}, err
	}
	if !present {
		return res, WriteResult{}, fmt.Errorf("%w: topic file %s does not exist", ErrNotFound, t.File)
	}
	main := parseParts(body, t.Split)
	if main.heading == "" {
		main.heading = "# " + t.Name
	}
	if !t.Split {
		for _, u := range main.units {
			if isSubIndexLine(strings.SplitN(u.text, "\n", 2)[0]) {
				return res, WriteResult{}, fmt.Errorf("%w: %s holds a bullet shaped like a sub-index line (%q); reword it first", ErrContent, t.File, strings.SplitN(u.text, "\n", 2)[0])
			}
		}
	}
	dir := strings.TrimSuffix(t.File, "/README.md")
	if !t.Split {
		dir = TopicsDir + "/" + t.Slug
	}

	// Where each title already lives, to catch duplicates and conflicts.
	owner := map[string]string{} // lowercase title -> file
	texts := map[string]string{} // lowercase title -> unit text
	note := func(file string, us []unit) error {
		for _, u := range us {
			if u.title == "" {
				continue
			}
			k := strings.ToLower(u.title)
			if prev, dup := owner[k]; dup {
				return fmt.Errorf("%w: bullet %q is in %s and in %s; resolve the duplicate by hand first", ErrContent, u.title, prev, file)
			}
			owner[k], texts[k] = file, u.text
		}
		return nil
	}
	if err := note(t.File, main.units); err != nil {
		return res, WriteResult{}, err
	}
	type existingSub struct {
		topic Topic
		parts fileParts
		stem  string
	}
	var existing []*existingSub
	for _, st := range t.Subtopics {
		sb, _, err := read(st.File)
		if err != nil {
			return res, WriteResult{}, err
		}
		es := &existingSub{topic: st, parts: parseParts(sb, false), stem: strings.TrimSuffix(path.Base(st.File), ".md")}
		if es.parts.heading == "" {
			es.parts.heading = "# " + st.Name
		}
		if err := note(st.File, es.parts.units); err != nil {
			return res, WriteResult{}, err
		}
		existing = append(existing, es)
	}

	// Fold the stray flat file: its bullets join the README pool.
	pool := append([]unit(nil), main.units...)
	var strayUnits, dropped []unit
	if t.Split && t.Stray != "" {
		if _, err := c.out(ctx, "ls-files", "--error-unmatch", "--", lit(t.Stray)); err != nil {
			return res, WriteResult{}, fmt.Errorf("%w: %s is untracked, so folding it could lose it; commit or remove it first", ErrDirty, t.Stray)
		}
		sb, _, err := read(t.Stray)
		if err != nil {
			return res, WriteResult{}, err
		}
		sp := parseParts(sb, false)
		seenInStray := map[string]bool{}
		for _, u := range sp.units {
			strayUnits = append(strayUnits, u)
			res.Folded++
			if u.title == "" {
				dup := false
				for _, p := range pool {
					dup = dup || p.text == u.text
				}
				if dup {
					dropped = append(dropped, u)
				} else {
					pool = append(pool, u)
				}
				continue
			}
			k := strings.ToLower(u.title)
			if seenInStray[k] {
				return res, WriteResult{}, fmt.Errorf("%w: bullet %q is twice in %s", ErrContent, u.title, t.Stray)
			}
			seenInStray[k] = true
			if prev, found := owner[k]; found {
				if texts[k] != u.text {
					return res, WriteResult{}, fmt.Errorf("%w: bullet %q is in %s and differently in %s; reconcile one by hand first", ErrContent, u.title, prev, t.Stray)
				}
				dropped = append(dropped, u)
				continue
			}
			pool = append(pool, u)
		}
		main.prose = append(main.prose, sp.prose...)
	}

	// Allocate the plan's titles from the pool.
	dest := make([]int, len(pool)) // plan index per pool unit, -1 stays in the README
	for i := range dest {
		dest[i] = -1
	}
	byTitle := map[string]int{}
	for i, u := range pool {
		if u.title != "" {
			byTitle[u.title] = i
		}
	}
	match := make([]*existingSub, len(subs))
	for i, s := range subs {
		for _, es := range existing {
			if es.stem == s.slug {
				match[i] = es
			}
		}
		for _, title := range s.titles {
			if idx, ok := byTitle[title]; ok && dest[idx] == -1 {
				dest[idx] = i
				continue
			}
			if match[i] != nil {
				done := false
				for _, u := range match[i].parts.units {
					done = done || u.title == title
				}
				if done {
					continue // a rerun: already in this subtopic
				}
			}
			return res, WriteResult{}, fmt.Errorf("%w: no bullet titled %q among the bullets of %s that are not in a subtopic yet", ErrNotFound, title, t.Name)
		}
	}

	// Build the new files.
	readme := main
	readme.units = nil
	for i, u := range pool {
		if dest[i] == -1 {
			readme.units = append(readme.units, u)
		}
	}
	p := &plan{slug: t.Slug}
	var finals []fileParts // every topic file as it ends up, for the line check
	moved := 0
	newSubs := 0
	for i, s := range subs {
		var add []unit
		for j, u := range pool {
			if dest[j] == i {
				add = append(add, u)
			}
		}
		moved += len(add)
		var parts fileParts
		var file, name string
		if es := match[i]; es != nil {
			parts, file, name = es.parts, es.topic.File, es.topic.Name
		} else {
			file, name = dir+"/"+s.slug+".md", t.Name+": "+s.name
			if _, err := os.Lstat(filepath.Join(c.Dir, filepath.FromSlash(file))); err == nil {
				return res, WriteResult{}, fmt.Errorf("%w: %s exists but %s does not list it", ErrContent, file, t.File)
			}
			parts = fileParts{heading: "# " + name}
			readme.subIndex = append(readme.subIndex, fmt.Sprintf("- [%s](%s.md) — %s", s.name, s.slug, s.hook))
			newSubs++
		}
		if len(add) > 0 || match[i] == nil {
			parts.units = append(append([]unit(nil), parts.units...), add...)
			p.set(file, parts.render())
		}
		finals = append(finals, parts)
		res.Subtopics = append(res.Subtopics, SplitSub{Name: name, File: file, Bullets: len(parts.units)})
	}
	res.Left = len(readme.units)

	changed := !t.Split || moved > 0 || newSubs > 0 || t.Stray != ""
	if !changed {
		return res, WriteResult{Policy: pol}, nil
	}
	finals = append(finals, readme)
	for _, es := range existing {
		known := false
		for _, m := range match {
			known = known || m == es
		}
		if !known {
			finals = append(finals, es.parts)
		}
	}

	// Safety net: every bullet line before is still there after, once.
	var before, after []string
	before = append(before, unitTexts(main.units)...)
	before = append(before, unitTexts(strayUnits)...)
	for _, d := range dropped {
		before = removeOne(before, d.text)
	}
	for _, es := range existing {
		before = append(before, unitTexts(es.parts.units)...)
	}
	for _, f := range finals {
		after = append(after, unitTexts(f.units)...)
	}
	sort.Strings(before)
	sort.Strings(after)
	if strings.Join(before, "\x00") != strings.Join(after, "\x00") {
		return res, WriteResult{}, fmt.Errorf("%w: split would change the bullet lines of %s; nothing written", ErrContent, t.Name)
	}

	// The root index points at the folder; a stray's root line goes.
	idxName := c.indexName()
	idx, _, err := read(idxName)
	if err != nil {
		return res, WriteResult{}, err
	}
	var idxLines []string
	for _, l := range strings.Split(idx, "\n") {
		m := indexRe.FindStringSubmatch(strings.TrimRight(l, " \t\r"))
		switch {
		case m != nil && m[2] == t.Slug && m[3] == ".md" && !t.Split:
			l = strings.Replace(l, "](topics/"+t.Slug+".md)", "](topics/"+t.Slug+"/README.md)", 1)
		case m != nil && m[2] == t.Slug && m[3] == ".md" && t.Split:
			continue
		}
		idxLines = append(idxLines, l)
	}
	if text := strings.Join(idxLines, "\n"); text != idx {
		p.set(idxName, text)
	}
	if t.Split {
		p.set(t.File, readme.render())
	} else {
		p.set(dir+"/README.md", readme.render())
		p.deletes = append(p.deletes, t.File)
	}
	if t.Stray != "" {
		p.deletes = append(p.deletes, t.Stray)
	}
	switch {
	case in.Plan == nil:
		p.message = fmt.Sprintf("knowledge: fold stray %s into %s", t.Stray, t.Name)
	case len(subs) == 1:
		p.message = fmt.Sprintf("knowledge: split %s into 1 subtopic", t.Name)
	default:
		p.message = fmt.Sprintf("knowledge: split %s into %d subtopics", t.Name, len(subs))
	}
	wr, err := c.land(ctx, pol, p)
	return res, wr, err
}

// removeOne drops the first element equal to s.
func removeOne(l []string, s string) []string {
	for i, x := range l {
		if x == s {
			return append(l[:i:i], l[i+1:]...)
		}
	}
	return l
}
