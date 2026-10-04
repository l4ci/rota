package backlog

import (
	"errors"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/golden"
)

// project is one generated .rota/ tree.
type project struct {
	Root      string   `json:"root"`
	Backlog   string   `json:"backlog"`
	Archive   string   `json:"archive"`
	Detail    string   `json:"detail"` // content for .rota/bugs/B07.md and friends, "" = none
	IDs       []string `json:"ids"`
	NoBacklog bool     `json:"noBacklog"`
}

func (p project) write(t *testing.T) {
	rota := filepath.Join(p.Root, ".rota")
	if err := os.MkdirAll(filepath.Join(rota, "bugs"), 0o755); err != nil {
		t.Fatal(err)
	}
	put := func(name, content string) {
		if err := os.WriteFile(filepath.Join(rota, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if !p.NoBacklog {
		put("BACKLOG.md", p.Backlog)
	}
	if p.Archive != "" {
		put("ARCHIVE.md", p.Archive)
	}
	if p.Detail != "" {
		put("bugs/B07.md", p.Detail)
		put("bugs/b07x.md", p.Detail)
	}
}

func genProjects(t *testing.T, n int) []project {
	rng := rand.New(rand.NewSource(11))
	lines := genLines(48, 400)
	ids := []string{"B07", "F12", "T3", "F100", "B7", "B٧", "X1", "B01", "B02", "Z9", "B07 ", "", "b07", "B0"}
	var out []project
	for i := 0; i < n; i++ {
		mk := func(count int) string {
			var b strings.Builder
			b.WriteString("# Backlog\n\n")
			for j := 0; j < count; j++ {
				if rng.Intn(6) == 0 {
					b.WriteString(pick(rng, []string{"## Bugs", "## Features", "## Tasks", "## Completed", ""}))
				} else {
					b.WriteString(lines[rng.Intn(len(lines))])
				}
				b.WriteString("\n")
			}
			s := b.String()
			if i%4 == 0 {
				s = strings.ReplaceAll(s, "\n", "\r\n")
			}
			return s
		}
		p := project{Root: filepath.Join(t.TempDir(), "proj"), Backlog: mk(rng.Intn(25)), IDs: ids}
		if i%3 != 0 {
			p.Archive = mk(rng.Intn(25))
		}
		if i%5 == 0 {
			p.Detail = "# detail\r\nbody\r\n\rlast"
		}
		p.NoBacklog = i%11 == 0
		out = append(out, p)
	}
	return out
}

func TestFileMatchesPython(t *testing.T) {
	projects := genProjects(t, 60)
	for _, p := range projects {
		p.write(t)
	}
	// Roots are fresh temp dirs; the golden records the inputs without them.
	recorded := make([]project, len(projects))
	copy(recorded, projects)
	for i := range recorded {
		recorded[i].Root = ""
	}
	var got []any
	items := 0
	for _, p := range projects {
		f := &File{Root: p.Root}
		r := map[string]any{"corpus": f.Corpus(), "items": []any{}, "detail": []any{}}
		if md, err := f.Markdown(-1); err == nil {
			r["markdown"] = md
		} else if !errors.Is(err, ErrNotFound) {
			t.Fatalf("Markdown: %v", err)
		} else {
			r["markdown"] = nil
		}
		for _, id := range p.IDs {
			it, err := f.Get(id)
			if err != nil {
				if !errors.Is(err, ErrNotFound) {
					t.Fatalf("Get(%q): %v", id, err)
				}
				r["items"] = append(r["items"].([]any), nil)
				continue
			}
			items++
			m := map[string]any{}
			for k, v := range fieldsMap(it.Fields) {
				m[k] = v
			}
			m["reason"], m["note"], m["title"], m["tag"] = it.Reason, it.Note, it.Title, it.Tag
			m["closed"], m["line"] = it.Closed, it.Line
			r["items"] = append(r["items"].([]any), m)
			if it.ID != id || it.Number != 0 || it.URL != "" {
				t.Errorf("Get(%q): ID/Number/URL = %q/%d/%q", id, it.ID, it.Number, it.URL)
			}
		}
		for _, id := range p.IDs {
			text, ok, err := f.Detail(id)
			if err != nil {
				t.Fatal(err)
			}
			if ok {
				r["detail"] = append(r["detail"].([]any), text)
			} else {
				r["detail"] = append(r["detail"].([]any), nil)
			}
		}
		got = append(got, r)
	}
	if items < 100 {
		t.Fatalf("generator found only %d items; the test is too weak", items)
	}
	golden.Check(t, map[string]any{"input": recorded}, got)
	t.Logf("compared %d projects (%d items found) against FileBackend.fields/detail_text/backlog_markdown and load_backlog_corpus", len(projects), items)
}

func TestFileItemShape(t *testing.T) {
	root := t.TempDir()
	p := project{Root: root, Backlog: "## Bugs\n\n- **[B07] [P1] Title. Part two.** body Milestone: M01\n- **[B08] [P2] Open.** x\n\n## Completed\n\n- ~~**[B09] [P3] Gone.** y~~ Done 2026-01-01 [`abc`] (blocked: need X)\n"}
	p.write(t)
	f := &File{Root: root}
	it, err := f.Get("B07")
	if err != nil {
		t.Fatal(err)
	}
	if it.Type != "B" || it.Tag != "P1" || it.Title != "Title. Part two" || it.Closed || it.Fields.Milestone != "M01" {
		t.Fatalf("B07 = %+v", it)
	}
	it, err = f.Get("B09")
	if err != nil || !it.Closed || it.Reason != "blocked" || it.Note != "need X" || it.Tag != "P3" {
		t.Fatalf("B09 = %+v, %v", it, err)
	}
	if _, err := f.Get("B7"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("B7 must not match B07, got %v", err)
	}
	if _, err := (&File{Root: t.TempDir()}).Markdown(0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing BACKLOG.md: %v", err)
	}
}

type counterScenario struct {
	Backlog  string   `json:"backlog"`
	Archive  string   `json:"archive"`
	Counters *string  `json:"counters"` // nil = no file
	Kinds    []string `json:"kinds"`
}

func TestNextIDMatchesPython(t *testing.T) {
	str := func(s string) *string { return &s }
	backlog := "## Bugs\n- **[B07] [P1] a.** x\n- **[B31] b.**\n## Features\n- **[F02] f.**\n"
	scen := []counterScenario{
		{backlog, "", nil, []string{"bugs", "features", "tasks", "milestones", "bugs"}},
		{backlog, "- ~~**[B50] z.**~~ Done 2026-01-01 [`a`]\n", str(`{"bugs": 3}`), []string{"bugs", "bugs", "features"}},
		{backlog, "", str(`{"tasks": 9, "bugs": 40, "features": 1}`), []string{"bugs", "tasks", "milestones", "features"}},
		{backlog, "", str(`{"since_refactor": {"bugs": 2}, "bugs": 5}`), []string{"bugs", "milestones"}},
		{backlog, "", str(`{"bugs": 99}`), []string{"bugs", "bugs"}},
		{backlog, "", str("{\n  \"bugs\": 1,\n  \"x\": [1, 2]\n}\n"), []string{"features", "bugs"}},
		{backlog, "", str(`not json`), []string{"bugs"}},
		{backlog, "", str(`[1, 2]`), []string{"bugs"}},
		{backlog, "", str(`{"bugs": "7"}`), []string{"bugs"}},
		{backlog, "", str(`{"bugs": null}`), []string{"bugs"}},
		{backlog, "", str(`{"bugs": 2.5}`), []string{"bugs"}},
		{backlog, "", nil, []string{"epics", "bugs"}},
		{"", "", nil, []string{"tasks", "tasks"}},
		{"[B٧٨] x [M12] [M3]", "", nil, []string{"bugs", "milestones"}},
		{"- **[B0001] a.**", "", nil, []string{"bugs"}},
		{"- **[B100] a.**", "", str(`{"bugs": 1}`), []string{"bugs"}},
	}
	var got []any
	ids := 0
	for _, s := range scen {
		root := t.TempDir()
		rota := filepath.Join(root, ".rota")
		os.MkdirAll(rota, 0o755)
		os.WriteFile(filepath.Join(rota, "BACKLOG.md"), []byte(s.Backlog), 0o644)
		if s.Archive != "" {
			os.WriteFile(filepath.Join(rota, "ARCHIVE.md"), []byte(s.Archive), 0o644)
		}
		if s.Counters != nil {
			os.WriteFile(filepath.Join(rota, "counters.json"), []byte(*s.Counters), 0o644)
		}
		f := &File{Root: root}
		var res []any
		for _, k := range s.Kinds {
			id, err := f.NextID(k)
			if err != nil {
				res = append(res, map[string]any{"err": true})
			} else {
				res = append(res, id)
				ids++
			}
		}
		r := map[string]any{"ids": res, "counters": nil}
		if raw, err := os.ReadFile(filepath.Join(rota, "counters.json")); err == nil {
			r["counters"] = string(raw)
		}
		got = append(got, r)
	}
	golden.Check(t, map[string]any{"input": scen}, got)
	t.Logf("compared %d counter scenarios (%d IDs minted), counters.json byte for byte", len(scen), ids)
}

// A fractional counter at or above every ID: Python writes the bumped float
// and then crashes formatting it. rota refuses and leaves the file alone.
func TestNextIDRefusesFractionalCounter(t *testing.T) {
	for _, raw := range []string{`{"bugs": 1e2}`, `{"bugs": 100.5, "x": 1}`} {
		root := t.TempDir()
		rota := filepath.Join(root, ".rota")
		os.MkdirAll(rota, 0o755)
		os.WriteFile(filepath.Join(rota, "BACKLOG.md"), []byte("## Bugs\n- **[B07] a.**\n"), 0o644)
		os.WriteFile(filepath.Join(rota, "counters.json"), []byte(raw), 0o644)
		if id, err := (&File{Root: root}).NextID("bugs"); err == nil {
			t.Fatalf("%s: NextID = %q, want an error", raw, id)
		}
		if got, _ := os.ReadFile(filepath.Join(rota, "counters.json")); string(got) != raw {
			t.Fatalf("%s: counters.json rewritten to %s", raw, got)
		}
	}
}

func ids(items []Item) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.ID)
	}
	return out
}

