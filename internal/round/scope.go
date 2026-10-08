package round

import (
	"fmt"
	"sort"
	"strings"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/itembody"
)

// Scopes parses the `## Touches` section of an item body: one symbol,
// endpoint, schema, migration or config key per bullet, backticks optional. Entries are trimmed, lower-cased, deduplicated and sorted.
func Scopes(body string) []string {
	sec, ok := itembody.Section(body, itembody.TouchesHeadRe)
	if !ok {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, line := range strings.Split(sec, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "- ") && !strings.HasPrefix(line, "* ") {
			continue
		}
		line = line[2:]
		line = strings.ToLower(strings.TrimSpace(strings.Trim(strings.TrimSpace(line), "`")))
		if line == "" || seen[line] {
			continue
		}
		seen[line] = true
		out = append(out, line)
	}
	sort.Strings(out)
	return out
}

// ScopeOverlaps lists the scopes two lists share, compared exactly.
func ScopeOverlaps(a, b []string) []string {
	in := map[string]bool{}
	for _, x := range a {
		in[x] = true
	}
	var out []string
	for _, y := range b {
		if in[y] {
			out = append(out, y)
			delete(in, y)
		}
	}
	sort.Strings(out)
	return out
}

// itemScopes is what an item declares it touches: its `## Touches` entries,
// else, when it carries a Subsystem field, the coarse scope subsystem:<name>.
// MAP.md is not read.
func itemScopes(be backlog.Backend, id string) []string {
	body, _, _ := be.Detail(id)
	if s := Scopes(body); len(s) > 0 {
		return s
	}
	if it, err := be.Get(id); err == nil {
		if sub := strings.ToLower(strings.TrimSpace(it.Fields.Get("subsystem"))); sub != "" {
			return []string{"subsystem:" + sub}
		}
	}
	return nil
}

// overlapDetail is the overlap check's line for one in-flight item.
func overlapDetail(with, slot string, paths, scopes []string) string {
	parts := make([]string, 0, 2)
	if len(paths) > 0 {
		parts = append(parts, strings.Join(paths, ", "))
	}
	if len(scopes) > 0 {
		parts = append(parts, "scopes "+strings.Join(scopes, ", "))
	}
	return fmt.Sprintf("%s (held by %s): %s", with, slot, strings.Join(parts, "; "))
}
