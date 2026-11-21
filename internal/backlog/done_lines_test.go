package backlog

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The four done-line readers must agree on a done line however it is
// framed: LF, CRLF or indented.
func TestDoneLineReadersAgreeOnFraming(t *testing.T) {
	const line = "- ~~**[B07] [P1] Thing.** x Milestone: M01~~ Done 2026-01-02 [`abc1234`]"
	for name, framed := range map[string]string{
		"lf":       line + "\n",
		"crlf":     line + "\r\n",
		"indented": "  " + line + "\n",
		"after-cr": "junk\r" + line + "\n",
		"after-ls": "junk\u2028" + line + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			text := "# Completed\n" + framed
			if got := doneIDs(text); !slices.Equal(got, []string{"B07"}) {
				t.Errorf("doneIDs = %v", got)
			}
			if _, _, d, ok := findDoneIn(text, "B07"); !ok || d.ID != "B07" {
				t.Errorf("findDoneIn ok=%v d=%+v", ok, d)
			}
			root := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, ".rota"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, ".rota", "BACKLOG.md"), []byte(text), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := CycleIDs(root, map[string]bool{"abc1234": true}); !slices.Equal(got, []string{"B07"}) {
				t.Errorf("CycleIDs = %v", got)
			}
			f := fileBackend(t, "## Completed\n"+framed)
			if moved, err := f.Archive(0, day(t, "2027-01-01")); err != nil || moved != 1 {
				t.Errorf("Archive = %d, %v", moved, err)
			}
			refs := closedMilestoneItems(text, "M01", map[string]bool{})
			if len(refs) != 1 || refs[0].ID != "B07" {
				t.Errorf("closedMilestoneItems = %+v", refs)
			}
		})
	}
}

// findDoneIn's offsets stay raw: removing [start,end) plus the newline
// leaves the other lines untouched.
func TestFindDoneInOffsetsAreRaw(t *testing.T) {
	a := "- ~~**[B01] A**~~ Done 2026-01-02 [`aaa1111`]"
	b := "  - ~~**[B02] B**~~ Done 2026-01-02 [`bbb2222`]\r"
	text := a + "\n" + b + "\nend\n"
	start, end, d, ok := findDoneIn(text, "B02")
	if !ok || d.ID != "B02" {
		t.Fatalf("not found: %v %+v", ok, d)
	}
	if text[start:end] != b {
		t.Errorf("span %q, want %q", text[start:end], b)
	}
}

// A CRLF done line is cut whole: the CR goes with its LF.
func TestFindDoneInSpansCRLF(t *testing.T) {
	text := "a\n- ~~**[B02] [P1] B.** x~~ Done 2026-01-02 [`bbb2222`]\r\nz\n"
	start, end, _, ok := findDoneIn(text, "B02")
	if !ok || strings.HasPrefix(text[end:], "\r") || text[:start]+text[end+1:] != "a\nz\n" {
		t.Errorf("start=%d end=%d ok=%v", start, end, ok)
	}
}
