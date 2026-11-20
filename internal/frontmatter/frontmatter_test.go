package frontmatter

import (
	"reflect"
	"testing"
)

func TestParse(t *testing.T) {
	fm, order, body := Parse("---\nname: x\n# c\ndepends: [a, b,]\nempty: []\n---\n\nbody\n")
	if Str(fm, "name") != "x" || !reflect.DeepEqual(fm["depends"], []string{"a", "b"}) || !reflect.DeepEqual(fm["empty"], []string{}) {
		t.Fatalf("fm = %#v", fm)
	}
	if !reflect.DeepEqual(order, []string{"name", "depends", "empty"}) || body != "body\n" {
		t.Fatalf("order %v body %q", order, body)
	}
	for _, in := range []string{"no frontmatter", "---\nunterminated: yes\n", "---\n---\nx"} {
		if fm, _, b := Parse(in); fm != nil || b != in {
			t.Errorf("%q: got %v %q", in, fm, b)
		}
	}
}

func TestUpdateField(t *testing.T) {
	in := "---\nname: s\nstatus: open\n---\nstatus: open\n"
	got, ok := UpdateField(in, "status", "done")
	if !ok || got != "---\nname: s\nstatus: done\n---\nstatus: open\n" {
		t.Fatalf("got %q %v", got, ok)
	}
	if _, ok := UpdateField(in, "missing", "x"); ok {
		t.Error("missing field reported found")
	}
	if _, ok := UpdateField("status: open\n", "status", "x"); ok {
		t.Error("no frontmatter reported found")
	}
}

func TestCRLF(t *testing.T) {
	fm, _, body := Parse("---\r\nname: x\r\ndepends: [a, b]\r\n---\r\nbody\r\n")
	if Str(fm, "name") != "x" || len(fm["depends"].([]string)) != 2 || body != "body\r\n" {
		t.Fatalf("fm=%v body=%q", fm, body)
	}
	got, ok := UpdateField("---\r\nstatus: open\r\n---\r\n", "status", "done")
	if !ok || got != "---\r\nstatus: done\r\n---\r\n" {
		t.Fatalf("got %q %v", got, ok)
	}
}

func TestSetField(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"replace", "---\nstatus: open\nfinished: old\n---\nb\n", "---\nstatus: open\nfinished: D\n---\nb\n"},
		{"insert after", "---\nstatus: done\nz: 1\n---\nb\n", "---\nstatus: done\nfinished: D\nz: 1\n---\nb\n"},
		{"insert after crlf", "---\r\nstatus: done\r\nz: 1\r\n---\r\nb\r\n", "---\r\nstatus: done\r\nfinished: D\r\nz: 1\r\n---\r\nb\r\n"},
		{"after absent goes last", "---\nz: 1\n---\nb\n", "---\nz: 1\nfinished: D\n---\nb\n"},
		{"body line ignored", "---\nstatus: done\n---\nfinished: body\n", "---\nstatus: done\nfinished: D\n---\nfinished: body\n"},
		{"no frontmatter", "plain\n", "plain\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := SetField(tc.in, "finished", "D", "status")
			if got != tc.want || ok != (tc.name != "no frontmatter") {
				t.Errorf("SetField = (%q, %v), want %q", got, ok, tc.want)
			}
		})
	}
}

// An empty value must not let UpdateField reach the next line's key.
func TestUpdateFieldEmptyValueKeepsNextKey(t *testing.T) {
	in := "---\nstatus:\nowner: x\n---\nbody\n"
	got, ok := UpdateField(in, "status", "DONE")
	if ok || got != in {
		t.Fatalf("ok=%v got %q, want untouched and not found", ok, got)
	}
	in = "---\nstatus:  \nowner: x\n---\n"
	if got, ok := UpdateField(in, "status", "DONE"); ok || got != in {
		t.Fatalf("trailing blanks: ok=%v got %q", ok, got)
	}
	if got, ok := UpdateField("---\nstatus: open\nowner: x\n---\n", "status", "DONE"); !ok || got != "---\nstatus: DONE\nowner: x\n---\n" {
		t.Fatalf("populated: ok=%v got %q", ok, got)
	}
}

func TestRenderRoundTrips(t *testing.T) {
	for _, title := range []string{"plain", "a: b", "#hash", "x --- y", "tail: [z]"} {
		out, err := Render([]Field{{"id", "A1"}, {"title", title}, {"depends", []string{"M01", "M02"}}})
		if err != nil {
			t.Fatalf("%q: %v", title, err)
		}
		fm, order, _ := Parse(out + "\nbody\n")
		if Str(fm, "title") != title || Str(fm, "id") != "A1" || !reflect.DeepEqual(fm["depends"], []string{"M01", "M02"}) {
			t.Errorf("%q: fm = %#v", title, fm)
		}
		if !reflect.DeepEqual(order, []string{"id", "title", "depends"}) {
			t.Errorf("%q: order = %v", title, order)
		}
	}
}

func TestRenderRefusesUnsafeValues(t *testing.T) {
	for _, title := range []string{"a\nstatus: done", "a\r\nb", "[a, b]", " lead", "trail "} {
		if out, err := Render([]Field{{"title", title}}); err == nil {
			t.Errorf("%q rendered as %q, want a refusal", title, out)
		}
	}
	if _, err := Render([]Field{{"bad key", "v"}}); err == nil {
		t.Error("key with a space accepted")
	}
	if _, err := Render([]Field{{"k", []string{"a, b"}}}); err == nil {
		t.Error("list item with a comma accepted")
	}
}

func TestRenderSkipsEmptyOptional(t *testing.T) {
	out, err := Render([]Field{{"a", "1"}, {"repo", Optional("")}, {"b", Optional("2")}})
	if err != nil || out != "---\na: 1\nb: 2\n---\n" {
		t.Fatalf("got %q, %v", out, err)
	}
}
