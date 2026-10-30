package cycle

import (
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/backlog"
)

func TestBranchName(t *testing.T) {
	long := "Move ship and work bookkeeping into rota ship finish and rota work start"
	for _, c := range []struct{ id, title, want string }{
		{"B07", "Quick switch overlay", "rota/b07-quick-switch-overlay"},
		{"2349", long, "rota/2349-move-ship-and-work-bookkeeping-into-rota"},
		{"M01-B07", "  Fix: the (weird) title!! ", "rota/m01-b07-fix-the-weird-title"},
		{"7", "", "rota/7"},
		{"7", "---", "rota/7"},
		{"12", "Ünïcode café", "rota/12-n-code-caf"},
	} {
		if got := BranchName(c.id, c.title); got != c.want {
			t.Errorf("BranchName(%q, %q) = %q, want %q", c.id, c.title, got, c.want)
		}
	}
}

func TestBranchNameLongTitleKeepsPrefix(t *testing.T) {
	got := BranchName("2349", "Move ship and work bookkeeping into rota ship finish and rota work start")
	if !strings.HasPrefix(got, "rota/2349-move-ship-and-work-bookkeeping") {
		t.Fatalf("got %q", got)
	}
}

func TestBranchNameSlugCap(t *testing.T) {
	got := BranchName("9", strings.Repeat("abcdefgh ", 20))
	slug := strings.TrimPrefix(got, "rota/9-")
	if len(slug) > slugMax || strings.HasSuffix(slug, "-") || strings.Contains(slug, "--") {
		t.Fatalf("slug %q breaks the cap (%d)", slug, slugMax)
	}
	if got != "rota/9-abcdefgh-abcdefgh-abcdefgh-abcdefgh" {
		t.Fatalf("got %q: want a cut on a word boundary", got)
	}
	if got := BranchName("9", strings.Repeat("x", 80)); len(got) > len("rota/9-")+slugMax {
		t.Fatalf("one long word: %q", got)
	}
}

func TestBranchNameKeepsIssueRef(t *testing.T) {
	if got := backlog.BranchIssueRef(BranchName("2349", "Move ship and work bookkeeping")); got != "#2349" {
		t.Fatalf("BranchIssueRef = %q, want #2349", got)
	}
}