// Every listed item is what Get returns for its ID, on generated trees.
func TestFileListEqualsGet(t *testing.T) {
	listed := 0
	for i, p := range genProjects(t, 200) {
		p.write(t)
		f := &File{Root: p.Root}
		all, err := f.List(true)
		if p.NoBacklog {
			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("project %d: missing BACKLOG.md: %v", i, err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		open, err := f.List(false)
		if err != nil {
			t.Fatal(err)
		}
		if len(open) > len(all) || (len(open) > 0 && !sameJSON(open, all[:len(open)])) {
			t.Fatalf("project %d: List(false) is not the prefix of List(true)", i)
		}
		seen := map[string]bool{}
		for j, it := range all {
			want, err := f.Get(it.ID)
			if err != nil {
				t.Fatalf("project %d item %d: Get(%q): %v", i, j, it.ID, err)
			}
			if !reflect.DeepEqual(&all[j], want) {
				t.Fatalf("project %d: List()[%d] = %+v, Get(%q) = %+v", i, j, it, it.ID, *want)
			}
			if seen[it.ID] {
				t.Fatalf("project %d: %q listed twice", i, it.ID)
			}
			seen[it.ID] = true
			listed++
		}
	}
	if listed < 100 {
		t.Fatalf("only %d items listed; the generator is too weak", listed)
	}
}

func TestFileListOrder(t *testing.T) {
	root := t.TempDir()
	p := project{Root: root,
		Backlog: "## Bugs\n\n- **[B02] [P1] Two.** x\n- **[B01] [P2] One.** y\n\n## Features\n\n- **[F05] [Major] Five.** z\n\n## Tasks\n\n- **[T01] Task.** t\n\n" +
			"## Completed\n\n- ~~**[B09] [P3] Nine.** n~~ Done 2026-01-01 [`abc`]\n- ~~**[B02] [P1] Two.** x~~ Done 2026-01-02 [`abd`]\n- ~~**[B08] Eight.**~~ Done 2026-01-03 [`abe`] (dropped: no)\n",
		Archive: "- ~~**[B09] [P3] Nine.** n~~ Done 2026-01-01 [`abc`]\n- ~~**[F01] [Minor] Old.** o~~ Done 2025-01-01 [`old`]\n"}
	p.write(t)
	f := &File{Root: root}
	open, err := f.List(false)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(ids(open), ","); got != "B02,B01,F05,T01" {
		t.Fatalf("open = %s", got)
	}
	all, _ := f.List(true)
	// B02 is open in the backlog and also on a done line: listed once, first.
	if got := strings.Join(ids(all), ","); got != "B02,B01,F05,T01,B09,B08,F01" {
		t.Fatalf("all = %s", got)
	}
	if all[5].Reason != "dropped" || all[5].Note != "no" || !all[5].Closed {
		t.Fatalf("B08 = %+v", all[5])
	}
}
