package extknowledge

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/l4ci/rota/internal/bulletfmt"
)

// MigrateChange is one bullet whose trailing "(seen on …)" moves into its
// stamp. Seen is the whole seen: value the bullet ends up with; Inferred marks
// a suffix that named no tool, which took the topic's slug.
type MigrateChange struct {
	Topic, Title, Seen string
	Inferred           bool
}

// MigrateReview is a "(seen on …)" the migration leaves for a person.
type MigrateReview struct{ Topic, Title, Text string }

// MigrateReport is what MigrateSeen found. Changed is true only when an apply
// committed. Stranded is true when nothing was left to change but the clone
// has commits its upstream lacks (an earlier apply whose push failed).
type MigrateReport struct {
	Changed      bool
	Stranded     bool
	Changes      []MigrateChange
	ManualReview []MigrateReview
	Write        WriteResult
}

var (
	trailingSeenRe = regexp.MustCompile(`\s*\(seen on ([^()]+)\)(\.?)$`)
	anySeenRe      = regexp.MustCompile(`\(seen on [^()]*\)`)
	versionOnlyRe  = regexp.MustCompile(`^[a-z]?[0-9][0-9a-z.+-]*$`)
)

// toolSlug is the slug a bare "(seen on 4.6)" takes as its tool: a subtopic
// takes its split topic's folder name.
func (t Topic) toolSlug() string {
	if t.Parent != "" {
		return path.Base(path.Dir(t.File))
	}
	return t.Slug
}

// topicFiles lists every indexed topic and subtopic whose file exists, in
// index order, each split topic's stray flat file right after it.
func (c Clone) topicFiles() ([]topicFileRef, error) {
	w, err := c.walk(nil, false)
	return w.Files, err
}

// MigrateSeen moves every trailing "(seen on Godot 4.6)" body suffix into the
// bullet's seen: stamp field. Without apply it only reports; with apply it
// needs a clean, current clone and lands one commit by the policy. A suffix
// that is not trailing, sits on a continuation line or does not parse goes to
// ManualReview and is left in place.
func (c Clone) MigrateSeen(ctx context.Context, apply bool) (res MigrateReport, err error) {
	if !apply {
		return c.migrateSeen(ctx, false)
	}
	err = c.mutate(ctx, func() error {
		res, err = c.migrateSeen(ctx, true)
		return err
	})
	return res, err
}

func (c Clone) migrateSeen(ctx context.Context, apply bool) (MigrateReport, error) {
	var pol Policy
	if apply {
		var err error
		if pol, err = c.Policy(); err != nil {
			return MigrateReport{}, err
		}
	}
	files, err := c.topicFiles()
	if err != nil {
		return MigrateReport{}, err
	}
	var rep MigrateReport
	p := &plan{message: "knowledge: move (seen on …) into seen: stamps", slug: "seen-stamps"}
	for _, f := range files {
		if f.Stray {
			continue // never written
		}
		lines := strings.Split(f.Content, "\n")
		touched := false
		for i, l := range lines {
			if !strings.HasPrefix(l, "- ") && !(strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t")) {
				continue
			}
			if !strings.HasPrefix(l, "- ") {
				// A continuation line: left alone, flagged when it holds a suffix.
				if m := anySeenRe.FindString(l); m != "" {
					rep.ManualReview = append(rep.ManualReview, MigrateReview{f.Topic.Name, owner(lines, i), m + " on a continuation line"})
				}
				continue
			}
			nl, ch, reviews, err := migrateLine(f.Topic, l)
			if err != nil {
				return MigrateReport{}, err
			}
			rep.ManualReview = append(rep.ManualReview, reviews...)
			if ch != nil {
				rep.Changes = append(rep.Changes, *ch)
				lines[i] = nl
				touched = true
			}
		}
		if touched {
			p.set(f.Rel, strings.Join(lines, "\n"))
		}
	}
	if !apply {
		return rep, nil
	}
	if len(p.order) == 0 {
		if ahead, _, ok := c.divergence(ctx); ok && ahead > 0 {
			rep.Stranded = true
		}
		rep.Write.Policy = pol
		return rep, nil
	}
	rep.Write, err = c.land(ctx, pol, p)
	rep.Changed = err == nil
	return rep, err
}

