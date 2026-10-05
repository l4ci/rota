package notechunk

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/l4ci/rota/internal/marker"
)

func TestNorm(t *testing.T) {
	if got := Norm("a\r\nb\n\n"); got != "a\nb" {
		t.Fatalf("%q", got)
	}
}

func TestKeepLines(t *testing.T) {
	got := KeepLines("a\nb\r\nc\rd e")
	want := []string{"a\n", "b\r\n", "c\r", "d ", "e"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%q", got)
	}
	if KeepLines("") != nil {
		t.Fatal("empty input should give no lines")
	}
}

func TestPartsSingleAtLimit(t *testing.T) {
	hdr := marker.NoteHeader("plan", 1, 1)
	text := strings.Repeat("x", 20)
	limit := utf8.RuneCountInString(hdr) + 20
	got := Parts("plan", text+"\n", limit)
	if len(got) != 1 || got[0] != hdr+text {
		t.Fatalf("%q", got)
	}
	if got := Parts("plan", text, limit-1); len(got) < 2 {
		t.Fatalf("one rune over the limit must split: %q", got)
	}
}

func TestPartsSplitsOnLines(t *testing.T) {
	limit := utf8.RuneCountInString(marker.NoteHeader("plan", 99, 99)) + 10
	got := Parts("plan", "aaaa\nbbbb\ncccc\ndddd", limit)
	want := []string{
		marker.NoteHeader("plan", 1, 2) + "aaaa\nbbbb\n",
		marker.NoteHeader("plan", 2, 2) + "cccc\ndddd",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%q", got)
	}
}

func TestPartsCutsLongLineByRunes(t *testing.T) {
	budget := 7
	limit := utf8.RuneCountInString(marker.NoteHeader("plan", 99, 99)) + budget
	text := strings.Repeat("é", 20)
	got := Parts("plan", text, limit)
	if len(got) != 3 {
		t.Fatalf("%d parts: %q", len(got), got)
	}
	var joined string
	for i, p := range got {
		hdr := marker.NoteHeader("plan", i+1, 3)
		if !strings.HasPrefix(p, hdr) || utf8.RuneCountInString(p) > limit {
			t.Fatalf("part %d: %q", i, p)
		}
		joined += strings.TrimPrefix(p, hdr)
	}
	if joined != text {
		t.Fatalf("lost text: %q", joined)
	}
}

func TestPartsLongLineAfterShortOne(t *testing.T) {
	budget := 5
	limit := utf8.RuneCountInString(marker.NoteHeader("plan", 99, 99)) + budget
	got := Parts("plan", "ab\n"+strings.Repeat("x", 12), limit)
	// the pending "ab\n" flushes before the long line is cut
	if !strings.HasSuffix(got[0], "ab\n") || len(got) != 4 {
		t.Fatalf("%q", got)
	}
}
