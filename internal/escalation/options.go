package escalation

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// Choice is the decision-ready part of a question: the options on offer, the
// 1-based number of the recommended one and what picking wrongly would cost.
// The zero Choice is a question without options (rota's own gate asks).
type Choice struct {
	Options   []string
	Recommend int
	Cost      string
}

// render is the block Compose puts between the body and the ask; empty for a
// question without options.
func (c Choice) render() string {
	if len(c.Options) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Options:\n")
	for i, o := range c.Options {
		mark := ""
		if i+1 == c.Recommend {
			mark = " (recommended)"
		}
		fmt.Fprintf(&b, "%d) %s%s\n", i+1, o, mark)
	}
	if c.Recommend >= 1 && c.Recommend <= len(c.Options) {
		fmt.Fprintf(&b, "\nRecommended: %d) %s\n", c.Recommend, c.Options[c.Recommend-1])
	}
	if c.Cost != "" {
		fmt.Fprintf(&b, "Cost if wrong: %s\n", c.Cost)
	}
	b.WriteString("\nReply `ok` or `yes` to take the recommendation, or the option number or letter (A is 1).\n\n")
	return b.String()
}

// ResolveOption maps a reply to a 1-based option number, 0 when it names none.
// The whole reply must be `ok`, `yes`, a number, a letter A-D (as `/rota-answer` offers) or `option <number|letter>` (case and
// trailing punctuation aside): `ok` and `yes` take the recommendation. Anything
// longer is prose for the orchestrator to read, never guessed at.
func ResolveOption(options []string, recommend int, reply string) int {
	if len(options) == 0 {
		return 0
	}
	words := strings.Fields(strings.ToLower(strings.TrimRightFunc(strings.TrimSpace(reply), unicode.IsPunct)))
	if len(words) == 2 && words[0] == "option" {
		words = words[1:]
	}
	if len(words) != 1 {
		return 0
	}
	switch words[0] {
	case "ok", "yes":
		if recommend >= 1 && recommend <= len(options) {
			return recommend
		}
		return 0
	}
	if n, err := strconv.Atoi(words[0]); err == nil && n >= 1 && n <= len(options) {
		return n
	}
	if w := words[0]; len(w) == 1 && w[0] >= 'a' && int(w[0]-'a') < len(options) {
		return int(w[0]-'a') + 1
	}
	return 0
}

// RecommendedFirst returns the choice with the recommended option moved to
// position 1, as /rota-answer lists its options, and Recommend set to 1.
func (c Choice) RecommendedFirst() Choice {
	if c.Recommend < 1 || c.Recommend > len(c.Options) {
		return c
	}
	opts := []string{c.Options[c.Recommend-1]}
	for i, o := range c.Options {
		if i != c.Recommend-1 {
			opts = append(opts, o)
		}
	}
	c.Options, c.Recommend = opts, 1
	return c
}
