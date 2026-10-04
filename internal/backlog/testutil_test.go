package backlog

import (
	"encoding/json"
	"math/rand"
	"strings"
)

// sameJSON compares two values by their JSON encoding.
func sameJSON(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// pick returns a random element.
func pick(rng *rand.Rand, xs []string) string { return xs[rng.Intn(len(xs))] }

// tricky is the hand-written table: every shape the parsers have to survive.
var tricky = []string{
	"- **[B01] [P1] Title.** Body. Detail: foo. Related: [F02]. Milestone: M01",
	"- **[F01] [Major] Title.** D. Milestone: M02 Repos: web Since: a1b2c3d",
	"- **[B07] [P1] Title.** D. Repos: web Subsystem: capture Captured: 2026-05-09",
	"- **[B07] [P1] Title.** Since: abc Captured: 2026-01-01 Subsystem: x Repos: y Milestone: M3 Related: [B01] Detail: `p`",
	"- **[B02] [P2] Mentions Related and Detail words.** body Related:",
	"- **[B02] [P2] Empty related.** body Related: Milestone: M01",
	"- **[B02] [P2] Trailing colon.** body Detail:",
	"- **[B02] [P2] Trailing colon space.** body Detail: ",
	"- **[B02] [P2] Tab.** body\tMilestone:\tM01\tRepos:\tweb",
	"- **[B02] [P2] Nbsp.** body Milestone: M01 Repos: web",
	"- **[B02] [P2] Em dash — title.** body — text. Detail: `.rota/bugs/B02.md`",
	"- **[B02] [P2] Trailing spaces.**   body   Milestone: M01   ",
	"- **[B02] [P2] CRLF.** body Milestone: M01\r",
	"- **[B02] [P2] Two milestones.** Milestone: M01 Milestone: M02",
	"- **[B02] [P2] Glued.** XMilestone: M01 Milestone: M02",
	"- **[B02] [P2] Underscore.** _Milestone: M01",
	"- **[B02] [P2] Digit.** 5Milestone: M01",
	"- **[B02] [P2] Accent.** éMilestone: M01",
	"- **[B02] [P2] Mark.** éMilestone: M01",
	"- **[B02] [P2] Lowercase.** milestone: M01",
	"- **[B02] [P2] Newline inside.** body\nMilestone: M01",
	"- **[B02] [P2] Newline value.** Milestone:\nM01",
	"- **[B02] [P2] Newline end.** Milestone: M01\n",
	"- **[B02] [P2] Colon only.** Milestone::M01",
	"- **[B02] [P2] No space.** Milestone:M01 Repos:web",
	"- **[F12] No tag.** body",
	"- **[F12]   [Major]   Wide.**   body",
	"- **[T3] [x] a.b.c**",
	"- **[T3] No period**",
	"- **[T3] [p] Title with ** star.** x",
	"- **[X1] Unknown letter.** x",
	"- **[B٧] Arabic digit.** x Milestone: M01",
	"- **[B] No digits.** x",
	"-  **[B01] Two spaces.** x",
	"* **[B01] Star bullet.** x",
	"- ~~**[B01] [P1] Struck.** body Milestone: M01~~ Done 2026-01-01 [`abc1234`]",
	"- ~~**[B01] [P1] Struck.** body~~ Done 2026-01-01 [`abc1234`] (dropped)",
	"- ~~**[B01] [P1] Struck.** body~~ Done 2026-01-01 [`abc1234`] (blocked: waiting on X. Related: [B02])",
	"- ~~**[B01] [P1] Struck.** body~~ Done 2026-01-01 [`abc1234`] (handed-off: to F02)",
	"- ~~**[B01] [P1] Struck.** body~~ Done 2026-01-01 [`#12`]  ",
	"- ~~**[B01] [P1] Struck.** body~~ Done 2026-01-01 [`abc`] (done)",
	"- ~~**[B01] [P1] Struck.** body~~ Done 2026-01-01 [`abc`] (dropped: note—with dash)\r",
	"- ~~**[B01] [P1] Struck.** body~~ Done 2026-1-01 [`abc`]",
	"- ~~**[B01] [P1] Struck.** body~~ Done 2026-01-01 [``]",
	"- ~~**[B01] [P1] Struck.** body~~Done 2026-01-01 [`abc`]",
	"- ~~**[B01] [P1] Struck ~~ inner.** body~~ Done 2026-01-01 [`abc`]",
	"- ~~**[B01] [P1] Struck.** Done 2026-01-01 [`x`] body~~ Done 2026-01-01 [`abc`]",
	"- ~~**[B01] [P1] Struck.** body~~ Done ٢٠٢٦-01-01 [`abc`]",
	"- **[B01] [P1] Open with done words.** body Done 2026-01-01 [`abc`]",
	"- **[B01] [P1] Open with done words.** body Done 2026-01-01 [`abc`] (dropped)",
	"- ~~**[B01] [P1] Struck.** body~~ Done 2026-01-01 [`abc`] (unknown)",
	"- ~~**[B01]**~~ Done 2026-01-01 [`abc`]",
	"- ~~no id~~ Done 2026-01-01 [`abc`]",
	"- ~~**[b01] lower.**~~ Done 2026-01-01 [`abc`]",
	"not a bullet",
	"",
	"   ",
	"- ",
	"- **",
}

// Fragments for the generator.
var (
	genBullets = []string{
		"**[B07] [P1] Title.**", "**[F12] Title.**", "**[T3] [x] a.b.c**", "**[B07] Title with Related and Detail words.**",
		"**[B07] [P1] Title.**", "**[X1] t.**", "**[B٧] arabic.**", "**[F100] [Major] — dash.**",
		"**[B07] [P1]  Spaces .**", "**[B07] [] Empty tag.**", "**[B07] [a b] c d.**", "**[B07] [P1] Star * inside.**",
	}
	genDescs = []string{"", "body", "Body text.", "Related", "Detail", "see Milestone M01", "x Since y", "unicode é中文", "  padded  ", "a:b", "Related:", "—"}
	genNames = []string{"Detail", "Related", "Milestone", "Repos", "Subsystem", "Captured", "Since"}
	genVals  = []string{"foo.", "`.rota/bugs/B07.md`", "[F02], [B03]", "M01", "web, api", "capture", "2026-05-09", "a1b2c3d", "", " ", "x  y", "a:b", "Detail: nested", "é", "[B01].", "M01 M02"}
	genSeps  = []string{" ", " ", " ", "  ", "\t", " ", "  ", "\n"}
	genColon = []string{": ", ": ", ":", ":  ", ":\t", ":  ", ":\n"}
	genEnds  = []string{"", "", "", " ", "  ", "\r", "\n", "\t"}
	genDone  = []string{"", "", "", " Done 2026-01-01 [`abc1234`]", " Done 2026-01-01 [`#12`]", " Done 2026-01-01 [`abc`] (dropped)",
		" Done 2026-01-01 [`abc`] (blocked: waiting on x)", " Done 2026-01-01 [`abc`] (handed-off: y. Related: [B01])", " Done 2026-01-01 [`abc`] (done)",
		" Done 2026-01-01 [`abc`] (blocked:)", " Done 2026-01-01 [`abc`] (dropped: a) (b)", "  Done  2026-01-01  [`abc`]"}
)

// genLine builds one pseudo-random bullet line from the fragments.
func genLine(rng *rand.Rand) string {
	var b strings.Builder
	b.WriteString(pick(rng, []string{"- ", "- ", "- ", "- ~~", "-  ", "* ", ""}))
	b.WriteString(pick(rng, genBullets))
	if d := pick(rng, genDescs); d != "" {
		b.WriteString(pick(rng, genSeps))
		b.WriteString(d)
	}
	perm := rng.Perm(len(genNames))
	for _, i := range perm[:rng.Intn(len(genNames)+1)] {
		b.WriteString(pick(rng, genSeps))
		b.WriteString(genNames[i])
		b.WriteString(pick(rng, genColon))
		b.WriteString(pick(rng, genVals))
	}
	if strings.HasPrefix(b.String(), "- ~~") && rng.Intn(4) != 0 {
		b.WriteString("~~")
	}
	if strings.HasPrefix(b.String(), "- ~~") && rng.Intn(4) != 0 {
		b.WriteString(pick(rng, genDone[3:]))
	} else {
		b.WriteString(pick(rng, genDone))
	}
	b.WriteString(pick(rng, genEnds))
	return b.String()
}

// genLines returns the tricky table plus n generated lines.
func genLines(seed int64, n int) []string {
	rng := rand.New(rand.NewSource(seed))
	out := append([]string(nil), tricky...)
	for i := 0; i < n; i++ {
		out = append(out, genLine(rng))
	}
	return out
}

// fieldsMap renders Fields as parse_todo_fields' dict.
func fieldsMap(f Fields) map[string]string {
	out := map[string]string{}
	for _, n := range SettableFields {
		out[n] = f.Get(n)
	}
	out["captured"], out["since"] = f.Captured, f.Since
	return out
}
