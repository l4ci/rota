package backlog

import (
	"strings"
	"testing"

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
		want := Type{p[0], p[1], p[2], strings.Contains(p[3], "C"), strings.Contains(p[3], "P")}
		if Types[i] != want {
			t.Errorf("row %d: Types has %+v, registry says %+v", i, Types[i], want)
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
