package topicmatch

import (
	"reflect"
	"testing"
)

// TestVariantsSideBySide pushes the same requests through all three rules, so
// the differences between them are pinned in one table.
func TestVariantsSideBySide(t *testing.T) {
	entries := []Entry{
		{"Go", "go"},
		{"Go Tests", "go-tests"},
		{"Godot", "godot"},
		{"Godot", "godot-two"}, // same name twice: dedupe vs not
	}
	names := []string{"Go", "Go Tests", "Godot", "Godot"}
	slugs := func(idx []int) []string {
		var out []string
		for _, i := range idx {
			out = append(out, entries[i].Slug)
		}
		return out
	}
	tests := []struct {
		name     string
		want     string
		headings []string // Headings
		read     []string // ReadIndex slugs
		write    string   // WriteIndex slug, "" = no match
	}{
		{"exact wins alone", "go", []string{"go"}, []string{"go"}, "go"},
		{"case and space", "  GO  ", []string{"go"}, []string{"go"}, "go"},
		{"partial reads, write refuses", "tes", []string{"go tests"}, []string{"go-tests"}, ""},
		{"partial dupes: heading dedupes, index keeps both", "odo", []string{"godot"}, []string{"godot", "godot-two"}, ""},
		{"slug is exact-only for reads", "go-tests", nil, []string{"go-tests"}, "go-tests"},
		{"slug substring finds nothing", "tests", []string{"go tests"}, []string{"go-tests"}, ""},
		{"slugified spelling is write-only", "Go  Tests!", nil, nil, "go-tests"},
		{"empty", "  ", nil, nil, ""},
		{"unknown", "rust", nil, nil, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Headings(tc.want, names); !reflect.DeepEqual(got, tc.headings) {
				t.Errorf("Headings = %v, want %v", got, tc.headings)
			}
			if got := slugs(ReadIndex(tc.want, entries)); !reflect.DeepEqual(got, tc.read) {
				t.Errorf("ReadIndex = %v, want %v", got, tc.read)
			}
			i, ok := WriteIndex(tc.want, entries)
			got := ""
			if ok {
				got = entries[i].Slug
			}
			if ok != (tc.write != "") || got != tc.write {
				t.Errorf("WriteIndex = %d %v, want %q", i, ok, tc.write)
			}
		})
	}
}
