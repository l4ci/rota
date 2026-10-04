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
