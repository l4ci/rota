package extknowledge

import (
	"context"
	"fmt"
	"strings"
)

// edit is a topic file open for one write: the clone is clean and current, the
// policy is read, and lines hold the file for the caller to change.
type edit struct {
	c     Clone
	pol   Policy
	topic Topic
	lines []string
}

// beginEdit is the start of every write that changes an existing bullet: the
// clean-clone and divergence refusals, the policy and the topic file.
func (c Clone) beginEdit(ctx context.Context, name string) (*edit, error) {
	if err := c.syncForWrite(ctx); err != nil {
		return nil, err
	}
	pol, err := c.Policy()
	if err != nil {
		return nil, err
	}
	t, raw, err := c.topicFile(name)
	if err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, fmt.Errorf("%w: topic file %s does not exist", ErrNotFound, t.File)
	}
	return &edit{c: c, pol: pol, topic: t, lines: strings.Split(string(raw), "\n")}, nil
}

// land writes the edited file and lands it by the policy.
func (e *edit) land(ctx context.Context, message string) (WriteResult, error) {
	p := &plan{message: message, slug: e.topic.Slug}
	p.set(e.topic.File, strings.Join(e.lines, "\n"))
	return e.c.land(ctx, e.pol, p)
}

// bullets lists the bullets of the open file; a split topic's sub-index lines
// are not bullets.
func (e *edit) bullets() []bullet { return e.topic.bullets(strings.Join(e.lines, "\n")) }

// Remove deletes the one bullet whose bold title equals in.Title
// (case-insensitive), with its indented continuation lines, then lands it.
func (c Clone) Remove(ctx context.Context, in RmIn) (WriteResult, error) {
	e, err := c.beginEdit(ctx, in.Topic)
	if err != nil {
		return WriteResult{}, err
	}
	want := strings.ToLower(strings.TrimSpace(in.Title))
	hit := -1
	for _, b := range e.bullets() {
		if b.Title != "" && strings.ToLower(b.Title) == want {
			if hit >= 0 {
				return WriteResult{}, fmt.Errorf("%w: several bullets in %q are titled %q", ErrMultiMatch, in.Topic, in.Title)
			}
			hit = b.Line
		}
	}
	if hit < 0 {
		return WriteResult{}, fmt.Errorf("%w: no bullet in %q is titled %q", ErrNotFound, in.Topic, in.Title)
	}
	end := hit + 1
	for end < len(e.lines) && e.lines[end] != "" && (e.lines[end][0] == ' ' || e.lines[end][0] == '\t') {
		end++
	}
	rest := e.lines[end:]
	// Removing the only line between two blank lines would leave two in a row.
	if hit > 0 && e.lines[hit-1] == "" && len(rest) > 0 && rest[0] == "" {
		rest = rest[1:]
	}
	e.lines = append(e.lines[:hit:hit], rest...)
	return e.land(ctx, fmt.Sprintf("knowledge: remove %q from %s", strings.TrimSpace(in.Title), in.Topic))
}

// Replace swaps in.Old for in.New inside the title and body of the one bullet
// that holds in.Old, then lands it. The stamp is not searched or changed, so
// an Old that touches it matches nothing. Old equal to New is no change.
func (c Clone) Replace(ctx context.Context, in ReplaceIn) (WriteResult, error) {
	if err := checkLine("replacement", in.New); err != nil {
		return WriteResult{}, err
	}
	e, err := c.beginEdit(ctx, in.Topic)
	if err != nil {
		return WriteResult{}, err
	}
	hit := -1
	var stamp string
	for _, b := range e.bullets() {
		if strings.Contains(strings.TrimSuffix(e.lines[b.Line], b.Stamp), in.Old) {
			if hit >= 0 {
				return WriteResult{}, fmt.Errorf("%w: %q is in several bullets of %q; use a longer fragment", ErrMultiMatch, in.Old, in.Topic)
			}
			hit, stamp = b.Line, b.Stamp
		}
	}
	if hit < 0 {
		return WriteResult{}, fmt.Errorf("%w: no bullet in %q has %q in its title or body", ErrNotFound, in.Topic, in.Old)
	}
	text := strings.ReplaceAll(strings.TrimSuffix(e.lines[hit], stamp), in.Old, in.New)
	if !strings.HasPrefix(text, "- ") {
		return WriteResult{}, fmt.Errorf("%w: the replacement would remove the bullet marker", ErrContent)
	}
	line := text + stamp
	if line == e.lines[hit] {
		pol, err := c.Policy()
		return WriteResult{Policy: pol}, err
	}
	e.lines[hit] = line
	return e.land(ctx, fmt.Sprintf("knowledge: replace text in %q of %s", titleOrLine(text), in.Topic))
}

// titleOrLine is the bold title of a bullet line, else the line itself.
func titleOrLine(line string) string {
	if m := titleRe.FindStringSubmatch(line); m != nil {
		return m[1]
	}
	return strings.TrimPrefix(line, "- ")
}

// editStamp rewrites a bullet stamp ("<!-- … -->", or empty) token by token:
// date replaces the date, a non-nil seen replaces or drops the seen: field.
// Every other token (source: and anything else) is kept as it is.
func editStamp(stamp, date string, seen *Seen) string {
	inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(stamp, "<!--"), "-->"))
	var toks []string
	haveDate, haveSeen := false, false
	for _, t := range strings.Fields(inner) {
		switch {
		case dateRe.MatchString(t) && date != "":
			if !haveDate {
				toks = append(toks, date)
			}
			haveDate = true
		case strings.HasPrefix(t, "seen:") && seen != nil:
			if !haveSeen && len(*seen) > 0 {
				toks = append(toks, "seen:"+seen.String())
			}
			haveSeen = true
		default:
			toks = append(toks, t)
		}
	}
	if date != "" && !haveDate {
		toks = append([]string{date}, toks...)
	}
	if seen != nil && !haveSeen && len(*seen) > 0 {
		toks = append(toks, "seen:"+seen.String())
	}
	return "<!-- " + strings.Join(toks, " ") + " -->"
}