// owner is the title of the bullet a continuation line at index i belongs to.
func owner(lines []string, i int) string {
	for j := i - 1; j >= 0; j-- {
		if strings.HasPrefix(lines[j], "- ") {
			return titleOrLine(lines[j])
		}
	}
	return ""
}

// migrateLine rewrites one bullet line. ch is nil when the line is unchanged.
// A malformed seen: token already in the stamp is an error: merging over it
// would read it as "no prior versions" and drop it.
func migrateLine(t Topic, line string) (out string, ch *MigrateChange, reviews []MigrateReview, err error) {
	title := titleOrLine(line)
	body, stamp := bulletfmt.SplitStamp(line)
	text := strings.TrimRight(body, " ")
	m := trailingSeenRe.FindStringSubmatchIndex(text)
	if m == nil {
		for _, s := range anySeenRe.FindAllString(text, -1) {
			reviews = append(reviews, MigrateReview{t.Name, title, s + " is not trailing"})
		}
		return line, nil, reviews, nil
	}
	rest, raw, dot := text[:m[0]], text[m[2]:m[3]], text[m[4]:m[5]]
	for _, s := range anySeenRe.FindAllString(rest, -1) {
		reviews = append(reviews, MigrateReview{t.Name, title, s + " is not trailing"})
	}
	found, inferred, err := parseSuffix(raw, t.toolSlug())
	if err != nil {
		reviews = append(reviews, MigrateReview{t.Name, title, "(seen on " + raw + "): " + err.Error()})
		return line, nil, reviews, nil
	}
	var have Seen
	if stamp != "" {
		b := bullet{}
		parseStamp(&b, strings.TrimSpace(stamp))
		if b.SeenErr != nil {
			return line, nil, reviews, fmt.Errorf("topic %s bullet %q: bad seen stamp: %w", t.Name, title, b.SeenErr)
		}
		have = b.Seen
	}
	merged := append(Seen{}, have...)
	conflict := false
	for _, tv := range found {
		kept := false
		for _, h := range have {
			if h.Tool == tv.Tool {
				kept = true
				if h.Version != tv.Version {
					conflict = true
					reviews = append(reviews, MigrateReview{t.Name, title, fmt.Sprintf("stamp says %s@%s, suffix says %s@%s", h.Tool, h.Version, tv.Tool, tv.Version)})
				}
			}
		}
		if !kept {
			merged = append(merged, tv)
		}
	}
	if conflict {
		return line, nil, reviews, nil
	}
	if dot != "" && !strings.HasSuffix(rest, ".") && !strings.HasSuffix(rest, "!") && !strings.HasSuffix(rest, "?") {
		rest += "."
	}
	out = rest + " " + editStamp(strings.TrimSpace(stamp), "", &merged)
	return out, &MigrateChange{Topic: t.Name, Title: title, Seen: merged.String(), Inferred: inferred}, reviews, nil
}

// parseSuffix reads "Swift 6, macOS 26" into tool versions. A part that is
// only a version takes the topic slug and sets inferred.
func parseSuffix(raw, slug string) (Seen, bool, error) {
	var out Seen
	inferred := false
	for _, part := range strings.Split(raw, ",") {
		fields := strings.Fields(strings.ToLower(part))
		if len(fields) == 0 {
			return nil, false, fmt.Errorf("empty entry")
		}
		version := fields[len(fields)-1]
		if !versionOnlyRe.MatchString(version) {
			return nil, false, fmt.Errorf("%q has no version", strings.TrimSpace(part))
		}
		tool := strings.Join(fields[:len(fields)-1], "-")
		if tool == "" {
			tool, inferred = slug, true
		}
		out = append(out, ToolVersion{tool, version})
	}
	check, err := ParseSeen(out.String())
	if err != nil {
		return nil, false, err
	}
	return check, inferred, nil
}
