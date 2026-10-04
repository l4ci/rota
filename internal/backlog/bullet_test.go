package backlog

import (
	"math/rand"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/pytest"
)

type bulletIn struct {
	Lines   []string    `json:"lines"`
	Sets    [][3]string `json:"sets"`
	Dones   [][5]string `json:"dones"`
	Origins [][2]string `json:"origins"`
	IDs     [][2]string `json:"ids"`
	Docs    []string    `json:"docs"`
}

func TestBulletParityWithPython(t *testing.T) {
	lines := genLines(48, 300)
	rng := rand.New(rand.NewSource(7))
	in := bulletIn{Lines: lines}

	values := []string{"", " ", "x", "M07", "`p`", " `.rota/a.md` ", "a b", "[F01], [B02]", "``", "é`", "x\ny"}
	for _, l := range lines {
		for k := 0; k < 3; k++ {
			in.Sets = append(in.Sets, [3]string{l, pick(rng, SettableFields), pick(rng, values)})
		}
	}
	in.Sets = append(in.Sets, [3]string{"- **[B1] t.** x", "title", "y"}, [3]string{"- **[B1] t.** x", "since", "y"}, [3]string{"", "milestone", "M1"})

	for _, l := range lines {
		in.Dones = append(in.Dones, [5]string{l, "2026-01-02", "abc1234", pick(rng, ClosureReasons), pick(rng, []string{"", "waiting on x", "a: b", "—"})})
	}
	in.Dones = append(in.Dones, [5]string{"- x", "d", "h", "", ""}, [5]string{"- x", "d", "h", "weird", "n"})

	ids := []string{"B01", "B07", "B1", "F12", "T3", "B02", "B100", "X1", "B.1", "B+", "[B01]", ""}
	for i, l := range lines {
		corpus := l + "\n" + lines[(i*7+1)%len(lines)] + "\n" + pick(rng, tricky)
		if i%5 == 0 {
			corpus = strings.ReplaceAll(corpus, "\n", "\r\n")
		}
		in.Origins = append(in.Origins, [2]string{corpus, pick(rng, ids)})
		in.Origins = append(in.Origins, [2]string{corpus, "B07"}, [2]string{corpus, "B01"})
	}
	for _, l := range lines {
		in.IDs = append(in.IDs, [2]string{l + " [B07] [F12] [B07] [T3] [T03] [X10]", pick(rng, []string{"", "", "BFT", "B", "F", "BFTM", "M"})})
	}
	in.IDs = append(in.IDs, [2]string{"[B07][F12][B٧٨] [M01] [B-1] [B007]", ""}, [2]string{"[M01] [M2] [M003]", "M"})

	// Whole documents: sections, headings in odd places, Completed lines.
	secs := []string{"## Bugs", "## Features", "## Tasks", "## Completed", "## Other", "## Bugs "}
	for d := 0; d < 150; d++ {
		var b strings.Builder
		for j := rng.Intn(14); j >= 0; j-- {
			switch rng.Intn(4) {
			case 0:
				b.WriteString(pick(rng, secs))
			default:
				b.WriteString(lines[rng.Intn(len(lines))])
			}
			b.WriteString(pick(rng, []string{"\n", "\n", "\n\n", "\r\n", " ", "\x0b", "\u0085"}))
		}
		in.Docs = append(in.Docs, b.String())
	}

	var want struct {
		Fields  []any `json:"fields"`
		Open    []any `json:"open"`
		Done    []any `json:"done"`
		Set     []any `json:"set"`
		Format  []any `json:"format"`
		Origin  []any `json:"origin"`
		IDs     []any `json:"ids"`
		Docs    []any `json:"docs"`
		Skipped any   `json:"-"`
	}
	pytest.GoldenJSON(t, in, &want)

	total := 0
	check := func(name string, inputs []any, got []any, want []any) {
		total += pytest.Compare(t, name, inputs, got, want)
	}
	lineIn := toAny(lines)
	var got []any

	for _, l := range lines {
		got = append(got, fieldsMap(ParseFields(l)))
	}
	check("parse_todo_fields", lineIn, got, want.Fields)

	got = nil
	for _, l := range lines {
		if b, ok := ParseOpen(l); ok {
			got = append(got, map[string]string{"id": b.ID, "tag": b.Tag, "title": b.Title, "rest": b.Rest})
		} else {
			got = append(got, nil)
		}
	}
	check("parse_open_bullet", lineIn, got, want.Open)

	got = nil
	for _, l := range lines {
		if d, ok := ParseDone(l); ok {
			got = append(got, map[string]string{"id": d.ID, "inner": d.Inner, "date": d.Date, "hash": d.Hash, "reason": d.Reason, "note": d.Note})
		} else {
			got = append(got, nil)
		}
	}
	check("parse_done_line", lineIn, got, want.Done)

	got = nil
	for _, s := range in.Sets {
		if r, err := SetField(s[0], s[1], s[2]); err != nil {
			got = append(got, map[string]string{"err": err.Error()})
		} else {
			got = append(got, r)
		}
	}
	check("set_todo_field", toAny(in.Sets), got, want.Set)

	got = nil
	for _, d := range in.Dones {
		if r, err := FormatDone(d[0], d[1], d[2], d[3], d[4]); err != nil {
			got = append(got, map[string]string{"err": err.Error()})
		} else {
			got = append(got, r)
		}
	}
	check("format_done_line", toAny(in.Dones), got, want.Format)

	got = nil
	for _, o := range in.Origins {
		if l, title, ok := FindOrigin(o[0], o[1]); ok {
			got = append(got, []string{l, title})
		} else {
			got = append(got, nil)
		}
	}
	check("find_origin_bullet", toAny(in.Origins), got, want.Origin)

	got = nil
	for _, x := range in.IDs {
		ids := FindItemIDs(x[0], x[1])
		if ids == nil {
			ids = []string{}
		}
		got = append(got, ids)
	}
	check("find_item_ids", toAny(in.IDs), got, want.IDs)

	got = nil
	for _, d := range in.Docs {
		entries := []any{}
		for _, e := range OpenBullets(d) {
			entries = append(entries, []any{e.ID, e.Line, fieldsMap(e.Fields), e.Section})
		}
		got = append(got, entries)
	}
	check("iter_open_bullets", toAny(in.Docs), got, want.Docs)
	t.Logf("compared %d cases across 8 Python functions", total)
}

func toAny[T any](xs []T) []any {
	out := make([]any, len(xs))
	for i, x := range xs {
		out[i] = x
	}
	return out
}

func TestFieldsGetAndSettableErrors(t *testing.T) {
	f := ParseFields("- **[B1] t.** Detail: `d` Milestone: M1 Since: abc")
	if f.Get("detail") != "`d`" || f.Get("milestone") != "M1" || f.Get("since") != "abc" || f.Get("nope") != "" {
		t.Fatalf("Get: %+v", f)
	}
	if _, err := SetField("- **[B1] t.**", "since", "x"); err == nil ||
		err.Error() != "since is not a settable field; pick one of milestone/related/repos/subsystem/detail" {
		t.Fatalf("err = %v", err)
	}
	if got, _ := SetField("- **[B1] t.** x Milestone: M1 Repos: web", "detail", "p"); got != "- **[B1] t.** x Detail: `p` Milestone: M1 Repos: web" {
		t.Fatalf("detail insert order: %q", got)
	}
}
