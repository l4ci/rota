package notechunk_test

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/l4ci/rota/internal/marker"
	"github.com/l4ci/rota/internal/notechunk"
)

func TestPartsNumberingLimit(t *testing.T) {
	const limit = 80
	text := strings.Repeat("x", 6000)
	parts := notechunk.Parts("plan", text, limit)
	maxSize, oversized := 0, 0
	var joined strings.Builder
	for _, part := range parts {
		size := utf8.RuneCountInString(part)
		maxSize = max(maxSize, size)
		if size > limit {
			oversized++
		}
		note, ok := marker.ParseNote(part)
		if !ok {
			t.Fatalf("invalid note marker: %q", part)
		}
		joined.WriteString(note.Rest)
	}
	t.Logf("parts=%d max_runes=%d limit=%d oversized=%d", len(parts), maxSize, limit, oversized)
	if oversized != 0 {
		t.Errorf("%d complete comments exceed %d runes", oversized, limit)
	}
	if joined.String() != text {
		t.Error("reassembled note differs from input")
	}
}

func TestPartsNumberingTransitions(t *testing.T) {
	for _, kind := range []string{"plan", "plan:S12", "proof", "design", "acceptance"} {
		for _, tc := range []struct {
			name     string
			size     int
			min, max int
		}{
			{"below10", 400, 2, 9},
			{"above10", 600, 10, 99},
			{"below100", 4000, 10, 99},
			{"above100", 6000, 100, 999},
			{"below1000", 40000, 100, 999},
			{"above1000", 60000, 1000, 9999},
		} {
			t.Run(kind+"/"+tc.name, func(t *testing.T) {
				// Each rune is multibyte. CRLF and trailing newlines exercise
				// normalization without computing the expected body with Norm.
				text := strings.Repeat("界é\n", tc.size/3) + "fin"
				want := "intro\n" + text
				parts := notechunk.Parts(kind, "intro\r\n"+text+"\n\n", 80)
				if len(parts) < tc.min || len(parts) > tc.max {
					t.Fatalf("%d parts, want %d..%d", len(parts), tc.min, tc.max)
				}
				var joined strings.Builder
				for i, part := range parts {
					if size := utf8.RuneCountInString(part); size > 80 {
						t.Fatalf("part %d/%d has %d runes, limit 80", i+1, len(parts), size)
					}
					note, ok := marker.ParseNote(part)
					header := "<!-- rota:" + kind + " " + strconv.Itoa(i+1) + "/" + strconv.Itoa(len(parts)) + " -->\n"
					if !ok || note.Kind != kind || note.Part != strconv.Itoa(i+1) || !strings.HasPrefix(part, header) {
						t.Fatalf("invalid part %d marker: %q", i+1, part)
					}
					joined.WriteString(note.Rest)
				}
				if joined.String() != want {
					t.Fatal("reassembled note differs from normalized input")
				}
			})
		}
	}
}

func TestPartsEmpty(t *testing.T) {
	for _, text := range []string{"", "\n\r\n"} {
		if got := notechunk.Parts("plan", text, 80); !reflect.DeepEqual(got, []string{"<!-- rota:plan -->\n"}) {
			t.Fatalf("%q", got)
		}
	}
}

func TestPartsSingleAtLimit(t *testing.T) {
	hdr := marker.NoteHeader("plan", 1, 1)
	text := strings.Repeat("x", 20)
	limit := utf8.RuneCountInString(hdr) + 20
	got := notechunk.Parts("plan", text+"\n", limit)
	if len(got) != 1 || got[0] != hdr+text {
		t.Fatalf("%q", got)
	}
	if got := notechunk.Parts("plan", text, limit-1); len(got) < 2 {
		t.Fatalf("one rune over the limit must split: %q", got)
	}
}

func TestPartsSplitsOnLines(t *testing.T) {
	limit := utf8.RuneCountInString(marker.NoteHeader("plan", 2, 2)) + 10
	got := notechunk.Parts("plan", "aaaa\nbbbb\ncccc\ndddd", limit)
	want := []string{
		marker.NoteHeader("plan", 1, 2) + "aaaa\nbbbb\n",
		marker.NoteHeader("plan", 2, 2) + "cccc\ndddd",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%q", got)
	}
}

func TestPartsLineSeparators(t *testing.T) {
	for _, sep := range []string{"\n", "\r\n", "\r", "\v", "\f", "\x1c", "\x1d", "\x1e", "\u0085", "\u2028", "\u2029"} {
		t.Run(strconv.Quote(sep), func(t *testing.T) {
			first := "aa" + sep
			if sep == "\r\n" {
				first = "aa\n"
			}
			limit := utf8.RuneCountInString(marker.NoteHeader("plan", 2, 2)) + 4
			got := notechunk.Parts("plan", "aa"+sep+"bbb"+sep+"ccc", limit)
			middle := "bbb" + sep
			if sep == "\r\n" {
				middle = "bbb\n"
			}
			want := []string{"<!-- rota:plan 1/3 -->\n" + first, "<!-- rota:plan 2/3 -->\n" + middle, "<!-- rota:plan 3/3 -->\nccc"}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got %q, want %q", got, want)
			}
		})
	}
}

func TestPartsCutsLongLineByRunes(t *testing.T) {
	budget := 7
	limit := utf8.RuneCountInString(marker.NoteHeader("plan", 2, 2)) + budget
	text := strings.Repeat("é", 20)
	got := notechunk.Parts("plan", text, limit)
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
	limit := utf8.RuneCountInString(marker.NoteHeader("plan", 2, 2)) + budget
	got := notechunk.Parts("plan", "ab\n"+strings.Repeat("x", 12), limit)
	// the pending "ab\n" flushes before the long line is cut
	if !strings.HasSuffix(got[0], "ab\n") || len(got) != 4 {
		t.Fatalf("%q", got)
	}
}
