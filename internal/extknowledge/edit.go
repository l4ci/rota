package extknowledge

import (
	"context"
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/bulletfmt"
)

// edit is a topic file open for one write: the clone is clean and current, the
// policy is read, and lines hold the file for the caller to change.
type edit struct {
	c     Clone
	pol   Policy
	topic Topic
	lines []string
}

// beginEdit reads the policy and topic file while mutate owns a clean,
// current checkout.
func (c Clone) beginEdit(ctx context.Context, name string) (*edit, error) {
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
func (c Clone) Remove(ctx context.Context, in RmIn) (res WriteResult, err error) {
	err = c.mutate(ctx, func() error {
		res, err = c.remove(ctx, in)
		return err
	})
	return res, err
}

func (c Clone) remove(ctx context.Context, in RmIn) (WriteResult, error) {
	e, err := c.beginEdit(ctx, in.Topic)
	if err != nil {
		return WriteResult{}, err
	}
	bs := e.bullets()
	titles := make([]string, len(bs))
	for i, b := range bs {
		titles[i] = b.Title
	}
	i, err := bulletfmt.ResolveTitle(titles, in.Title)
	if err != nil {
		return WriteResult{}, fmt.Errorf("%w (in %q)", err, in.Topic)
	}
	e.lines = bulletfmt.DeleteBullet(e.lines, bs[i].Line)
	return e.land(ctx, fmt.Sprintf("knowledge: remove %q from %s", strings.TrimSpace(in.Title), in.Topic))
}

// Replace swaps in.Old for in.New inside the title and body of the one bullet
// that holds in.Old, then lands it. The stamp is not searched or changed, so
// an Old that touches it matches nothing. Old equal to New is no change.
func (c Clone) Replace(ctx context.Context, in ReplaceIn) (res WriteResult, err error) {
	if err := bulletfmt.CheckLine("replacement", in.New); err != nil {
		return WriteResult{}, err
	}
	err = c.mutate(ctx, func() error {
		res, err = c.replace(ctx, in)
		return err
	})
	return res, err
}

func (c Clone) replace(ctx context.Context, in ReplaceIn) (WriteResult, error) {
	e, err := c.beginEdit(ctx, in.Topic)
	if err != nil {
		return WriteResult{}, err
	}
	hit, err := e.selectFragment(in.Old,
		fmt.Errorf("%w: %q is in several bullets of %q; use a longer fragment", ErrMultiMatch, in.Old, in.Topic),
		fmt.Errorf("%w: no bullet in %q has %q in its title or body", ErrNotFound, in.Topic, in.Old))
	if err != nil {
		return WriteResult{}, err
	}
	end := bulletfmt.End(e.lines, hit)
	edited := bulletfmt.ReplaceIn(e.lines[hit:end], in.Old, in.New)
	if err := bulletfmt.CheckMarker(edited[0]); err != nil {
		return WriteResult{}, err
	}
	if strings.Join(edited, "\n") == strings.Join(e.lines[hit:end], "\n") {
		pol, err := c.Policy()
		return WriteResult{Policy: pol}, err
	}
	copy(e.lines[hit:end], edited)
	text, _ := bulletfmt.SplitStamp(edited[0])
	return e.land(ctx, fmt.Sprintf("knowledge: replace text in %q of %s", titleOrLine(text), in.Topic))
}

// selectFragment is the one bullet the shared fragment rule
// (bulletfmt.SelectFragment) picks in the open file, or the refusal: several
// when more than one matches, none when nothing does. A split topic's
// sub-index lines are not bullets.
func (e *edit) selectFragment(fragment string, several, none error) (int, error) {
	var hits []int
	for _, i := range bulletfmt.SelectFragment(e.lines, fragment) {
		if !(e.topic.Split && isSubIndexLine(e.lines[i])) {
			hits = append(hits, i)
		}
	}
	switch len(hits) {
	case 0:
		return -1, none
	case 1:
		return hits[0], nil
	}
	return -1, several
}

// titleOrLine is the bold title of a bullet line, else the line itself.
func titleOrLine(line string) string {
	if title, ok := bulletfmt.Title(line); ok {
		return title
	}
	return strings.TrimPrefix(line, "- ")
}

// editStamp rewrites a bullet stamp ("<!-- … -->", or empty) token by token:
// date replaces the date, a non-nil seen replaces or drops the seen: field.
// Every other token (source: and anything else) is kept as it is.
func editStamp(stamp, date string, seen *Seen) string {
	var toks []string
	haveDate, haveSeen := false, false
	for _, t := range bulletfmt.StampFields(stamp) {
		switch {
		case bulletfmt.IsDate(t) && date != "":
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
