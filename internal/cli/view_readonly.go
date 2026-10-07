package cli

import (
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/tui"
)

// The four read-only --ui views of #546. Each turns one verb's Data into rows
// for the shared browser; none writes anything, and a row's full text comes
// from the same verb a typed command would run.

// doctorView lists the checks with failing rows first, then warnings, then
// passes, each with its hint inline. The reader repeats the selected row in
// full, for a hint the row cut short.
func doctorView(c *Ctx, res Result) (tui.Model, error) {
	var fails, warns, passes []browseRow
	for _, ch := range dataObjs(res.Data, "checks") {
		status, name, detail, hint := jsonx.Str(ch, "status"), jsonx.Str(ch, "name"), jsonx.Str(ch, "detail"), jsonx.Str(ch, "hint")
		label := fmt.Sprintf("%-4s  %s", strings.ToUpper(status), name)
		if detail != "" {
			label += "  " + detail
		}
		if hint != "" {
			label += "  → " + hint
		}
		preview := detail
		if hint != "" {
			preview += "\nhint: " + hint
		}
		row := browseRow{Label: label, Preview: preview, Key: name}
		switch status {
		case "fail":
			fails = append(fails, row)
		case "warn":
			warns = append(warns, row)
		default:
			passes = append(passes, row)
		}
	}
	rows := append(append(fails, warns...), passes...)
	title := fmt.Sprintf("doctor — %d failing, %d warning, %d passing", len(fails), len(warns), len(passes))
	return newBrowser(title, "no checks", rows, true, nil), nil
}

const barCells = 20

// headroomBar draws pct (0-100) as a bar of barCells cells.
func headroomBar(pct float64) string {
	pct = min(max(pct, 0), 100)
	n := int(pct*barCells/100 + 0.5)
	return "[" + strings.Repeat("█", n) + strings.Repeat("░", barCells-n) + "]"
}

// accountsView shows one row per account: a headroom bar, the verdict and when
// the usage window resets. An account with no reading shows its reason.
func accountsView(c *Ctx, res Result) (tui.Model, error) {
	var rows []browseRow
	for _, a := range dataObjs(res.Data, "accounts") {
		name, verdict, reason := jsonx.Str(a, "name"), jsonx.Str(a, "verdict"), jsonx.Str(a, "reason")
		bar, pct := strings.Repeat(" ", barCells+2), "   -"
		if h, ok := dataFloat(a, "headroom"); ok {
			bar, pct = headroomBar(h), fmt.Sprintf("%3.0f%%", h)
		}
		reset := "-"
		if r := jsonx.Str(a, "resetsAt"); r != "" {
			reset = "resets " + r
		}
		label := fmt.Sprintf("%-14s %s %s  %-9s %s", name, bar, pct, verdict, reset)
		preview := name + ": " + verdict
		for _, f := range []struct{ key, label string }{{"fiveHour", "5h used"}, {"sevenDay", "7d used"}} {
			if v, ok := dataFloat(a, f.key); ok {
				preview += fmt.Sprintf("\n%s: %.0f%%", f.label, v)
			}
		}
		if reason != "" {
			preview += "\n" + reason
		}
		if dir := jsonx.Str(a, "configDir"); dir != "" {
			preview += "\nconfig: " + dir
		}
		rows = append(rows, browseRow{Label: label, Preview: preview, Key: name})
	}
	return newBrowser("worker accounts", "no accounts configured (slots inherit the ambient CLAUDE_CONFIG_DIR)", rows, true, nil), nil
}

// backlogView lists the open items. The filter matches a row's whole label,
// which carries the id, type, priority or size, and milestone, so typing a
// label or a milestone narrows the list. Enter reads the item's body.
func backlogView(c *Ctx, res Result) (tui.Model, error) {
	var rows []browseRow
	add := func(o *jsonx.Object, kind, tag string) {
		id, title, ms := jsonx.Str(o, "id"), jsonx.Str(o, "title"), jsonx.Str(o, "milestone")
		kind = strings.TrimSpace(kind + " " + tag)
		label := fmt.Sprintf("%-6s %-10s", id, kind)
		if ms != "" {
			label += " " + ms
		}
		label += " " + title
		preview := id + "  " + kind + "\n" + title
		if ms != "" {
			preview += "\nmilestone: " + ms
		}
		if br := jsonx.Str(o, "branch"); br != "" {
			preview += "\nbranch: " + br
		}
		if rel, _ := o.Get("related"); rel != nil {
			if ids, _ := rel.([]any); len(ids) > 0 {
				var s []string
				for _, r := range ids {
					s = append(s, fmt.Sprint(r))
				}
				preview += "\nrelated: " + strings.Join(s, ", ")
			}
		}
		rows = append(rows, browseRow{Label: label, Preview: preview + "\n\nEnter reads the body.", Key: id})
	}
	for _, o := range dataObjs(res.Data, "inProgress") {
		add(o, "active", jsonx.Str(o, "type"))
	}
	for _, o := range dataObjs(res.Data, "bugs") {
		add(o, "bug", jsonx.Str(o, "priority"))
	}
	for _, o := range dataObjs(res.Data, "features") {
		add(o, "feature", jsonx.Str(o, "size"))
	}
	for _, o := range dataObjs(res.Data, "tasks") {
		add(o, "task", "")
	}
	load := func(id string) (string, error) {
		body, err := runVerbText(c, true, "item", "field", "get", "--name", "detail", "--", id)
		if err == nil && strings.TrimSpace(body) == "" {
			body = "(no body)"
		}
		return body, err
	}
	return newBrowser("backlog — / filters by label or milestone", "no open items", rows, false, load), nil
}

// topicRows turns a stats verb's topics into rows.
func topicRows(data any) []browseRow {
	var rows []browseRow
	for _, t := range dataObjs(data, "topics") {
		name := jsonx.Str(t, "name")
		v, _ := t.Get("bullets")
		bullets, _ := jsonx.Int(v)
		v, _ = t.Get("bytes")
		size, _ := jsonx.Int(v)
		rows = append(rows, browseRow{
			Label:   fmt.Sprintf("%s (%d)", name, bullets),
			Preview: fmt.Sprintf("%s\n%d bullets, %d bytes\n\nEnter reads the topic.", name, bullets, size),
			Key:     name,
		})
	}
	return rows
}

// knowledgeView lists KNOWLEDGE.md topics; Enter reads one in the reader.
func knowledgeView(c *Ctx, res Result) (tui.Model, error) {
	load := func(topic string) (string, error) {
		return runVerbText(c, true, "knowledge", "query", "--", topic)
	}
	return newBrowser("knowledge", "no topics in KNOWLEDGE.md", topicRows(res.Data), false, load), nil
}

// decisionsView lists DECISIONS.md topics; Enter reads one in the reader.
func decisionsView(c *Ctx, res Result) (tui.Model, error) {
	load := func(topic string) (string, error) {
		return runVerbText(c, false, "decisions", "query", "--", topic)
	}
	return newBrowser("decisions", "no topics in DECISIONS.md", topicRows(res.Data), false, load), nil
}
