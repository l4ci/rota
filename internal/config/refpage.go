package config

import (
	_ "embed"
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/jsonx"
)

//go:generate go run ./genref ../../docs/reference/config-options.md

//go:embed reference.tmpl.md
var referenceTmpl string

// ReferencePage is docs/reference/config-options.md: the static prose of
// reference.tmpl.md with the key tables, one per group in schema order,
// generated from Keys.
func ReferencePage() string {
	return strings.Replace(referenceTmpl, "{{KEYS}}", keyTables(), 1)
}

func keyTables() string {
	var groups []string
	byGroup := map[string][]Key{}
	for _, key := range Keys {
		if _, seen := byGroup[key.Group]; !seen {
			groups = append(groups, key.Group)
		}
		byGroup[key.Group] = append(byGroup[key.Group], key)
	}
	var b strings.Builder
	b.WriteString("## Keys\n\nEvery key, by group. Default is what a missing key reads as; Values lists the allowed values of an enum key.\n")
	for _, g := range groups {
		fmt.Fprintf(&b, "\n### %s\n\n| Key | Type | Default | Values | Description |\n|-----|------|---------|--------|-------------|\n", g)
		for _, key := range byGroup[g] {
			values := ""
			if len(key.Choices) > 0 {
				values = "`" + strings.Join(key.Choices, "`, `") + "`"
			}
			fmt.Fprintf(&b, "| `%s` | %s | %s | %s | %s |\n", key.Name, key.Type, defaultCell(key), values, cell(key.Desc))
		}
	}
	return b.String()
}

// defaultCell is the default as JSON in code font, or "unset" for an empty
// string, which every such key reads as "not configured".
func defaultCell(key Key) string {
	if s, ok := key.Default.(string); ok && s == "" {
		return "unset"
	}
	raw, err := jsonx.MarshalCompact(Default(key))
	if err != nil {
		raw = []byte(fmt.Sprint(key.Default))
	}
	return "`" + cell(string(raw)) + "`"
}

func cell(s string) string { return strings.ReplaceAll(s, "|", `\|`) }
