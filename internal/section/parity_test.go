package section

import (
	"math/rand"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/pytest"
)

type kase struct {
	Content string `json:"content"`
	Name    string `json:"name"`
	Body    string `json:"body"`
}

func goResult(c kase) map[string]any {
	r := map[string]any{
		"body":    Body(c.Content, c.Name),
		"replace": Replace(c.Content, c.Name, c.Body),
		"append":  Append(c.Content, c.Name, c.Body),
	}
	if s, e, ok := Find(c.Content, c.Name); ok {
		r["find"] = []int{s, e}
	} else {
		r["find"] = nil
	}
	topics := [][]string{}
	for _, tp := range Topics(c.Content) {
		topics = append(topics, []string{tp.Name, tp.Body})
	}
	r["topics"] = topics
	return r
}

func tableCases() []kase {
	docs := []string{
		"",
		"## Bugs\n",
		"## Bugs",
		"## Bugs\n\n- **[B01] [P1] T.** d\n\n## Features\n\n- x\n",
		"# Backlog\n\n## Bugs  \n\n- a\n## Features\n- b\n",
		"## Bugs\t\r\n- a\r\n## Features\r\n",
		"## Bugs\n\n\n\n## Tasks\n",
		"## Bugs and more\n- a\n## Bugs\n- b\n",
		"x ## Bugs\n## Bugs \n- nbsp\n",
		"## Bugs \n\n \n- em space\n## Features",
		"## Bugs\x1c\n- sep\n",
		"## Bugs \n \n \n## Features\n",
		"## Bugs\n## Features\n## Tasks\n",
		"## Bugs\n\n## \n## x\n##y\n",
		"preamble\n\n## Café\ntext\n\n## A.B\n- dot\n## AxB\n",
		"## Bugs\n- no trailing newline",
		"## Bugs\n```\n## inside fence\n```\n## Features\n",
		"## Bugs\n\n\n",
		"\n\n## Bugs\n",
		"## Bugs\r\n\r\n- a\r\n",
		"## Dup\n1\n## Dup\n2\n",
		"## Bugs\n   \n\t\n## Features\n",
		"## Bugs  x\n## Bugs\n",
	}
	names := []string{"Bugs", "Features", "Tasks", "Missing", "A.B", "Café", "Dup", "Bugs and more", "", " Bugs"}
	bodies := []string{"", "\n- new\n", "text", "\n\n### sub\n\n**Status:** planned\n"}
	var out []kase
	for _, d := range docs {
		for _, n := range names {
			out = append(out, kase{d, n, bodies[len(out)%len(bodies)]})
		}
	}
	return out
}

func generated(n int) []kase {
	rng := rand.New(rand.NewSource(48))
	heads := []string{"## Bugs", "## Features", "## Tasks", "## Completed", "## Bugs ", "## Bugs\t", "## Bugs ", "##  Bugs", "## Bugsy", "## ", "##", "## x y", "### Bugs", "## Café", "## Bugs\x1f", "## Bugs "}
	lines := []string{"", "", "- **[B01] [P1] T.** d", "text", "   ", "\t", "- ~~**[F02] x.**~~ Done 2026-01-01 [`abc`]", "```", " ", "## inner", "é"}
	ends := []string{"\n", "\n", "\n", "\r\n", "\r", ""}
	names := []string{"Bugs", "Features", "Tasks", "Completed", "x y", "Café", "Missing"}
	bodies := []string{"", "\n- new\n", "text", "\n\n"}
	var out []kase
	for i := 0; i < n; i++ {
		var b strings.Builder
		for j := rng.Intn(9); j >= 0; j-- {
			if rng.Intn(3) == 0 {
				b.WriteString(heads[rng.Intn(len(heads))])
			} else {
				b.WriteString(lines[rng.Intn(len(lines))])
			}
			if j > 0 || rng.Intn(2) == 0 {
				b.WriteString(ends[rng.Intn(len(ends))])
			}
		}
		out = append(out, kase{b.String(), names[rng.Intn(len(names))], bodies[rng.Intn(len(bodies))]})
	}
	return out
}

func TestMatchesPython(t *testing.T) {
	cases := append(tableCases(), generated(600)...)
	got := make([]any, len(cases))
	inputs := make([]any, len(cases))
	for i, c := range cases {
		got[i], inputs[i] = goResult(c), c
	}
	var want []any
	pytest.GoldenJSON(t, cases, &want)
	n := pytest.Compare(t, "section", inputs, got, want)
	t.Logf("compared %d cases (find, body, replace, append, topics)", n)
}

func TestFindEdges(t *testing.T) {
	// \s* may eat newlines but must stop at a line end: the body keeps its leading "\n".
	s, e, ok := Find("## A \n\nx\n## B\n", "A")
	if !ok || s != 6 || e != 9 {
		t.Fatalf("Find = %d,%d,%v", s, e, ok)
	}
	if _, _, ok := Find("## Ab\n", "A"); ok {
		t.Fatal("prefix of another heading must not match")
	}
}
