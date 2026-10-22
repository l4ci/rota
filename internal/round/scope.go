package round

import (
	"fmt"
	"sort"
	"strings"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/itembody"
)

// touchEntry is one bullet of a `## Touches` section: Raw in the author's
// spelling, Norm as overlap compares it (see itembody.Entry). Hidden marks a
// bullet that mimics a signature or sentinel.
type touchEntry struct {
	Raw, Norm string
	Hidden    bool
}

// touchEntries is the one parser of a `## Touches` section: its bullets in
// source order, none when the section is missing. Empty and backtick-only
// bullets are no entry. A bullet that mimics a signature or sentinel
// (mimicsSentinel) is kept but Hidden: the brief omits it so an issue cannot
// spoof a signed sentinel inside a brief, while overlap keeps it because its
// output goes to the orchestrator and issues here name ROTA-DONE and similar
// as real identifiers; two that declare one must still clash.
func touchEntries(body string) []touchEntry {
	sec, ok := itembody.Section(body, itembody.TouchesHeadRe)
	if !ok {
		return nil
	}
	var out []touchEntry
	for _, l := range strings.Split(sec, "\n") {
		l = strings.TrimSpace(l)
		if !strings.HasPrefix(l, "- ") && !strings.HasPrefix(l, "* ") {
			continue
		}
		raw := strings.TrimSpace(l[2:])
		norm := itembody.Entry(raw)
		if norm == "" {
			continue
		}
		out = append(out, touchEntry{Raw: raw, Norm: norm, Hidden: mimicsSentinel(raw)})
	}
	return out
}

// Scopes is the overlap view of a `## Touches` section: one symbol,
// endpoint, schema, migration or config key per bullet, backticks optional.
// Entries are trimmed, lower-cased, deduplicated and sorted.
func Scopes(body string) []string {
	seen := map[string]bool{}
	var out []string
	for _, e := range touchEntries(body) {
		if !seen[e.Norm] {
			seen[e.Norm] = true
			out = append(out, e.Norm)
		}
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

// Scope sources: where an item's scope entries came from.
const (
	SourceTouches   = "touches"
	SourceSubsystem = "subsystem"
	SourceInferred  = "inferred"
	SourceNone      = "none"
)

// ScopeKind is the comment kind that records an explorer's inferred scope.
const ScopeKind = "scope"

// MaxInferred is the most entries a recorded inferred scope keeps.
const MaxInferred = 10

// itemScopes is what an item declares it touches: its `## Touches` entries,
// else, when it carries a Subsystem field, the coarse scope subsystem:<name>,
// else the inferred scope an explorer recorded. MAP.md is not read.
func itemScopes(be backlog.Backend, id string) []string {
	s, _ := ItemScopeSource(be, id)
	return s
}

// ItemScopeSource is itemScopes with where the entries came from: touches,
// subsystem, inferred, or none. A declared source always wins over an
// inferred one.
func ItemScopeSource(be backlog.Backend, id string) ([]string, string) {
	body, _, _ := be.Detail(id)
	if s := Scopes(body); len(s) > 0 {
		return s, SourceTouches
	}
	if it, err := be.Get(id); err == nil {
		if sub := strings.ToLower(strings.TrimSpace(it.Fields.Get("subsystem"))); sub != "" {
			return []string{"subsystem:" + sub}, SourceSubsystem
		}
	}
	if s := InferredScopes(be, id); len(s) > 0 {
		return s, SourceInferred
	}
	return nil, SourceNone
}

// InferredScopes is the newest scope comment of an item, parsed like a
// `## Touches` section; nil when none was recorded.
func InferredScopes(be backlog.Backend, id string) []string {
	cs, err := be.Comments(id, ScopeKind)
	if err != nil || len(cs) == 0 {
		return nil
	}
	return itembody.Entries(cs[len(cs)-1].Text)
}

// RecordInferred writes entries as the item's inferred scope, a comment of
// kind scope. The declared `## Touches` is not edited. Entries are normalised
// as Touches are; more than MaxInferred, or none, is refused.
func RecordInferred(be backlog.Backend, id string, entries []string) ([]string, error) {
	norm := itembody.Entries("- " + strings.Join(entries, "\n- "))
	if len(norm) == 0 {
		return nil, fmt.Errorf("no scope entries to record")
	}
	if len(norm) > MaxInferred {
		return nil, fmt.Errorf("%d scope entries: record at most %d", len(norm), MaxInferred)
	}
	text := "Inferred scope (explorer, warn-only):\n- " + strings.Join(norm, "\n- ")
	if _, err := be.AddComment(id, ScopeKind, text); err != nil {
		return nil, err
	}
	return norm, nil
}

// overlapDetail is the overlap check's line for one in-flight item.
func overlapDetail(with, slot string, paths, scopes []string, inferred bool) string {
	parts := make([]string, 0, 2)
	if len(paths) > 0 {
		parts = append(parts, strings.Join(paths, ", "))
	}
	if len(scopes) > 0 {
		label := "scopes "
		if inferred {
			label = "inferred scopes "
		}
		parts = append(parts, label+strings.Join(scopes, ", "))
	}
	return fmt.Sprintf("%s (held by %s): %s", with, slot, strings.Join(parts, "; "))
}
