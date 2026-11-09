package backlog

import (
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/golden"
)

// registry is the ROTA_TYPE_REGISTRY line of the retired shell type registry, frozen
// as it stood when the shell and Python helpers were deleted. Change it only
// together with Types.
const registry = "B:Bugs:bugs:CP F:Features:features:CP T:Tasks:tasks:P S:::P"

// Types must stay the frozen registry row for row.
func TestTypesMatchRegistry(t *testing.T) {
	rows := strings.Fields(registry)
	if len(rows) != len(Types) {
		t.Fatalf("registry has %d rows, Types has %d", len(rows), len(Types))
	}
	var letters string
	var sections []string
	for i, row := range rows {
		p := strings.Split(row, ":")
		if len(p) != 4 {
			t.Fatalf("malformed row %q", row)
		}
		got := Types[i]
		if got.Letter != p[0] || got.Section != p[1] || got.Kind != p[2] ||
			got.Countable != strings.Contains(p[3], "C") || got.Plannable != strings.Contains(p[3], "P") {
			t.Errorf("row %d: Types has %+v, registry says %s", i, got, row)
		}
		if p[1] != "" {
			letters += p[0]
			sections = append(sections, p[1])
		}
	}
	if letters != ItemLetters {
		t.Errorf("ItemLetters = %q, registry gives %q", ItemLetters, letters)
	}
	if strings.Join(sections, ",") != strings.Join(OpenSections, ",") {
		t.Errorf("OpenSections = %v, registry gives %v", OpenSections, sections)
	}
}

func TestTypesMatchPython(t *testing.T) {
	var countable, plannable string
	dirs, sections := map[string]any{}, map[string]any{}
	for _, ty := range Types {
		if ty.Countable {
			countable += ty.Letter
		}
		if ty.Plannable {
			plannable += ty.Letter
		}
		if ty.Kind != "" {
			dirs[ty.Letter], sections[ty.Kind] = ty.Kind, ty.Section
		}
	}
	golden.Check(t, map[string]any{"input": nil}, map[string]any{"items": ItemLetters, "countable": countable, "plannable": plannable,
		"dir": dirs, "section": sections, "open": OpenSections})
}

func TestTypeLookups(t *testing.T) {
	if ty, ok := TypeByLetter("F"); !ok || ty.Kind != "features" {
		t.Fatalf("TypeByLetter(F) = %+v, %v", ty, ok)
	}
	if ty, ok := TypeByKind("bugs"); !ok || ty.Letter != "B" {
		t.Fatalf("TypeByKind(bugs) = %+v, %v", ty, ok)
	}
	if _, ok := TypeByKind(""); ok {
		t.Fatal("TypeByKind(\"\") must not match the Slice row")
	}
	if _, ok := TypeByLetter("X"); ok {
		t.Fatal("unknown letter matched")
	}
}

// Every derived lookup must follow Types: a row added there without its
// section, tags or issue role fails here instead of drifting silently.
func TestDerivedLookupsCoverTypes(t *testing.T) {
	for _, ty := range Types {
		if ty.Kind == "" {
			continue
		}
		if got := typeLetter(ty.Kind); got != ty.Letter {
			t.Errorf("typeLetter(%q) = %q, want %q", ty.Kind, got, ty.Letter)
		}
		if got := sectionForDir(ty.Kind); got != ty.Section || got == "" {
			t.Errorf("sectionForDir(%q) = %q, want %q", ty.Kind, got, ty.Section)
		}
		if got := sectionOfLetter(ty.Letter); got != ty.Section {
			t.Errorf("sectionOfLetter(%q) = %q, want %q", ty.Letter, got, ty.Section)
		}
		if ty.Role == "" {
			t.Errorf("type %s has no issue role", ty.Letter)
		}
		if !ValidKind(ty.Kind) {
			t.Errorf("ValidKind(%q) = false", ty.Kind)
		}
		b := &Issues{Cfg: mustDecode(t, `{}`)}
		if got := b.Letter(Issue{Labels: []string{config.Label(b.Cfg, ty.Role)}}); got != ty.Letter {
			t.Errorf("Issues.Letter(role %s) = %q, want %q", ty.Role, got, ty.Letter)
		}
		if !ValidTag(ty.Kind, "") {
			t.Errorf("ValidTag(%q, \"\") = false", ty.Kind)
		}
		for _, tag := range strings.Fields(ty.Tags) {
			if !ValidTag(ty.Kind, tag) {
				t.Errorf("ValidTag(%q, %q) = false", ty.Kind, tag)
			}
		}
	}
}
